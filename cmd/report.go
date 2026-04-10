package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
	"fishnet/internal/interaction"
	"fishnet/internal/llm"
	"fishnet/internal/report"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Report generation commands",
}

// ─── report generate ──────────────────────────────────────────────────────────

var reportGenerateCmd = &cobra.Command{
	Use:     "generate",
	Aliases: []string{"gen"},
	Short:   "Generate a simulation analysis report",
	Long:    `Runs the ReACT report agent to produce a full Markdown analysis of the knowledge graph.`,
	Example: `  fishnet report generate --scenario "AI regulation debate"
  fishnet report generate --scenario "Product launch" --output report.md`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scenario, _ := cmd.Flags().GetString("scenario")
		output, _ := cmd.Flags().GetString("output")

		if scenario == "" {
			return fmt.Errorf("--scenario is required")
		}
		resolveAPIKey()

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		client := llm.New(cfg.LLM)
		agent := report.New(database, client)

		// Create a ReportTask to track lifecycle
		taskMgr := report.NewTaskManager(".")
		rtask := &report.Task{
			Scenario:  scenario,
			ProjectID: projectID,
			Status:    "pending",
		}

		fmt.Printf("\n%s Generating report for: %s\n\n", cyan("→"), bold(scenario))

		ctx := context.Background()

		// Collect sections during generation; finalize task after Generate returns.
		var sectionRecords []report.SectionRecord
		r, err := agent.Generate(ctx, projectID, scenario, func(s report.Section) {
			fmt.Printf("  %s §%d %s\n", green("✓"), s.Index, s.Title)
			sectionRecords = append(sectionRecords, report.SectionRecord{
				Index:     s.Index,
				Title:     s.Title,
				Completed: true,
			})
		})

		// Finalize task using the report ID now that Generate has returned.
		if r != nil && r.ID != "" {
			rtask.ID = r.ID
			rtask.ReportFile = ".fishnet/reports/" + r.ID + ".md"
			rtask.LogFile = ".fishnet/reports/" + r.ID + ".jsonl"
			rtask.Sections = sectionRecords
			rtask.SectionsTotal = len(r.Sections)
			rtask.SectionsDone = len(sectionRecords)
		}

		if err != nil {
			if rtask.ID != "" {
				rtask.MarkFailed(err.Error())
				_ = taskMgr.Save(rtask)
			}
			return err
		}

		rtask.MarkCompleted()
		if taskMgr.Save(rtask) == nil && rtask.ID != "" {
			fmt.Printf("\n  Report ID: %s\n", bold(rtask.ID))
		}

		md := r.FormatMarkdown()
		fmt.Println()
		fmt.Println(md)

		if output != "" {
			if err := os.WriteFile(output, []byte(md), 0644); err != nil {
				fmt.Printf("%s Could not write output: %v\n", yellow("!"), err)
			} else {
				fmt.Printf("%s Report saved to %s\n", green("✓"), output)
			}
		}
		return nil
	},
}

// ─── report status ────────────────────────────────────────────────────────────

var reportStatusCmd = &cobra.Command{
	Use:   "status <report-id>",
	Short: "Show status of a report generation task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskMgr := report.NewTaskManager(".")
		t, err := taskMgr.Load(args[0])
		if err != nil {
			return err
		}
		statusFn := cyan
		switch t.Status {
		case "completed":
			statusFn = green
		case "failed":
			statusFn = yellow
		}
		fmt.Printf("\n%s  %s\n", bold("Report:"), t.ID)
		fmt.Printf("  Status:   %s\n", statusFn(t.Status))
		fmt.Printf("  Scenario: %s\n", t.Scenario)
		fmt.Printf("  Progress: %d%%  (%d/%d sections)\n", t.Progress(), t.SectionsDone, t.SectionsTotal)
		if t.ReportFile != "" {
			fmt.Printf("  File:     %s\n", t.ReportFile)
		}
		if t.LogFile != "" {
			fmt.Printf("  Logs:     fishnet report logs %s\n", t.ID)
		}
		if t.ErrorMsg != "" {
			fmt.Printf("  Error:    %s\n", red(t.ErrorMsg))
		}
		fmt.Println()
		return nil
	},
}

