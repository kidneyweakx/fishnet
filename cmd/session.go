package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
	"fishnet/internal/llm"
	"fishnet/internal/session"
	"fishnet/internal/sim"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage saved simulation sessions",
}

// ─── session list ─────────────────────────────────────────────────────────────

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := session.NewManager(".")
		sessions, err := mgr.List()
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Println("No sessions saved. Use: fishnet session save --scenario \"...\"")
			return nil
		}
		fmt.Printf("%-24s %-16s %-32s %-7s %-12s %s\n",
			"ID", "NAME", "SCENARIO", "ROUNDS", "PLATFORMS", "DATE")
		fmt.Println(strings.Repeat("-", 100))
		for _, s := range sessions {
			platLabel := "tw+rd"
			if len(s.Platforms) == 1 {
				switch s.Platforms[0] {
				case "twitter":
					platLabel = "tw"
				case "reddit":
					platLabel = "rd"
				default:
					platLabel = s.Platforms[0]
				}
			} else if len(s.Platforms) > 0 {
				platLabel = strings.Join(s.Platforms, "+")
			}
			scenario := s.Scenario
			if len(scenario) > 30 {
				scenario = scenario[:29] + "…"
			}
			name := s.Name
			if name == "" {
				name = "-"
			}
			date := s.UpdatedAt.Format("2006-01-02")
			fmt.Printf("%-24s %-16s %-32s %-7d %-12s %s\n",
				s.ID, name, scenario, s.Rounds, platLabel, date)
		}
		return nil
	},
}

// ─── session show ─────────────────────────────────────────────────────────────

var sessionShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show session details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := session.NewManager(".")
		s, err := mgr.Load(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("\n%s Session: %s\n", cyan("→"), bold(s.ID))
		if s.Name != "" {
			fmt.Printf("  Name:      %s\n", s.Name)
		}
		fmt.Printf("  Scenario:  %s\n", s.Scenario)
		fmt.Printf("  Rounds:    %d\n", s.Rounds)
		if len(s.Platforms) > 0 {
			fmt.Printf("  Platforms: %s\n", strings.Join(s.Platforms, ", "))
		} else {
			fmt.Printf("  Platforms: twitter + reddit\n")
		}
		if s.MaxAgents > 0 {
			fmt.Printf("  MaxAgents: %d\n", s.MaxAgents)
		}
		if s.TimeZone != "" {
			fmt.Printf("  TimeZone:  %s\n", s.TimeZone)
		}
		if s.SimID != "" {
			fmt.Printf("  SimID:     %s\n", s.SimID)
		}
		if len(s.Tags) > 0 {
			fmt.Printf("  Tags:      %s\n", strings.Join(s.Tags, ", "))
		}
		if s.Notes != "" {
			fmt.Printf("  Notes:     %s\n", s.Notes)
		}
		fmt.Printf("  Created:   %s\n", s.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("  Updated:   %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05"))
		fmt.Println()
		return nil
	},
}

// ─── session save ─────────────────────────────────────────────────────────────

