package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
	"fishnet/internal/doc"
	"fishnet/internal/graph"
	"fishnet/internal/llm"
	"fishnet/internal/nlp"
	"fishnet/internal/task"
)

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Read documents and build knowledge graph",
	Long: `Read all .txt/.md/.rst/.csv/.json files from the source directory,
chunk them, extract entities, and store in the local graph.

By default uses local ONNX NER (no API key needed, Chinese+English, fast).
Use --llm to switch to cloud LLM extraction.`,
	Example: `  fishnet analyze
  fishnet analyze --dir ./docs
  fishnet analyze --llm --dir ./reports
  fishnet analyze --resume task-20240409-143022`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		chunkSize, _ := cmd.Flags().GetInt("chunk-size")
		chunkOverlap, _ := cmd.Flags().GetInt("chunk-overlap")
		skipGraph, _ := cmd.Flags().GetBool("docs-only")
		community, _ := cmd.Flags().GetBool("community")
		useOntology, _ := cmd.Flags().GetBool("ontology")
		resumeTaskID, _ := cmd.Flags().GetString("resume")
		useLLM, _ := cmd.Flags().GetBool("llm")
		useOnnx := !useLLM // ONNX is the default; LLM is opt-in

		if cfg.LLM.APIKey == "" {
			if k := os.Getenv("FISHNET_API_KEY"); k != "" {
				cfg.LLM.APIKey = k
			} else if k := os.Getenv("OPENAI_API_KEY"); k != "" {
				cfg.LLM.APIKey = k
			} else if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
				cfg.LLM.APIKey = k
			}
		}

		// Resolve dir
		if dir == "" {
			dir = "."
		}
		absDir, _ := filepath.Abs(dir)

		// Open DB
		database, err := db.Open(cfg.DBPath)
		if err != nil {
			die("open db", err)
		}
		defer database.Close()

		// Get or auto-create project
		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			projectID, err = database.UpsertProject(cfg.Project, absDir)
			if err != nil {
				die("create project", err)
			}
			fmt.Printf("%s Auto-created project %s\n", yellow("!"), bold(cfg.Project))
		}

		taskMgr := task.NewManager(".")

		// ── Resume mode: skip ingestion, jump to extraction ─────────────────────
		if resumeTaskID != "" {
			t, err := taskMgr.Load(resumeTaskID)
			if err != nil {
				return fmt.Errorf("resume: %w", err)
			}
			fmt.Printf("%s Resuming task %s (was %s, %d/%d chunks done)\n",
				cyan("→"), bold(t.ID), t.Status, t.ChunksDone, t.ChunksTotal)
			absDir = t.Dir
			useOntology = t.HasOntology
			community = t.HasCommunity
			return runExtraction(database, projectID, absDir, concurrency, useOntology, community, useOnnx, t, taskMgr)
		}

		// ── Step 1: Read documents ───────────────────────────────────────────────
		fmt.Printf("\n%s Reading documents from %s\n", cyan("→"), bold(absDir))
		docs, err := doc.ReadDir(absDir)
		if err != nil {
			die("read dir", err)
		}
		if len(docs) == 0 {
			return fmt.Errorf("no supported documents found in %s", absDir)
		}
		fmt.Printf("  Found %d documents\n", len(docs))

		if chunkSize <= 0 {
			chunkSize = cfg.Graph.ChunkSize
		}
		if chunkOverlap <= 0 {
			chunkOverlap = cfg.Graph.ChunkOverlap
		}

		// ── Step 2: Chunk & store ────────────────────────────────────────────────
		fmt.Printf("\n%s Chunking (size=%d overlap=%d)...\n", cyan("→"), chunkSize, chunkOverlap)
		totalChunks := 0
		for _, d := range docs {
			chunks := doc.Chunk(d.Content, chunkSize, chunkOverlap)
			docID, err := database.AddDocument(projectID, d.Path, d.Name, d.Content, len(chunks))
			if err != nil {
				fmt.Printf("  skip %s: %v\n", d.Name, err)
				continue
			}
			for i, c := range chunks {
				database.AddChunk(docID, projectID, c, i)
			}
			totalChunks += len(chunks)
			fmt.Printf("  %-30s → %d chunks\n", d.Name, len(chunks))
		}
		fmt.Printf("  Total: %d chunks\n", totalChunks)

		if skipGraph {
			fmt.Printf("\n%s Docs loaded (--docs-only). Run without flag to extract graph.\n", green("✓"))
			return nil
		}

		// Create task record before starting extraction
		t, taskErr := taskMgr.Create(projectID, absDir, int64(totalChunks), useOntology, community)
		if taskErr == nil {
			fmt.Printf("%s Task created: %s\n", cyan("→"), bold(t.ID))
		}

		return runExtraction(database, projectID, absDir, concurrency, useOntology, community, useOnnx, t, taskMgr)
	},
}