// ─── report sections ──────────────────────────────────────────────────────────

var reportSectionsCmd = &cobra.Command{
	Use:   "sections <report-id>",
	Short: "List completed sections of a report",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskMgr := report.NewTaskManager(".")
		t, err := taskMgr.Load(args[0])
		if err != nil {
			return err
		}
		if len(t.Sections) == 0 {
			fmt.Printf("No sections recorded for report %s\n", args[0])
			return nil
		}
		fmt.Printf("\n%s  %s — %d sections\n\n", bold("Report:"), t.ID, len(t.Sections))
		for _, s := range t.Sections {
			mark := green("✓")
			if !s.Completed {
				mark = yellow("…")
			}
			fmt.Printf("  %s §%d  %s\n", mark, s.Index, s.Title)
		}
		fmt.Println()
		return nil
	},
}

// ─── report logs ─────────────────────────────────────────────────────────────

var reportLogsCmd = &cobra.Command{
	Use:   "logs <report-id>",
	Short: "Show reasoning log (tool calls, search results) for a report",
	Example: `  fishnet report logs <report-id>
  fishnet report logs <report-id> --stage tool_call`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stageFilter, _ := cmd.Flags().GetString("stage")
		limit, _ := cmd.Flags().GetInt("limit")

		entries, err := report.ReadLogEntries(".", args[0])
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Printf("No log entries for report %s\n", args[0])
			return nil
		}

		fmt.Printf("\n%s  Log for %s  (%d entries)\n\n", bold("Report:"), args[0], len(entries))

		shown := 0
		for _, e := range entries {
			if stageFilter != "" && e.Stage != stageFilter {
				continue
			}
			if limit > 0 && shown >= limit {
				break
			}
			shown++

			stageColor := cyan
			switch e.Stage {
			case "complete":
				stageColor = green
			case "tool_call":
				stageColor = yellow
			case "planning":
				stageColor = bold
			}

			section := ""
			if e.Section != "" {
				section = "  [" + e.Section + "]"
			}
			tool := ""
			if e.Tool != "" {
				tool = "  " + cyan(e.Tool)
			}
			content := e.Content
			if len([]rune(content)) > 120 {
				content = string([]rune(content)[:117]) + "…"
			}

			fmt.Printf("  %s  %s%s%s\n    %s\n\n",
				e.Time[:min(19, len(e.Time))],
				stageColor(e.Stage),
				section,
				tool,
				content)
		}
		return nil
	},
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─── report chat ─────────────────────────────────────────────────────────────

var reportChatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Chat with the Report Agent (uses knowledge graph + simulation data)",
	Long: `Interactive Q&A with the Report Agent. The agent can call tools to search
the knowledge graph, interview personas, and analyse simulation results.
Use --session to resume a past conversation.`,
	Example: `  fishnet report chat
  fishnet report chat --sim <sim_id>
  fishnet report chat --session rchat-20240409-143022`,
	RunE: func(cmd *cobra.Command, args []string) error {
		simID, _ := cmd.Flags().GetString("sim")
		sessionRef, _ := cmd.Flags().GetString("session")

		resolveAPIKey()

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		client := llm.New(cfg.LLM)
		agent := report.New(database, client)
		ctx := context.Background()

		intMgr := interaction.NewManager(".")

		// Load or create interaction session
		var sess *interaction.Session
		if sessionRef != "" {
			sess, err = intMgr.Load(sessionRef)
			if err != nil {
				return fmt.Errorf("load session: %w", err)
			}
			fmt.Printf("%s Resuming chat session %s (%d turns)\n\n",
				cyan("→"), sess.ID, len(sess.History)/2)
		} else {
			sess, err = intMgr.Create(interaction.TypeReportChat, "", "")
			if err != nil {
				fmt.Printf("%s Warning: could not create interaction session: %v\n", yellow("!"), err)
			}
		}

		fmt.Printf("%s Report Agent  (type 'exit' to quit", cyan("→"))
		if sess != nil {
			fmt.Printf(", session: %s", sess.ID)
		}
		fmt.Printf(")\n\n")

		var history []llm.Message
		if sess != nil {
			history = sess.History
		}

		for {
			fmt.Print(bold("you") + ": ")
			line, err := readLine()
			if err != nil || strings.ToLower(strings.TrimSpace(line)) == "exit" {
				break
			}
			q := strings.TrimSpace(line)
			if q == "" {
				continue
			}

			resp, err := agent.Chat(ctx, projectID, simID, q, history)
			if err != nil {
				fmt.Printf("%s %v\n", red("✗"), err)
				continue
			}

			history = append(history,
				llm.Message{Role: "user", Content: q},
				llm.Message{Role: "assistant", Content: resp},
			)

			fmt.Printf("\n%s %s\n\n", cyan("agent:"), resp)

			// Persist history
			if sess != nil {
				sess.History = history
				_ = intMgr.Save(sess)
			}
		}
		return nil
	},
}

// ─── interview ────────────────────────────────────────────────────────────────

var interviewCmd = &cobra.Command{
	Use:     "interview [agent]",
	Aliases: []string{"chat"},
	Short:   "Interview a graph node as an in-character persona",
	Long:    `Start an interactive Q&A session with a graph entity speaking as their persona.`,
	Example: `  fishnet interview Alice
  fishnet interview "Elon Musk" --question "What do you think about AI regulation?"
  fishnet interview --all --question "What do you think about {scenario}?"
  fishnet interview --batch "Alice,Bob,Carol" --question "What's your stance?"`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		question, _ := cmd.Flags().GetString("question")
		allFlag, _ := cmd.Flags().GetBool("all")
		batchFlag, _ := cmd.Flags().GetString("batch")
		saveFlag, _ := cmd.Flags().GetBool("save")

		resolveAPIKey()

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		client := llm.New(cfg.LLM)
		interviewAgent := report.New(database, client)
		ctx := context.Background()
		intMgr := interaction.NewManager(".")

		// ── Batch / All mode ──────────────────────────────────────────────────
		if allFlag || batchFlag != "" {
			if question == "" {
				return fmt.Errorf("--question is required for --all/--batch mode")
			}

			var nodes []db.Node

			if allFlag {
				nodes, err = database.GetNodes(projectID)
				if err != nil {
					return err
				}
			} else {
				allNodes, err := database.GetNodes(projectID)
				if err != nil {
					return err
				}
				nodeByName := make(map[string]db.Node, len(allNodes))
				for _, n := range allNodes {
					nodeByName[strings.ToLower(n.Name)] = n
				}
				for _, name := range strings.Split(batchFlag, ",") {
					name = strings.TrimSpace(name)
					if name == "" {
						continue
					}
					if n, ok := nodeByName[strings.ToLower(name)]; ok {
						nodes = append(nodes, n)
					} else {
						fmt.Printf("%s Agent %q not found in graph, skipping\n", yellow("!"), name)
					}
				}
			}

			if len(nodes) == 0 {
				return fmt.Errorf("no agents found to interview")
			}

			fmt.Printf("\n%s Batch interview: %d agents\n  Question: %s\n\n",
				cyan("→"), len(nodes), question)

			sem := make(chan struct{}, 4)
			var mu sync.Mutex
			var wg sync.WaitGroup

			for _, n := range nodes {
				wg.Add(1)
				go func(node db.Node) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()

					resp, _, err := interviewAgent.Interview(ctx, projectID, node.Name, question, nil)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						fmt.Printf("%s [%s]: error: %v\n", red("✗"), node.Name, err)
					} else {
						fmt.Printf("%s %s\n\n", cyan("["+node.Name+"]:"), resp)
					}
				}(n)
			}
			wg.Wait()
			return nil
		}

		// ── Single agent mode ─────────────────────────────────────────────────
		if len(args) == 0 {
			return fmt.Errorf("provide an agent name or use --all / --batch")
		}
		agentRef := args[0]

		// Single-question mode
		if question != "" {
			resp, _, err := interviewAgent.Interview(ctx, projectID, agentRef, question, nil)
			if err != nil {
				return err
			}
			fmt.Printf("\n%s %s\n\n", cyan(agentRef+":"), resp)
			if saveFlag {
				sess, _ := intMgr.Create(interaction.TypeInterview, agentRef, question)
				if sess != nil {
					sess.History = []llm.Message{
						{Role: "user", Content: question},
						{Role: "assistant", Content: resp},
					}
					_ = intMgr.Save(sess)
					fmt.Printf("%s Saved as interaction: %s\n", green("✓"), sess.ID)
				}
			}
			return nil
		}

		// Interactive REPL mode
		fmt.Printf("\n%s Interviewing %s  (type 'exit' to quit)\n\n",
			cyan("→"), bold(agentRef))

		var sess *interaction.Session
		if saveFlag {
			sess, _ = intMgr.Create(interaction.TypeInterview, agentRef, "")
			if sess != nil {
				fmt.Printf("  Session: %s\n\n", sess.ID)
			}
		}

		var history []llm.Message
		for {
			fmt.Print(S("you") + ": ")
			line, err := readLine()
			if err != nil || strings.ToLower(strings.TrimSpace(line)) == "exit" {
				break
			}
			q := strings.TrimSpace(line)
			if q == "" {
				continue
			}

			resp, msg, err := interviewAgent.Interview(ctx, projectID, agentRef, q, history)
			if err != nil {
				fmt.Printf("%s %v\n", red("✗"), err)
				continue
			}
			history = append(history, llm.Message{Role: "user", Content: q})
			history = append(history, msg)
			fmt.Printf("\n%s %s\n\n", cyan(agentRef+":"), resp)

			if sess != nil {
				sess.History = history
				_ = intMgr.Save(sess)
			}
		}
		return nil
	},
}

