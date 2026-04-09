package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
	"fishnet/internal/graph"
	"fishnet/internal/llm"
	"fishnet/internal/viz"
)

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Graph operations (stats, web viz, community detection)",
}

var graphStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show graph statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		stats := database.GetStats(projectID)
		fmt.Printf("\n%s %s\n", bold("Project:"), cfg.Project)
		fmt.Printf("  Nodes:       %d\n", stats.Nodes)
		fmt.Printf("  Edges:       %d\n", stats.Edges)
		fmt.Printf("  Documents:   %d\n", stats.Documents)
		fmt.Printf("  Chunks:      %d\n", stats.Chunks)
		fmt.Printf("  Communities: %d\n", stats.Communities)
		return nil
	},
}

var graphShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print ASCII graph summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}
		return viz.PrintASCII(database, projectID)
	},
}

var graphWebCmd = &cobra.Command{
	Use:   "web",
	Short: "Open interactive graph visualization in browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		url, err := viz.Serve(database, projectID)
		if err != nil {
			return err
		}

		fmt.Printf("%s Graph server running at %s\n", green("✓"), cyan(url))
		fmt.Printf("  Press Ctrl+C to stop\n\n")

		// Open browser
		openBrowser(url)

		// Block forever
		select {}
	},
}

var graphCommunityCmd = &cobra.Command{
	Use:   "community",
	Short: "Run Louvain community detection",
	RunE: func(cmd *cobra.Command, args []string) error {
		summaries, _ := cmd.Flags().GetBool("summarize")
		minSize, _ := cmd.Flags().GetInt("min-size")

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		var client *llm.Client
		if summaries {
			client = llm.New(cfg.LLM)
			fmt.Printf("%s Running community detection with LLM summaries...\n", cyan("→"))
		} else {
			fmt.Printf("%s Running community detection...\n", cyan("→"))
		}

		if minSize <= 0 {
			minSize = cfg.Graph.CommunityMinSize
		}

		results, err := graph.RunCommunityDetection(
			context.Background(), database, client, projectID, minSize)
		if err != nil {
			return err
		}

		fmt.Printf("\n%s Found %d communities\n\n", green("✓"), len(results))
		for _, c := range results {
			fmt.Printf("%s Community %d%s (%d nodes)\n",
				cyan("■"), c.ID, "", len(c.Nodes))
			if c.Summary != "" {
				fmt.Printf("  %s\n", c.Summary)
			}
			for i, n := range c.Nodes {
				if i >= 5 {
					fmt.Printf("  ... +%d more\n", len(c.Nodes)-i)
					break
				}
				fmt.Printf("  • %s (%s)\n", n.Name, n.Type)
			}
			fmt.Println()
		}
		return nil
	},
}

// ─── Search subcommand ────────────────────────────────────────────────────────

var graphSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search the knowledge graph",
}

var graphSearchQuickCmd = &cobra.Command{
	Use:   "quick",
	Short: "Keyword search on node names and summaries",
	RunE: func(cmd *cobra.Command, args []string) error {
		query, _ := cmd.Flags().GetString("query")
		limit, _ := cmd.Flags().GetInt("limit")
		if query == "" {
			return fmt.Errorf("--query is required")
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		result := graph.QuickSearch(database, projectID, query, limit)
		printSearchResult(result)
		return nil
	},
}

var graphSearchPanoramaCmd = &cobra.Command{
	Use:   "panorama",
	Short: "Broad search — returns all matching nodes plus their edges",
	RunE: func(cmd *cobra.Command, args []string) error {
		query, _ := cmd.Flags().GetString("query")
		limit, _ := cmd.Flags().GetInt("limit")
		if query == "" {
			return fmt.Errorf("--query is required")
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		result := graph.PanoramaSearch(database, projectID, query, limit)
		printSearchResult(result)
		return nil
	},
}

var graphSearchInsightCmd = &cobra.Command{
	Use:   "insight",
	Short: "LLM decomposes query into sub-questions, runs QuickSearch for each",
	RunE: func(cmd *cobra.Command, args []string) error {
		query, _ := cmd.Flags().GetString("query")
		if query == "" {
			return fmt.Errorf("--query is required")
		}

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
		result, err := graph.InsightForge(context.Background(), database, client, projectID, query)
		if err != nil {
			return err
		}
		printSearchResult(result)
		return nil
	},
}

// printSearchResult formats and prints a SearchResult to stdout.
func printSearchResult(r graph.SearchResult) {
	fmt.Printf("\n%s Search: %q\n", bold("→"), r.Query)
	fmt.Printf("  %d nodes, %d edges, %d facts\n\n", len(r.Nodes), len(r.Edges), len(r.Facts))

	if len(r.Nodes) > 0 {
		fmt.Printf("%s\n", bold("Nodes:"))
		for _, n := range r.Nodes {
			fmt.Printf("  • [%s] %s", n.Type, n.Name)
			if n.Summary != "" {
				fmt.Printf(" — %s", n.Summary)
			}
			fmt.Println()
		}
	}

	if len(r.Facts) > 0 {
		fmt.Printf("\n%s\n", bold("Key Facts:"))
		for _, f := range r.Facts {
			fmt.Printf("  %s\n", f)
		}
	}
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "linux":
		cmd, args = "xdg-open", []string{url}
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", url}
	default:
		return
	}
	exec.Command(cmd, args...).Start()
}

func init() {
	graphCommunityCmd.Flags().Bool("summarize", false, "Generate LLM summaries for each community")
	graphCommunityCmd.Flags().Int("min-size", 0, "Minimum community size (default: from config)")

	graphSearchQuickCmd.Flags().String("query", "", "Search query")
	graphSearchQuickCmd.Flags().Int("limit", 20, "Maximum number of nodes to return")

	graphSearchPanoramaCmd.Flags().String("query", "", "Search query")
	graphSearchPanoramaCmd.Flags().Int("limit", 50, "Maximum number of nodes to return")

	graphSearchInsightCmd.Flags().String("query", "", "Search query")

	graphSearchCmd.AddCommand(graphSearchQuickCmd, graphSearchPanoramaCmd, graphSearchInsightCmd)
	graphCmd.AddCommand(graphStatsCmd, graphShowCmd, graphWebCmd, graphCommunityCmd, graphSearchCmd)
	rootCmd.AddCommand(graphCmd)
}
