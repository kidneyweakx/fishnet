package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
	"fishnet/internal/llm"
	"fishnet/internal/report"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Report generation commands",
}

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

		fmt.Printf("\n%s Generating report for: %s\n\n", cyan("→"), bold(scenario))

		ctx := context.Background()
		r, err := agent.Generate(ctx, projectID, scenario, func(s report.Section) {
			fmt.Printf("  %s §%d %s\n", green("✓"), s.Index, s.Title)
		})
		if err != nil {
			return err
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
				// Resolve names from DB
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

			// Semaphore for max 4 concurrent interviews
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
						fmt.Printf("%s %s\n\n", cyan("["+node.Name+"]:")+" ", resp)
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
			return nil
		}

		// Interactive REPL mode
		fmt.Printf("\n%s Interviewing %s  (type 'exit' to quit)\n\n",
			cyan("→"), bold(agentRef))

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

	interviewCmd.Flags().String("question", "", "Single question (non-interactive mode)")
	interviewCmd.Flags().Bool("all", false, "Interview all agents in the graph")
	interviewCmd.Flags().String("batch", "", "Comma-separated agent names to interview")

	reportCmd.AddCommand(reportGenerateCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(interviewCmd)
}
