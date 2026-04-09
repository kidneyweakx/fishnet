package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
)

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query simulation results stored in the database",
}

// ─── query posts ────────────────────────────────────────────────────────────

var queryPostsCmd = &cobra.Command{
	Use:   "posts",
	Short: "List posts from a simulation",
	Example: `  fishnet query posts --sim <sim_id>
  fishnet query posts --sim <sim_id> --platform twitter --limit 20`,
	RunE: func(cmd *cobra.Command, args []string) error {
		simID, _ := cmd.Flags().GetString("sim")
		plt, _ := cmd.Flags().GetString("platform")
		limit, _ := cmd.Flags().GetInt("limit")
		if simID == "" {
			return fmt.Errorf("--sim is required")
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		posts, err := database.GetSimPosts(simID, plt, limit)
		if err != nil {
			return fmt.Errorf("get sim posts: %w", err)
		}
		if len(posts) == 0 {
			fmt.Println(yellow("No posts found for simulation: ") + simID)
			return nil
		}

		fmt.Printf("\n%s  Posts for simulation %s\n", bold("■"), cyan(simID))
		fmt.Printf("  %s\n\n", strings.Repeat("─", 72))
		fmt.Printf("  %-6s  %-10s  %-14s  %-30s  %s\n",
			bold("Round"), bold("Platform"), bold("Author"), bold("Content"), bold("Likes/Reposts"))
		fmt.Printf("  %s\n", strings.Repeat("─", 72))

		for _, p := range posts {
			snippet := p.Content
			if len([]rune(snippet)) > 30 {
				snippet = string([]rune(snippet)[:27]) + "..."
			}
			fmt.Printf("  %-6d  %-10s  %-14s  %-30s  %d / %d\n",
				p.Round, p.Platform, truncStr(p.AuthorName, 14), snippet, p.Likes, p.Reposts)
		}
		fmt.Println()
		return nil
	},
}

// ─── query actions ───────────────────────────────────────────────────────────

var queryActionsCmd = &cobra.Command{
	Use:   "actions",
	Short: "List actions from a simulation",
	Example: `  fishnet query actions --sim <sim_id>
  fishnet query actions --sim <sim_id> --agent alice --type CREATE_POST --limit 50`,
	RunE: func(cmd *cobra.Command, args []string) error {
		simID, _ := cmd.Flags().GetString("sim")
		agentID, _ := cmd.Flags().GetString("agent")
		actionType, _ := cmd.Flags().GetString("type")
		limit, _ := cmd.Flags().GetInt("limit")
		if simID == "" {
			return fmt.Errorf("--sim is required")
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		actions, err := database.GetSimActions(simID, agentID, actionType, limit)
		if err != nil {
			return fmt.Errorf("get sim actions: %w", err)
		}
		if len(actions) == 0 {
			fmt.Println(yellow("No actions found for simulation: ") + simID)
			return nil
		}

		fmt.Printf("\n%s  Actions for simulation %s\n", bold("■"), cyan(simID))
		fmt.Printf("  %s\n\n", strings.Repeat("─", 80))
		fmt.Printf("  %-6s  %-10s  %-14s  %-16s  %-24s  %s\n",
			bold("Round"), bold("Platform"), bold("Agent"), bold("Type"), bold("PostID"), bold("OK"))
		fmt.Printf("  %s\n", strings.Repeat("─", 80))

		for _, a := range actions {
			ok := green("✓")
			if !a.Success {
				ok = red("✗")
			}
			fmt.Printf("  %-6d  %-10s  %-14s  %-16s  %-24s  %s\n",
				a.Round, a.Platform, truncStr(a.AgentName, 14), a.ActionType,
				truncStr(a.PostID, 24), ok)
		}
		fmt.Println()
		return nil
	},
}

// ─── query timeline ──────────────────────────────────────────────────────────

var queryTimelineCmd = &cobra.Command{
	Use:   "timeline",
	Short: "Show chronological action timeline for a simulation",
	Example: `  fishnet query timeline --sim <sim_id>
  fishnet query timeline --sim <sim_id> --limit 30`,
	RunE: func(cmd *cobra.Command, args []string) error {
		simID, _ := cmd.Flags().GetString("sim")
		limit, _ := cmd.Flags().GetInt("limit")
		if simID == "" {
			return fmt.Errorf("--sim is required")
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		actions, err := database.GetSimTimeline(simID, limit)
		if err != nil {
			return fmt.Errorf("get sim timeline: %w", err)
		}
		if len(actions) == 0 {
			fmt.Println(yellow("No timeline data found for simulation: ") + simID)
			return nil
		}

		fmt.Printf("\n%s  Timeline for simulation %s\n", bold("■"), cyan(simID))
		fmt.Printf("  %s\n\n", strings.Repeat("─", 80))

		for _, a := range actions {
			statusMark := green("✓")
			if !a.Success {
				statusMark = red("✗")
			}
			snippet := ""
			if a.Content != "" {
				snippet = " — " + truncStr(a.Content, 40)
			}
			fmt.Printf("  [R%02d] %s  %s  %s%s%s\n",
				a.Round,
				cyan(fmt.Sprintf("%-8s", a.Platform)),
				bold(fmt.Sprintf("%-14s", truncStr(a.AgentName, 14))),
				yellow(fmt.Sprintf("%-14s", a.ActionType)),
				snippet,
				statusMark)
		}
		fmt.Println()
		return nil
	},
}

// ─── query stats ─────────────────────────────────────────────────────────────

var queryStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show per-agent statistics for a simulation",
	Example: `  fishnet query stats --sim <sim_id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		simID, _ := cmd.Flags().GetString("sim")
		if simID == "" {
			return fmt.Errorf("--sim is required")
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		stats, err := database.GetAgentStats(simID)
		if err != nil {
			return fmt.Errorf("get agent stats: %w", err)
		}
		if len(stats) == 0 {
			fmt.Println(yellow("No stats found for simulation: ") + simID)
			return nil
		}

		fmt.Printf("\n%s  Agent stats for simulation %s\n", bold("■"), cyan(simID))
		fmt.Printf("  %s\n\n", strings.Repeat("─", 70))
		fmt.Printf("  %-20s  %7s  %7s  %8s  %8s\n",
			bold("Agent"), bold("Posts"), bold("Likes"), bold("Reposts"), bold("Comments"))
		fmt.Printf("  %s\n", strings.Repeat("─", 70))

		for _, s := range stats {
			fmt.Printf("  %-20s  %7d  %7d  %8d  %8d\n",
				truncStr(s.AgentName, 20), s.TotalPosts, s.TotalLikes, s.TotalReposts, s.TotalComments)
		}
		fmt.Println()
		return nil
	},
}

// ─── query sims ──────────────────────────────────────────────────────────────

var querySimsCmd = &cobra.Command{
	Use:   "sims",
	Short: "List recent simulations for the current project",
	Example: `  fishnet query sims
  fishnet query sims --limit 10`,
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()

		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			return err
		}

		sims, err := database.GetSimsByProject(projectID, limit)
		if err != nil {
			return fmt.Errorf("get sims: %w", err)
		}
		if len(sims) == 0 {
			fmt.Println(yellow("No simulations found for project: ") + cfg.Project)
			return nil
		}

		fmt.Printf("\n%s  Simulations for project %s\n", bold("■"), cyan(cfg.Project))
		fmt.Printf("  %s\n\n", strings.Repeat("─", 88))
		fmt.Printf("  %-36s  %-40s  %-19s  %s\n",
			bold("ID"), bold("Scenario"), bold("Created At"), bold("Done"))
		fmt.Printf("  %s\n", strings.Repeat("─", 88))

		for _, s := range sims {
			doneStr := green("✓")
			if !s.Done {
				doneStr = yellow("…")
			}
			fmt.Printf("  %-36s  %-40s  %-19s  %s\n",
				s.ID, truncStr(s.Scenario, 40), truncStr(s.CreatedAt, 19), doneStr)
		}
		fmt.Println()
		return nil
	},
}

// ─── init ────────────────────────────────────────────────────────────────────

func init() {
	// query posts flags
	queryPostsCmd.Flags().String("sim", "", "Simulation ID (required)")
	queryPostsCmd.Flags().String("platform", "", "Filter by platform: twitter|reddit")
	queryPostsCmd.Flags().Int("limit", 20, "Max rows to return")

	// query actions flags
	queryActionsCmd.Flags().String("sim", "", "Simulation ID (required)")
	queryActionsCmd.Flags().String("agent", "", "Filter by agent ID or name")
	queryActionsCmd.Flags().String("type", "", "Filter by action type (e.g. CREATE_POST)")
	queryActionsCmd.Flags().Int("limit", 50, "Max rows to return")

	// query timeline flags
	queryTimelineCmd.Flags().String("sim", "", "Simulation ID (required)")
	queryTimelineCmd.Flags().Int("limit", 30, "Max rows to return")

	// query stats flags
	queryStatsCmd.Flags().String("sim", "", "Simulation ID (required)")

	// query sims flags
	querySimsCmd.Flags().Int("limit", 10, "Max simulations to list")

	queryCmd.AddCommand(queryPostsCmd)
	queryCmd.AddCommand(queryActionsCmd)
	queryCmd.AddCommand(queryTimelineCmd)
	queryCmd.AddCommand(queryStatsCmd)
	queryCmd.AddCommand(querySimsCmd)

	rootCmd.AddCommand(queryCmd)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// truncStr truncates s to at most n runes (used for table formatting).
func truncStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