var sessionSaveCmd = &cobra.Command{
	Use:   "save",
	Short: "Save a simulation configuration as a session",
	RunE: func(cmd *cobra.Command, args []string) error {
		scenario, _ := cmd.Flags().GetString("scenario")
		name, _ := cmd.Flags().GetString("name")
		rounds, _ := cmd.Flags().GetInt("rounds")
		platformsStr, _ := cmd.Flags().GetString("platforms")
		maxAgents, _ := cmd.Flags().GetInt("agents")
		notes, _ := cmd.Flags().GetString("notes")
		tagsStr, _ := cmd.Flags().GetString("tags")

		if scenario == "" {
			return fmt.Errorf("--scenario is required")
		}

		var platforms []string
		if platformsStr != "" {
			for _, p := range strings.Split(platformsStr, ",") {
				p = strings.TrimSpace(strings.ToLower(p))
				if p != "" {
					platforms = append(platforms, p)
				}
			}
		}

		var tags []string
		if tagsStr != "" {
			for _, t := range strings.Split(tagsStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
		}

		s := &session.Session{
			ID:        session.NewID(),
			Name:      name,
			Scenario:  scenario,
			Platforms: platforms,
			Rounds:    rounds,
			MaxAgents: maxAgents,
			Notes:     notes,
			Tags:      tags,
		}

		mgr := session.NewManager(".")
		if err := mgr.Save(s); err != nil {
			return err
		}

		fmt.Printf("%s Session saved: %s\n", green("✓"), bold(s.ID))
		if name != "" {
			fmt.Printf("  Name: %s\n", name)
		}
		fmt.Printf("  Scenario: %s\n", scenario)
		fmt.Printf("  Rounds: %d  Platforms: %s\n", rounds, func() string {
			if len(platforms) == 0 {
				return "twitter + reddit"
			}
			return strings.Join(platforms, ", ")
		}())
		return nil
	},
}

// ─── session run ──────────────────────────────────────────────────────────────

var sessionRunCmd = &cobra.Command{
	Use:   "run <id>",
	Short: "Run a saved session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		roundsOverride, _ := cmd.Flags().GetInt("rounds")
		scenarioOverride, _ := cmd.Flags().GetString("scenario")
		concurrency, _ := cmd.Flags().GetInt("concurrency")

		mgr := session.NewManager(".")
		s, err := mgr.Load(args[0])
		if err != nil {
			return err
		}

		// Apply overrides
		if roundsOverride > 0 {
			s.Rounds = roundsOverride
		}
		if scenarioOverride != "" {
			s.Scenario = scenarioOverride
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
		ps := sim.NewPlatformSim(database, client)

		platLabel := "twitter + reddit"
		if len(s.Platforms) == 1 {
			platLabel = s.Platforms[0]
		} else if len(s.Platforms) > 0 {
			platLabel = strings.Join(s.Platforms, " + ")
		}

		fmt.Printf("\n%s Running session: %s\n  Scenario:  %s\n  Rounds:    %d\n  Platforms: %s\n\n",
			cyan("→"), bold(s.ID), bold(s.Scenario), s.Rounds, platLabel)

		progressCh := make(chan sim.RoundProgress, 64)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- ps.Run(ctx, projectID, sim.RoundConfig{
				Scenario:    s.Scenario,
				MaxRounds:   s.Rounds,
				MaxAgents:   s.MaxAgents,
				Platforms:   s.Platforms,
				Concurrency: concurrency,
			}, progressCh)
		}()

		lastRound := 0
		for prog := range progressCh {
			if prog.Done {
				break
			}
			if prog.Round != lastRound {
				lastRound = prog.Round
				pct := prog.Round * 100 / prog.MaxRounds
				bar := progressBar(pct, 20)
				fmt.Printf("\r  Round %2d/%d [%s] tw:%d posts rd:%d posts",
					prog.Round, prog.MaxRounds, bar, prog.TwitterStat.Posts, prog.RedditStat.Posts)
			}
		}

		if err := <-done; err != nil {
			return err
		}

		fmt.Printf("\n\n%s Simulation complete\n", green("✓"))

		// Save sim_id back to session
		simID, err2 := database.CreateSim(projectID, s.Scenario)
		if err2 == nil {
			database.FinishSim(simID, "")
			s.SimID = simID
			mgr.Save(s) // best-effort
		}

		return nil
	},
}

// ─── session fork ─────────────────────────────────────────────────────────────

var sessionForkCmd = &cobra.Command{
	Use:   "fork <id>",
	Short: "Copy a session with a new ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		mgr := session.NewManager(".")
		forked, err := mgr.Fork(args[0], name)
		if err != nil {
			return err
		}
		fmt.Printf("%s Forked to: %s\n", green("✓"), bold(forked.ID))
		if name != "" {
			fmt.Printf("  Name: %s\n", name)
		}
		return nil
	},
}

