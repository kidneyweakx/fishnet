package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
	"fishnet/internal/interaction"
	"fishnet/internal/llm"
	"fishnet/internal/report"
)

// surveyCmd broadcasts a question to multiple agents and saves the results.
var surveyCmd = &cobra.Command{
	Use:   "survey",
	Short: "Broadcast a question to multiple agents and collect structured responses",
	Long: `Survey runs a question across all (or selected) graph agents concurrently,
displays a formatted response table, and saves the full result as a reusable
interaction session.`,
	Example: `  fishnet survey --question "How do you feel about the AI regulation proposal?"
  fishnet survey --question "What is your main concern?" --agents "Alice,Bob,Corp X"
  fishnet survey --question "Rate the proposal 1-10" --all --limit 20`,
	RunE: func(cmd *cobra.Command, args []string) error {
		question, _ := cmd.Flags().GetString("question")
		agentsFlag, _ := cmd.Flags().GetString("agents")
		allFlag, _ := cmd.Flags().GetBool("all")
		limit, _ := cmd.Flags().GetInt("limit")

		if question == "" {
			return fmt.Errorf("--question is required")
		}
		if !allFlag && agentsFlag == "" {
			allFlag = true // default: survey all agents
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
		interviewAgent := report.New(database, client)
		ctx := context.Background()

		// Resolve agent list
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
			for _, name := range strings.Split(agentsFlag, ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if n, ok := nodeByName[strings.ToLower(name)]; ok {
					nodes = append(nodes, n)
				} else {
					fmt.Printf("%s Agent %q not found, skipping\n", yellow("!"), name)
				}
			}
		}

		if len(nodes) == 0 {
			return fmt.Errorf("no agents found to survey")
		}
		if limit > 0 && len(nodes) > limit {
			nodes = nodes[:limit]
		}

		fmt.Printf("\n%s Survey: %d agents\n  %s\n\n",
			cyan("→"), len(nodes), bold("Q: "+question))

		sem := make(chan struct{}, 6)
		type result struct {
			node db.Node
			resp string
			err  error
		}
		results := make([]result, len(nodes))
		var wg sync.WaitGroup
		var mu sync.Mutex
		done := 0

		for i, n := range nodes {
			wg.Add(1)
			go func(idx int, node db.Node) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				resp, _, err := interviewAgent.Interview(ctx, projectID, node.Name, question, nil)
				results[idx] = result{node: node, resp: resp, err: err}

				mu.Lock()
				done++
				fmt.Printf("\r  %s %d/%d", progressBar(done*100/len(nodes), 20), done, len(nodes))
				mu.Unlock()
			}(i, n)
		}
		wg.Wait()
		fmt.Println()

		// Build answers
		var answers []interaction.SurveyAnswer
		fmt.Printf("\n%s Results:\n\n", bold("→"))
		for _, r := range results {
			if r.err != nil {
				fmt.Printf("  %s [%s]: error: %v\n\n", red("✗"), r.node.Name, r.err)
				continue
			}
			answers = append(answers, interaction.SurveyAnswer{
				AgentName: r.node.Name,
				AgentType: r.node.Type,
				Response:  r.resp,
			})
			name := r.node.Name
			if len(name) > 22 {
				name = name[:21] + "…"
			}
			snippet := r.resp
			if len([]rune(snippet)) > 80 {
				snippet = string([]rune(snippet)[:77]) + "…"
			}
			fmt.Printf("  %s (%s)\n  %s\n\n",
				cyan(name), r.node.Type, snippet)
		}

		// Save as interaction session
		intMgr := interaction.NewManager(".")
		sess, err := intMgr.Create(interaction.TypeSurvey, "", question)
		if err == nil {
			sess.Answers = answers
			sess.History = []llm.Message{
				{Role: "user", Content: question},
			}
			if saveErr := intMgr.Save(sess); saveErr == nil {
				fmt.Printf("%s Survey saved: %s  (%d responses)\n",
					green("✓"), sess.ID, len(answers))
				fmt.Printf("  View: fishnet history show %s\n", sess.ID)
			}
		}
		return nil
	},
}

func init() {
	surveyCmd.Flags().String("question", "", "Question to ask all agents (required)")
	surveyCmd.Flags().String("agents", "", "Comma-separated agent names (default: all)")
	surveyCmd.Flags().Bool("all", false, "Survey all agents in the graph")
	surveyCmd.Flags().Int("limit", 0, "Max number of agents to survey (0 = no limit)")
	rootCmd.AddCommand(surveyCmd)
}