// S wraps a string in bold ANSI for prompts.
func S(s string) string { return bold(s) }

// readLine reads one line from stdin.
func readLine() (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(b)
		if err != nil {
			return string(buf), err
		}
		if b[0] == '\n' {
			break
		}
		buf = append(buf, b[0])
	}
	return string(buf), nil
}

func init() {
	reportGenerateCmd.Flags().String("scenario", "", "Scenario description for the report (required)")
	reportGenerateCmd.Flags().String("output", "", "Save report as Markdown to this file")

	reportLogsCmd.Flags().String("stage", "", "Filter by stage: planning|tool_call|tool_result|section_content|complete")
	reportLogsCmd.Flags().Int("limit", 0, "Max log entries to show (0 = all)")

	reportChatCmd.Flags().String("sim", "", "Simulation ID for additional context")
	reportChatCmd.Flags().String("session", "", "Resume a past report chat session by ID")

	interviewCmd.Flags().String("question", "", "Single question (non-interactive mode)")
	interviewCmd.Flags().Bool("all", false, "Interview all agents in the graph")
	interviewCmd.Flags().String("batch", "", "Comma-separated agent names to interview")
	interviewCmd.Flags().Bool("save", false, "Save this interaction to history")

	reportCmd.AddCommand(reportGenerateCmd)
	reportCmd.AddCommand(reportStatusCmd)
	reportCmd.AddCommand(reportSectionsCmd)
	reportCmd.AddCommand(reportLogsCmd)
	reportCmd.AddCommand(reportChatCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(interviewCmd)
}