// ─── session modify ───────────────────────────────────────────────────────────

var sessionModifyCmd = &cobra.Command{
	Use:   "modify <id>",
	Short: "Modify session fields",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fields := map[string]string{}
		if v, _ := cmd.Flags().GetString("scenario"); v != "" {
			fields["scenario"] = v
		}
		if v, _ := cmd.Flags().GetInt("rounds"); v > 0 {
			fields["rounds"] = fmt.Sprintf("%d", v)
		}
		if v, _ := cmd.Flags().GetString("platforms"); v != "" {
			fields["platforms"] = v
		}
		if v, _ := cmd.Flags().GetString("timezone"); v != "" {
			fields["timezone"] = v
		}
		if v, _ := cmd.Flags().GetString("notes"); v != "" {
			fields["notes"] = v
		}
		if v, _ := cmd.Flags().GetString("tags"); v != "" {
			fields["tags"] = v
		}
		if v, _ := cmd.Flags().GetString("name"); v != "" {
			fields["name"] = v
		}
		if len(fields) == 0 {
			return fmt.Errorf("no fields to update; use --scenario, --rounds, --platforms, --name, --notes, --tags, or --timezone")
		}
		mgr := session.NewManager(".")
		s, err := mgr.Patch(args[0], fields)
		if err != nil {
			return err
		}
		fmt.Printf("%s Session updated: %s\n", green("✓"), bold(s.ID))
		return nil
	},
}

// ─── session delete ───────────────────────────────────────────────────────────

var sessionDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := session.NewManager(".")
		if err := mgr.Delete(args[0]); err != nil {
			return err
		}
		fmt.Printf("%s Session deleted: %s\n", green("✓"), args[0])
		return nil
	},
}

func init() {
	// session save flags
	sessionSaveCmd.Flags().String("scenario", "", "Simulation scenario (required)")
	sessionSaveCmd.Flags().String("name", "", "Human-readable name for this session")
	sessionSaveCmd.Flags().Int("rounds", 10, "Number of simulation rounds")
	sessionSaveCmd.Flags().String("platforms", "", "Platforms: twitter,reddit (default: both)")
	sessionSaveCmd.Flags().Int("agents", 0, "Max agents (0 = all nodes)")
	sessionSaveCmd.Flags().String("notes", "", "Notes for this session")
	sessionSaveCmd.Flags().String("tags", "", "Comma-separated tags")

	// session run flags
	sessionRunCmd.Flags().Int("rounds", 0, "Override rounds from session")
	sessionRunCmd.Flags().String("scenario", "", "Override scenario from session")
	sessionRunCmd.Flags().Int("concurrency", 6, "Max concurrent LLM calls")

	// session fork flags
	sessionForkCmd.Flags().String("name", "", "Name for the forked session")

	// session modify flags
	sessionModifyCmd.Flags().String("scenario", "", "New scenario")
	sessionModifyCmd.Flags().Int("rounds", 0, "New round count")
	sessionModifyCmd.Flags().String("platforms", "", "New platforms (comma-separated)")
	sessionModifyCmd.Flags().String("timezone", "", "New timezone")
	sessionModifyCmd.Flags().String("notes", "", "New notes")
	sessionModifyCmd.Flags().String("tags", "", "New tags (comma-separated)")
	sessionModifyCmd.Flags().String("name", "", "New name")

	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionShowCmd)
	sessionCmd.AddCommand(sessionSaveCmd)
	sessionCmd.AddCommand(sessionRunCmd)
	sessionCmd.AddCommand(sessionForkCmd)
	sessionCmd.AddCommand(sessionModifyCmd)
	sessionCmd.AddCommand(sessionDeleteCmd)

	rootCmd.AddCommand(sessionCmd)
}