// runExtraction runs steps 3–4 (graph extraction + community detection) and
// manages the task lifecycle. t may be nil if task creation failed.
func runExtraction(
	database *db.DB,
	projectID, absDir string,
	concurrency int,
	useOntology, community, useOnnx bool,
	t *task.GraphTask,
	taskMgr *task.Manager,
) error {
	// Handle interrupt: mark task as interrupted
	if t != nil {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			t.MarkInterrupted()
			_ = taskMgr.Save(t)
			fmt.Printf("\n%s Build interrupted. Resume with: fishnet analyze --resume %s\n",
				yellow("!"), t.ID)
			os.Exit(130)
		}()
	}

	// ── Step 3: Extract graph ────────────────────────────────────────────────
	chunks, err := database.UnprocessedChunks(projectID)
	if err != nil {
		die("get chunks", err)
	}
	if len(chunks) == 0 {
		fmt.Printf("\n%s All chunks already processed.\n", green("✓"))
		if t != nil {
			t.MarkCompleted()
			_ = taskMgr.Save(t)
		}
	} else {
		// Download ONNX model on first use (one-time, ~86 MB).
		if useOnnx {
			modelReady, _ := nlp.IsModelReady()
			if !modelReady {
				fmt.Printf("\n%s First run: downloading ONNX NER model (~86 MB, one-time)...\n", cyan("→"))
				if err := nlp.EnsureModels(true); err != nil {
					if cfg.LLM.APIKey != "" || cfg.LLM.Provider == "ollama" {
						fmt.Printf("%s Model download failed, falling back to LLM extraction.\n", yellow("!"))
						useOnnx = false
					} else {
						return fmt.Errorf("model download failed: %w", err)
					}
				} else {
					fmt.Printf("%s Model ready.\n", green("✓"))
				}
			}
		}

		// LLM client — only needed for LLM extraction or ontology; nil is fine for ONNX.
		var client *llm.Client
		if !useOnnx || useOntology {
			client = llm.New(cfg.LLM)
		}
		if concurrency <= 0 {
			concurrency = cfg.LLM.MaxConcurrency
		}

		ctx := context.Background()

		// ── Step 3a: Generate ontology (optional, LLM only) ────────────────────
		var builderCfg graph.Config
		if useOnnx {
			builderCfg.ExtractionMode = "onnx"
			useOntology = false // ontology generation requires LLM
		}
		if useOntology {
			docs, _ := doc.ReadDir(absDir)
			if len(docs) > 0 {
				fmt.Printf("\n%s Generating ontology schema...\n", cyan("→"))
				sample := docs[0].Content
				if len(sample) > 3000 {
					sample = sample[:3000]
				}
				schema, ontErr := graph.GenerateOntology(ctx, client, sample)
				if ontErr == nil {
					builderCfg.Schema = schema
					fmt.Printf("  Entity types: ")
					for i, et := range schema.EntityTypes {
						if i > 0 {
							fmt.Printf(", ")
						}
						fmt.Printf("%s", et.Name)
					}
					fmt.Println()
					fmt.Printf("  Edge types:   ")
					for i, et := range schema.EdgeTypes {
						if i > 0 {
							fmt.Printf(", ")
						}
						fmt.Printf("%s", et.Name)
					}
					fmt.Println()
				}
			}
		}

		if t != nil {
			t.MarkRunning()
			t.ChunksTotal = int64(len(chunks))
			_ = taskMgr.Save(t)
		}

		fmt.Printf("\n%s Extracting graph from %d chunks (concurrency=%d)...\n",
			cyan("→"), len(chunks), concurrency)

		start := time.Now()
		builder := graph.NewBuilderWithConfig(database, client, builderCfg)

		lastPct := -1
		lastSavePct := -1
		result, err := builder.BuildFromChunks(ctx, projectID, chunks, concurrency,
			func(p graph.Progress) {
				pct := int(p.Done * 100 / int64(p.Total))
				if pct != lastPct && pct%5 == 0 {
					lastPct = pct
					bar := progressBar(pct, 30)
					fmt.Printf("\r  [%s] %d%% (+%d nodes, +%d edges)",
						bar, pct, p.NodesAdded, p.EdgesAdded)
				}
				// Persist task progress every 10%
				if t != nil && pct != lastSavePct && pct%10 == 0 {
					lastSavePct = pct
					t.ChunksDone = p.Done
					t.NodesAdded = p.NodesAdded
					t.EdgesAdded = p.EdgesAdded
					_ = taskMgr.Save(t)
				}
			})
		fmt.Println()
		if err != nil {
			if t != nil {
				t.MarkFailed(err.Error())
				_ = taskMgr.Save(t)
			}
			die("extract graph", err)
		}

		elapsed := time.Since(start).Round(time.Second)
		fmt.Printf("  Done in %s: +%d nodes, +%d edges (%d errors)\n",
			elapsed, result.NodesAdded, result.EdgesAdded, result.Errors)

		if t != nil {
			t.NodesAdded = result.NodesAdded
			t.EdgesAdded = result.EdgesAdded
			t.Errors = result.Errors
		}
	}

	// ── Step 4: Community detection (optional) ───────────────────────────────
	if community {
		fmt.Printf("\n%s Running community detection...\n", cyan("→"))
		client := llm.New(cfg.LLM)
		results, err := graph.RunCommunityDetection(
			context.Background(), database, client, projectID,
			cfg.Graph.CommunityMinSize)
		if err != nil {
			fmt.Printf("  %s community detection failed: %v\n", yellow("!"), err)
		} else {
			fmt.Printf("  Found %d communities\n", len(results))
			for _, c := range results {
				fmt.Printf("  [%d] %d nodes — %s\n", c.ID, len(c.Nodes), c.Summary)
			}
		}
	}

	// ── Summary ──────────────────────────────────────────────────────────────
	stats := database.GetStats(projectID)
	fmt.Printf("\n%s Graph ready: %d nodes, %d edges, %d communities\n",
		green("✓"), stats.Nodes, stats.Edges, stats.Communities)
	fmt.Printf("  Run: fishnet graph web   — to visualize\n")
	fmt.Printf("       fishnet sim run     — to simulate\n")

	if t != nil {
		t.MarkCompleted()
		_ = taskMgr.Save(t)
		fmt.Printf("  Task: %s\n", t.ID)
	}

	return nil
}

func progressBar(pct, width int) string {
	filled := pct * width / 100
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	return bar
}

func init() {
	analyzeCmd.Flags().String("dir", "", "Documents directory (default: from config)")
	analyzeCmd.Flags().Int("concurrency", 0, "Concurrent LLM calls (default: from config)")
	analyzeCmd.Flags().Int("chunk-size", 0, "Characters per chunk (default: from config)")
	analyzeCmd.Flags().Int("chunk-overlap", 0, "Overlap between chunks (default: from config)")
	analyzeCmd.Flags().Bool("docs-only", false, "Only load docs, skip graph extraction")
	analyzeCmd.Flags().Bool("community", false, "Run community detection after extraction")
	analyzeCmd.Flags().Bool("ontology", true, "Generate domain ontology before extraction (default: true)")
	analyzeCmd.Flags().String("resume", "", "Resume an interrupted build task by task ID")
	analyzeCmd.Flags().Bool("llm", false, "Use cloud LLM for extraction instead of local ONNX (requires API key)")
	rootCmd.AddCommand(analyzeCmd)
}
