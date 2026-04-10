package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
	"fishnet/internal/llm"
	"fishnet/internal/platform"
	"fishnet/internal/session"
	"fishnet/internal/sim"
	"fishnet/internal/simrun"
)

const pidFile = ".fishnet/sim.pid"

// simGraphMemory is set by --graph-memory on simPlatformCmd.
var simGraphMemory bool

func writePID() {
	_ = os.MkdirAll(".fishnet", 0755)
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func removePID() {
	_ = os.Remove(pidFile)
}

func readPID() (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID in %s", pidFile)
	}
	return pid, nil
}

var simCmd = &cobra.Command{
	Use:   "sim",
	Short: "Simulation commands",
}

// ─── sim run (copywriting / persona reaction) ────────────────────────────────

var simRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a copywriting simulation with graph personas",
	Long:  `Simulate how entities in the knowledge graph react to a scenario. Good for testing copy angles.`,
	Example: `  fishnet sim run --scenario "We're launching an AI assistant for small businesses"
  fishnet sim run --scenario "New policy restricting social media for teens" --agents 20`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scenario, _ := cmd.Flags().GetString("scenario")
		maxAgents, _ := cmd.Flags().GetInt("agents")
		output, _ := cmd.Flags().GetString("output")
		quiet, _ := cmd.Flags().GetBool("quiet")

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
		if maxAgents <= 0 {
			maxAgents = cfg.Sim.MaxAgents
		}

		client := llm.New(cfg.LLM)
		engine := sim.New(database, client)

		fmt.Printf("\n%s Running copy simulation…\n  Scenario: %s\n  Max agents: %d\n\n",
			cyan("→"), bold(scenario), maxAgents)

		ctx := context.Background()
		result, err := engine.RunCopySimulation(ctx, projectID, scenario, maxAgents,
			func(resp sim.AgentResponse) {
				if quiet {
					return
				}
				emoji := "~"
				if resp.Sentiment == "positive" {
					emoji = "+"
				} else if resp.Sentiment == "negative" {
					emoji = "-"
				}
				fmt.Printf("  [%s %d] %s: %s\n", emoji, resp.Score, cyan(resp.AgentName), resp.Response)
			})
		if err != nil {
			return err
		}

		fmt.Println(sim.FormatResult(result))

		jsonResult, _ := sim.SaveResult(result)
		simID, _ := database.CreateSim(projectID, scenario)
		database.FinishSim(simID, jsonResult)

		if output != "" {
			if err := os.WriteFile(output, []byte(jsonResult), 0644); err != nil {
				fmt.Printf("%s Could not write output: %v\n", yellow("!"), err)
			} else {
				fmt.Printf("%s Result saved to %s\n", green("✓"), output)
			}
		}
		return nil
	},
}

// ─── sim platform (Twitter + Reddit OASIS-style social simulation) ───────────

var simPlatformCmd = &cobra.Command{
	Use:   "platform",
	Short: "Run a multi-round Twitter/Reddit social simulation (OASIS-style)",
	Long: `Simulate agent behavior across Twitter and Reddit over multiple rounds.
Each agent makes LLM-free decisions each round; LLM is only used to generate post content.
~15-20x more efficient than OASIS.`,
	Example: `  fishnet sim platform --scenario "AI regulation debate" --rounds 10
  fishnet sim platform --scenario "Product launch" --rounds 5 --platforms twitter
  fishnet sim platform --scenario "Climate policy" --agents 30 --output ./sim-out`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scenario, _ := cmd.Flags().GetString("scenario")
		rounds, _ := cmd.Flags().GetInt("rounds")
		maxAgents, _ := cmd.Flags().GetInt("agents")
		platformsStr, _ := cmd.Flags().GetString("platforms")
		output, _ := cmd.Flags().GetString("output")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		quiet, _ := cmd.Flags().GetBool("quiet")
		graphMemory, _ := cmd.Flags().GetBool("graph-memory")
		noLLM, _ := cmd.Flags().GetBool("no-llm")
		simMode, _ := cmd.Flags().GetString("mode")
		sessionRef, _ := cmd.Flags().GetString("session")

		// ── Load from prepared session if --session is given ─────────────────
		var prebuiltPersonalities []*platform.Personality
		if sessionRef != "" {
			sessMgr := session.NewManager(".")
			sess, err := sessMgr.Load(sessionRef)
			if err != nil {
				return fmt.Errorf("load session: %w", err)
			}
			// Override flags from session if not explicitly set
			if scenario == "" {
				scenario = sess.Scenario
			}
			if rounds == 10 { // default — use session value if set
				rounds = sess.Rounds
			}
			if maxAgents == 0 {
				maxAgents = sess.MaxAgents
			}
			if platformsStr == "" && len(sess.Platforms) > 0 {
				platformsStr = strings.Join(sess.Platforms, ",")
			}

			// Load pre-built personalities if prepared
			store := sim.NewPersonaStore(".")
			if store.Exists(sess.ID) {
				pp, err := store.Load(sess.ID)
				if err != nil {
					fmt.Printf("%s Could not load prepared personas: %v — rebuilding\n", yellow("!"), err)
				} else {
					prebuiltPersonalities = pp.Data
					fmt.Printf("%s Loaded %d pre-built agent profiles from session %s\n",
						green("✓"), len(prebuiltPersonalities), sess.ID)
				}
			} else {
				fmt.Printf("%s Session not prepared — run: fishnet sim prepare %s\n", yellow("!"), sess.ID)
			}
		}

		if scenario == "" {
			return fmt.Errorf("--scenario is required (or use --session with a created session)")
		}

		// Write PID file so `sim stop` can find us.
		writePID()
		defer removePID()

		resolveAPIKey()

		var platforms []string
		if platformsStr != "" {
			for _, p := range strings.Split(platformsStr, ",") {
				p = strings.TrimSpace(strings.ToLower(p))
				if p == "twitter" || p == "reddit" {
					platforms = append(platforms, p)
				}
			}
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
		ps := sim.NewPlatformSim(database, client)

		platLabel := "twitter + reddit"
		if len(platforms) == 1 {
			platLabel = platforms[0]
		}
		preparedNote := ""
		if len(prebuiltPersonalities) > 0 {
			preparedNote = fmt.Sprintf(" [%d pre-built agents]", len(prebuiltPersonalities))
		}
		// Resolve mode: --mode flag > --no-llm > default batch
		resolvedMode := simMode
		if resolvedMode == "" && noLLM {
			resolvedMode = sim.ModeNoLLM
		}
		modeLabel := resolvedMode
		if modeLabel == "" {
			modeLabel = sim.ModeBatch
		}

		fmt.Printf("\n%s Platform simulation\n  Scenario:  %s\n  Rounds:    %d\n  Platforms: %s\n  Mode:      %s%s\n\n",
			cyan("→"), bold(scenario), rounds, platLabel, modeLabel, preparedNote)

		// ── Create SimRun record ─────────────────────────────────────────────
		runMgr := simrun.NewManager(".")
		run, _ := runMgr.Create(projectID, sessionRef, scenario, rounds, platforms)
		if run != nil {
			run.MarkRunning(os.Getpid())
			_ = runMgr.Save(run)
			fmt.Printf("%s Run ID: %s\n\n", cyan("→"), run.ID)
		}

		progressCh := make(chan sim.RoundProgress, 64)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Run simulation in goroutine
		done := make(chan error, 1)
		go func() {
			done <- ps.Run(ctx, projectID, sim.RoundConfig{
				Scenario:          scenario,
				MaxRounds:         rounds,
				MaxAgents:         maxAgents,
				Platforms:         platforms,
				OutputDir:         output,
				Concurrency:       concurrency,
				EnableGraphMemory: graphMemory,
				Personalities:     prebuiltPersonalities,
				Mode:              resolvedMode,
			NoLLM:             noLLM,
			}, progressCh)
		}()

		lastRound := 0
		saveTick := 0

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

				// Persist SimRun progress every 2 rounds
				if run != nil {
					saveTick++
					if saveTick%2 == 0 {
						run.Rounds = prog.Round
						run.Twitter = simrun.PlatformStats{Posts: prog.TwitterStat.Posts}
						run.Reddit = simrun.PlatformStats{Posts: prog.RedditStat.Posts}
						_ = runMgr.Save(run)
					}
				}
			}
			_ = quiet
		}

		simErr := <-done
		if run != nil {
			run.Rounds = rounds
			run.Twitter = simrun.PlatformStats{}
			run.Reddit = simrun.PlatformStats{}
			if simErr != nil {
				run.MarkFailed(simErr.Error())
			} else {
				run.MarkCompleted()
			}
			_ = runMgr.Save(run)
		}
		if simErr != nil {
			return simErr
		}

		fmt.Printf("\n\n%s Simulation complete\n", green("✓"))
		if run != nil {
			fmt.Printf("  Run ID: %s\n", run.ID)
			fmt.Printf("  Status: fishnet sim run-status %s\n", run.ID)
		}
		if output != "" {
			fmt.Printf("  Actions log: %s/actions.jsonl\n", output)
		}
		return nil
	},
}


// ─── sim stop ────────────────────────────────────────────────────────────────

var simStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a running platform simulation",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := readPID()
		if err != nil {
			return fmt.Errorf("no running simulation found (could not read %s)", pidFile)
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			removePID()
			return fmt.Errorf("process %d not found: %w", pid, err)
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			removePID()
			return fmt.Errorf("failed to send SIGTERM to PID %d: %w", pid, err)
		}
		removePID()
		fmt.Printf("%s Sent SIGTERM to simulation process (PID %d)\n", green("✓"), pid)
		return nil
	},
}

// ─── sim status ───────────────────────────────────────────────────────────────

var simStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether a simulation is currently running",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := readPID()
		if err != nil {
			fmt.Printf("%s No simulation is currently running\n", yellow("○"))
			return nil
		}
		// Check if process actually exists
		proc, err := os.FindProcess(pid)
		if err != nil {
			fmt.Printf("%s PID file found (%d) but process not found\n", yellow("!"), pid)
			return nil
		}
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			fmt.Printf("%s PID file found (%d) but process is not running\n", yellow("!"), pid)
			return nil
		}
		fmt.Printf("%s Simulation running (PID %d)\n", green("●"), pid)
		return nil
	},
}

// ─── sim list ─────────────────────────────────────────────────────────────────

var simListCmd = &cobra.Command{
	Use:   "list",
	Short: "List past simulations",
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

		rows, err := database.Query(
			`SELECT id, scenario, status, created_at FROM simulations WHERE project_id = ? ORDER BY created_at DESC LIMIT 50`,
			projectID)
		if err != nil {
			return err
		}
		defer rows.Close()

		fmt.Printf("%-38s %-40s %-10s %s\n", "ID", "SCENARIO", "STATUS", "CREATED")
		fmt.Println(strings.Repeat("-", 100))
		any := false
		for rows.Next() {
			any = true
			var id, scenario, status, createdAt string
			rows.Scan(&id, &scenario, &status, &createdAt)
			if len(scenario) > 38 {
				scenario = scenario[:37] + "…"
			}
			fmt.Printf("%-38s %-40s %-10s %s\n", id, scenario, status, createdAt)
		}
		if !any {
			fmt.Println("No past simulations found. Run: fishnet sim platform --scenario \"...\"")
		}
		return rows.Err()
	},
}

// ─── sim oasis ────────────────────────────────────────────────────────────────

var simOasisCmd = &cobra.Command{
	Use:   "oasis",
	Short: "Run an OASIS simulation (Python subprocess wrapper)",
	Long: `Launch OASIS (Python) with a generated config from the knowledge graph.
Requires 'oasis' in PATH or OASIS_PATH env var.
For a 15-20x faster built-in alternative, use: fishnet sim platform`,
	Example: `  fishnet sim oasis --scenario "AI regulation debate" --rounds 10
  fishnet sim oasis --scenario "Product launch" --config ./oasis_config.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scenario, _ := cmd.Flags().GetString("scenario")
		rounds, _ := cmd.Flags().GetInt("rounds")
		configPath, _ := cmd.Flags().GetString("config")

		if scenario == "" {
			return fmt.Errorf("--scenario is required")
		}

		// Locate oasis binary
		oasisBin := os.Getenv("OASIS_PATH")
		if oasisBin == "" {
			var err error
			oasisBin, err = exec.LookPath("oasis")
			if err != nil {
				fmt.Println(red("✗") + " OASIS not found. Use `fishnet sim platform` for the built-in simulation (15-20x faster, no Python required).")
				return fmt.Errorf("oasis binary not found in PATH and OASIS_PATH is not set")
			}
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

		// Build minimal OASIS config JSON from DB nodes
		nodes, err := database.GetNodes(projectID)
		if err != nil {
			return err
		}

		type oasisAgent struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
			Bio  string `json:"bio"`
		}
		type oasisConfig struct {
			Scenario  string       `json:"scenario"`
			MaxRounds int          `json:"max_rounds"`
			Agents    []oasisAgent `json:"agents"`
		}

		oc := oasisConfig{
			Scenario:  scenario,
			MaxRounds: rounds,
		}
		for _, n := range nodes {
			oc.Agents = append(oc.Agents, oasisAgent{
				ID:   n.ID,
				Name: n.Name,
				Type: n.Type,
				Bio:  n.Summary,
			})
		}

		// Write config file if not provided
		if configPath == "" {
			_ = os.MkdirAll(".fishnet", 0755)
			configPath = ".fishnet/oasis_config.json"
			data, err := json.MarshalIndent(oc, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(configPath, data, 0644); err != nil {
				return err
			}
			fmt.Printf("%s Generated OASIS config: %s  (%d agents)\n", cyan("→"), configPath, len(oc.Agents))
		}

		fmt.Printf("%s Running OASIS: %s\n  Scenario: %s\n  Rounds: %d\n\n",
			cyan("→"), oasisBin, scenario, rounds)

		oasisCmd := exec.Command(oasisBin, "run", "--config", configPath, "--scenario", scenario)
		oasisCmd.Stdout = os.Stdout
		oasisCmd.Stderr = os.Stderr
		oasisCmd.Stdin = os.Stdin

		if err := oasisCmd.Run(); err != nil {
			return fmt.Errorf("oasis exited with error: %w", err)
		}
		fmt.Printf("\n%s OASIS simulation complete\n", green("✓"))
		return nil
	},
}

func init() {
	// sim run flags
	simRunCmd.Flags().String("scenario", "", "Scenario or copy brief to simulate (required)")
	simRunCmd.Flags().Int("agents", 0, "Max number of agent personas (default: from config)")
	simRunCmd.Flags().String("output", "", "Save result as JSON to this file")
	simRunCmd.Flags().Bool("quiet", false, "Suppress per-agent output")

	// sim create flags
	simCreateCmd.Flags().String("scenario", "", "Simulation scenario / topic (required)")
	simCreateCmd.Flags().Int("rounds", 10, "Number of simulation rounds")
	simCreateCmd.Flags().Int("agents", 0, "Max agents (0 = all nodes)")
	simCreateCmd.Flags().String("platforms", "", "Platforms: twitter,reddit (default: both)")
	simCreateCmd.Flags().String("timezone", "", "Timezone for activity patterns (e.g. Asia/Taipei)")
	simCreateCmd.Flags().String("name", "", "Optional human-readable name for this simulation")

	// sim prepare flags
	simPrepareCmd.Flags().Int("concurrency", 0, "Max concurrent LLM calls (default: from config)")
	simPrepareCmd.Flags().Bool("status", false, "Show prepare status without running")

	// sim platform flags
	simPlatformCmd.Flags().String("scenario", "", "Simulation scenario / topic (required)")
	simPlatformCmd.Flags().Int("rounds", 10, "Number of simulation rounds")
	simPlatformCmd.Flags().Int("agents", 0, "Max agents (0 = all nodes)")
	simPlatformCmd.Flags().String("platforms", "", "Platforms: twitter,reddit (default: both)")
	simPlatformCmd.Flags().String("output", "", "Directory to save actions.jsonl")
	simPlatformCmd.Flags().Int("concurrency", 6, "Max concurrent LLM calls")
	simPlatformCmd.Flags().Bool("quiet", false, "Suppress action feed output")
	simPlatformCmd.Flags().String("branches", "", "Branch mode: 'auto' to generate branches via LLM")
	simPlatformCmd.Flags().Int("branch-count", 2, "Number of auto branches to generate (used with --branches auto)")
	simPlatformCmd.Flags().StringArray("branch", nil, "Explicit branch definition: 'name:description' (repeatable)")
	simPlatformCmd.Flags().BoolVar(&simGraphMemory, "graph-memory", false, "write simulation actions back to knowledge graph")
	simPlatformCmd.Flags().String("session", "", "Load scenario/agents from a prepared session (skips personality build)")
	simPlatformCmd.Flags().Bool("no-llm", false, "Template-only mode: zero LLM calls, use local templates for all content")
	simPlatformCmd.Flags().String("mode", "", "Simulation fidelity: nollm | batch (default) | heavy (1 LLM call/agent/round)")

	// sim oasis flags
	simOasisCmd.Flags().String("scenario", "", "Simulation scenario (required)")
	simOasisCmd.Flags().Int("rounds", 10, "Number of simulation rounds")
	simOasisCmd.Flags().String("config", "", "Path to OASIS config JSON (auto-generated if not set)")

	// sim branch flags
	simBranchCmd.Flags().String("scenario", "", "Base scenario for branching simulation (required)")
	simBranchCmd.Flags().Int("rounds", 10, "Number of simulation rounds per branch")
	simBranchCmd.Flags().Int("agents", 0, "Max agents (0 = all nodes)")
	simBranchCmd.Flags().String("platforms", "", "Platforms: twitter,reddit (default: both)")
	simBranchCmd.Flags().Int("concurrency", 6, "Max concurrent LLM calls")
	simBranchCmd.Flags().String("branches", "auto", "Branch mode: 'auto' or explicit with --branch flags")
	simBranchCmd.Flags().Int("branch-count", 2, "Number of auto branches (used with --branches auto)")
	simBranchCmd.Flags().StringArray("branch", nil, "Explicit branch: 'name:description' (repeatable)")
	simBranchCmd.Flags().Int("max-branches", 3, "Max simultaneous branches")

	// sim copy-react flags
	simCopyCmd.Flags().String("copy", "", "Copy text to test (required)")
	simCopyCmd.Flags().String("title", "", "Optional headline / title for the copy")
	simCopyCmd.Flags().String("platform", "twitter", "Platform context: twitter|reddit")
	simCopyCmd.Flags().Int("round", 1, "Round at which to inject the copy")
	simCopyCmd.Flags().Int("agents", 0, "Max agents (0 = all nodes)")
	simCopyCmd.Flags().Int("rounds", 5, "Total simulation rounds")
	simCopyCmd.Flags().Int("concurrency", 6, "Max concurrent LLM calls")

	simExportCmd.Flags().String("input", "", "Path to actions.jsonl (default: .fishnet/simulations/actions.jsonl)")
	simExportCmd.Flags().String("output", ".fishnet/simulations/export", "Directory to save export files")
	simExportCmd.Flags().String("scenario", "", "Scenario description (used in report; inferred from actions if omitted)")

	simCmd.AddCommand(simCreateCmd)
	simCmd.AddCommand(simPrepareCmd)
	simCmd.AddCommand(simRunCmd)
	simCmd.AddCommand(simPlatformCmd)
	simCmd.AddCommand(simStopCmd)
	simCmd.AddCommand(simStatusCmd)
	simCmd.AddCommand(simListCmd)
	simCmd.AddCommand(simOasisCmd)
	simCmd.AddCommand(simBranchCmd)
	simCmd.AddCommand(simCopyCmd)
	simCmd.AddCommand(simRunStatusCmd)
	simCmd.AddCommand(simEnvStatusCmd)
	simCmd.AddCommand(simCloseEnvCmd)
	simCmd.AddCommand(simExportCmd)
	rootCmd.AddCommand(simCmd)
}

// ─── sim branch ───────────────────────────────────────────────────────────────

var simBranchCmd = &cobra.Command{
	Use:   "branch",
	Short: "Run a branching simulation with multiple parallel timelines",
	Long: `Run the base scenario plus one or more 'what if' variants concurrently.
Each branch is a full independent simulation starting from round 0.`,
	Example: `  fishnet sim branch --scenario "AI regulation" --branches auto --rounds 10
  fishnet sim branch --scenario "Product launch" --branch "govt:government bans it" --branch "viral:goes viral overnight"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scenario, _ := cmd.Flags().GetString("scenario")
		rounds, _ := cmd.Flags().GetInt("rounds")
		maxAgents, _ := cmd.Flags().GetInt("agents")
		platformsStr, _ := cmd.Flags().GetString("platforms")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		branchMode, _ := cmd.Flags().GetString("branches")
		branchCount, _ := cmd.Flags().GetInt("branch-count")
		branchDefs, _ := cmd.Flags().GetStringArray("branch")
		maxBranches, _ := cmd.Flags().GetInt("max-branches")

		if scenario == "" {
			return fmt.Errorf("--scenario is required")
		}
		resolveAPIKey()

		var platforms []string
		if platformsStr != "" {
			for _, p := range strings.Split(platformsStr, ",") {
				p = strings.TrimSpace(strings.ToLower(p))
				if p == "twitter" || p == "reddit" {
					platforms = append(platforms, p)
				}
			}
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
		ps := sim.NewPlatformSim(database, client)
		ctx := context.Background()

		// Resolve branches
		var branches []sim.Branch
		if branchMode == "auto" {
			fmt.Printf("%s Generating %d branch variants via LLM…\n", cyan("→"), branchCount)
			autoB, err := ps.AutoBranches(ctx, scenario)
			if err != nil {
				return fmt.Errorf("auto branches: %w", err)
			}
			if branchCount > 0 && len(autoB) > branchCount {
				autoB = autoB[:branchCount]
			}
			branches = autoB
		}
		// Parse explicit --branch flags (in addition to or instead of auto)
		for i, def := range branchDefs {
			parts := strings.SplitN(def, ":", 2)
			name := parts[0]
			desc := ""
			if len(parts) == 2 {
				desc = parts[1]
			}
			branches = append(branches, sim.Branch{
				ID:          fmt.Sprintf("branch-%03d", len(branches)+1),
				Name:        name,
				Description: desc,
			})
			_ = i
		}
		if len(branches) == 0 {
			return fmt.Errorf("no branches defined; use --branches auto or --branch name:description")
		}

		fmt.Printf("\n%s Branching simulation\n  Scenario:  %s\n  Branches:  %d\n  Rounds:    %d\n\n",
			cyan("→"), bold(scenario), len(branches), rounds)
		for _, b := range branches {
			fmt.Printf("  + %-15s %s\n", b.Name+":", b.Description)
		}
		fmt.Println()

		baseCfg := sim.RoundConfig{
			Scenario:    scenario,
			MaxRounds:   rounds,
			MaxAgents:   maxAgents,
			Platforms:   platforms,
			Concurrency: concurrency,
		}
		results, err := ps.RunMultiBranch(ctx, projectID, sim.MultiBranchConfig{
			Base:        baseCfg,
			Branches:    branches,
			MaxBranches: maxBranches,
		}, nil)
		if err != nil {
			return err
		}

		fmt.Printf("\n%s Results:\n\n", green("✓"))
		fmt.Print(sim.FormatBranchResults(results))
		return nil
	},
}

// ─── sim create ───────────────────────────────────────────────────────────────

var simCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a named simulation config (first step of the prepare workflow)",
	Long: `Save a simulation configuration without running it.
After creation, run 'sim prepare <id>' to pre-build agent personalities,
then 'sim platform --session <id>' to run using those cached personalities.`,
	Example: `  fishnet sim create --scenario "AI regulation debate" --rounds 10 --agents 30
  fishnet sim create --scenario "Product launch" --platforms twitter --timezone Asia/Taipei`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scenario, _ := cmd.Flags().GetString("scenario")
		rounds, _ := cmd.Flags().GetInt("rounds")
		maxAgents, _ := cmd.Flags().GetInt("agents")
		platformsStr, _ := cmd.Flags().GetString("platforms")
		timezone, _ := cmd.Flags().GetString("timezone")
		name, _ := cmd.Flags().GetString("name")

		if scenario == "" {
			return fmt.Errorf("--scenario is required")
		}

		var platforms []string
		if platformsStr != "" {
			for _, p := range strings.Split(platformsStr, ",") {
				p = strings.TrimSpace(strings.ToLower(p))
				if p == "twitter" || p == "reddit" {
					platforms = append(platforms, p)
				}
			}
		}
		if len(platforms) == 0 {
			platforms = []string{"twitter", "reddit"}
		}
		if rounds <= 0 {
			rounds = cfg.Sim.DefaultRounds
		}
		if maxAgents <= 0 {
			maxAgents = cfg.Sim.MaxAgents
		}

		mgr := session.NewManager(".")
		sess := &session.Session{
			Name:      name,
			Scenario:  scenario,
			Platforms: platforms,
			Rounds:    rounds,
			MaxAgents: maxAgents,
			TimeZone:  timezone,
			Status:    "created",
		}
		if err := mgr.Save(sess); err != nil {
			return err
		}

		fmt.Printf("%s Created simulation: %s\n", green("✓"), bold(sess.ID))
		fmt.Printf("  Scenario:  %s\n", sess.Scenario)
		fmt.Printf("  Platforms: %s\n", strings.Join(sess.Platforms, ", "))
		fmt.Printf("  Rounds:    %d\n", sess.Rounds)
		fmt.Printf("  Agents:    %d\n", sess.MaxAgents)
		if sess.TimeZone != "" {
			fmt.Printf("  Timezone:  %s\n", sess.TimeZone)
		}
		fmt.Printf("\n  Next: fishnet sim prepare %s\n", sess.ID)
		return nil
	},
}

// ─── sim prepare ──────────────────────────────────────────────────────────────

var simPrepareCmd = &cobra.Command{
	Use:   "prepare <session-id>",
	Short: "Pre-build agent personalities for a simulation (prepare phase)",
	Long: `Run the LLM personality-building phase before the actual simulation.
Saves a snapshot of all agent profiles so 'sim platform --session <id>'
can skip this expensive step and start running immediately.`,
	Example: `  fishnet sim prepare sim-20240409-143022
  fishnet sim prepare sim-20240409-143022 --concurrency 8`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		showStatus, _ := cmd.Flags().GetBool("status")

		mgr := session.NewManager(".")
		sess, err := mgr.Load(args[0])
		if err != nil {
			return err
		}

		// ── Status-only mode ─────────────────────────────────────────────────
		if showStatus {
			printPrepareStatus(sess)
			return nil
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

		nodes, err := database.GetNodes(projectID)
		if err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("no nodes found; run: fishnet analyze first")
		}
		if sess.MaxAgents > 0 && len(nodes) > sess.MaxAgents {
			nodes = nodes[:sess.MaxAgents]
		}

		if concurrency <= 0 {
			concurrency = cfg.LLM.MaxConcurrency
		}

		client := llm.New(cfg.LLM)
		ps := sim.NewPlatformSim(database, client)

		fmt.Printf("\n%s Building personalities for %d agents (concurrency=%d)...\n",
			cyan("→"), len(nodes), concurrency)

		ctx := context.Background()
		personalities := ps.BuildPersonalities(ctx, nodes, sess.Scenario, concurrency, nil)

		// Persist personas
		store := sim.NewPersonaStore(".")
		pp := &sim.PreparedPersonalities{
			SessionID:  sess.ID,
			Scenario:   sess.Scenario,
			AgentCount: len(personalities),
			Data:       personalities,
		}
		if err := store.Save(pp); err != nil {
			return fmt.Errorf("save personas: %w", err)
		}

		// Update session status
		sess.MarkPrepared(len(personalities))
		if err := mgr.Save(sess); err != nil {
			return fmt.Errorf("update session: %w", err)
		}

		fmt.Printf("%s Prepared %d agent profiles\n\n", green("✓"), len(personalities))

		// Preview: first 10 agents
		fmt.Printf("%s Agent Preview:\n", bold("→"))
		limit := 10
		if len(personalities) < limit {
			limit = len(personalities)
		}
		fmt.Printf("  %-22s %-14s %-12s %-9s %s\n",
			"NAME", "TYPE", "INTERESTS", "STYLE", "STANCE")
		fmt.Println("  " + strings.Repeat("-", 78))
		for _, p := range personalities[:limit] {
			interests := ""
			if len(p.Interests) > 0 {
				interests = strings.Join(p.Interests, ",")
				if len(interests) > 12 {
					interests = interests[:11] + "…"
				}
			}
			name := p.Name
			if len(name) > 22 {
				name = name[:21] + "…"
			}
			fmt.Printf("  %-22s %-14s %-12s %-9s %s\n",
				name, p.NodeType, interests, p.PostStyle, p.Stance)
		}
		if len(personalities) > limit {
			fmt.Printf("  … and %d more agents\n", len(personalities)-limit)
		}

		// Simulation config preview
		fmt.Printf("\n%s Simulation Config:\n", bold("→"))
		fmt.Printf("  Scenario:  %s\n", sess.Scenario)
		fmt.Printf("  Platforms: %s\n", strings.Join(sess.Platforms, ", "))
		fmt.Printf("  Rounds:    %d\n", sess.Rounds)
		fmt.Printf("  Agents:    %d\n", len(personalities))
		if sess.TimeZone != "" {
			fmt.Printf("  Timezone:  %s\n", sess.TimeZone)
		}

		// Stance breakdown
		stances := map[string]int{}
		for _, p := range personalities {
			stances[p.Stance]++
		}
		fmt.Printf("\n%s Stance Distribution:\n", bold("→"))
		for _, stance := range []string{"supportive", "neutral", "opposing", "observer"} {
			if n := stances[stance]; n > 0 {
				bar := strings.Repeat("█", n*20/len(personalities))
				fmt.Printf("  %-12s %3d  %s\n", stance, n, bar)
			}
		}

		fmt.Printf("\n  Run: fishnet sim platform --session %s\n", sess.ID)
		return nil
	},
}

func printPrepareStatus(sess *session.Session) {
	fmt.Printf("\n%s  %s\n", bold("Session:"), sess.ID)
	if sess.Name != "" {
		fmt.Printf("  Name:     %s\n", sess.Name)
	}
	fmt.Printf("  Status:   %s\n", statusColorFn(sess.Status)(sess.Status))
	fmt.Printf("  Scenario: %s\n", sess.Scenario)
	if sess.Status == "prepared" || sess.PersonaCount > 0 {
		fmt.Printf("  Agents:   %d prepared\n", sess.PersonaCount)
		if !sess.PreparedAt.IsZero() {
			fmt.Printf("  Prepared: %s\n", sess.PreparedAt.Format("2006-01-02 15:04:05"))
		}
	} else {
		fmt.Printf("  Agents:   not yet prepared\n")
		fmt.Printf("\n  Run: fishnet sim prepare %s\n", sess.ID)
	}
	fmt.Printf("  Rounds:   %d  Platforms: %s\n",
		sess.Rounds, strings.Join(sess.Platforms, ", "))
	fmt.Println()
}

func statusColorFn(status string) func(string) string {
	switch status {
	case "completed":
		return green
	case "running":
		return cyan
	case "prepared":
		return cyan
	case "failed", "interrupted":
		return yellow
	default:
		return func(s string) string { return s }
	}
}

// ─── sim run-status ───────────────────────────────────────────────────────────

var simRunStatusCmd = &cobra.Command{
	Use:   "run-status [run-id]",
	Short: "Show status of simulation runs (all or a specific run)",
	Example: `  fishnet sim run-status
  fishnet sim run-status run-20240409-143022`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := simrun.NewManager(".")

		// Single run detail
		if len(args) == 1 {
			r, err := mgr.Load(args[0])
			if err != nil {
				return err
			}
			printSimRunDetail(r)
			return nil
		}

		// List all runs
		runs, err := mgr.List()
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			fmt.Println("No simulation runs found. Run: fishnet sim platform --scenario \"...\"")
			return nil
		}
		fmt.Printf("%-22s  %-12s  %4s  %-12s  %-10s  %s\n",
			"RUN ID", "STATUS", "PCT", "ROUNDS", "PLATFORMS", "SCENARIO")
		fmt.Println(strings.Repeat("-", 85))
		for _, r := range runs {
			platforms := strings.Join(r.Platforms, "+")
			if platforms == "" {
				platforms = "tw+rd"
			}
			rounds := fmt.Sprintf("%d/%d", r.Rounds, r.MaxRounds)
			sc := r.Scenario
			if len(sc) > 35 {
				sc = sc[:34] + "…"
			}
			fmt.Printf("%-22s  %-12s  %3d%%  %-12s  %-10s  %s\n",
				r.ID, r.Status, r.Progress(), rounds, platforms, sc)
		}
		return nil
	},
}

func printSimRunDetail(r *simrun.SimRun) {
	statusFn := cyan
	switch r.Status {
	case "completed":
		statusFn = green
	case "failed", "stopped":
		statusFn = yellow
	}
	fmt.Printf("\n%s  %s\n", bold("Run:"), r.ID)
	fmt.Printf("  Status:    %s\n", statusFn(r.Status))
	fmt.Printf("  Scenario:  %s\n", r.Scenario)
	fmt.Printf("  Progress:  %d%%  (round %d / %d)\n", r.Progress(), r.Rounds, r.MaxRounds)
	fmt.Printf("  Platforms: %s\n", strings.Join(r.Platforms, ", "))
	if r.SessionID != "" {
		fmt.Printf("  Session:   %s\n", r.SessionID)
	}
	fmt.Printf("\n  Twitter:   %d posts  %d actions\n", r.Twitter.Posts, r.Twitter.Actions)
	fmt.Printf("  Reddit:    %d posts  %d actions\n", r.Reddit.Posts, r.Reddit.Actions)
	if r.PID > 0 {
		fmt.Printf("  PID:       %d\n", r.PID)
	}
	if !r.StartedAt.IsZero() {
		fmt.Printf("  Started:   %s\n", r.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if !r.FinishedAt.IsZero() {
		fmt.Printf("  Finished:  %s\n", r.FinishedAt.Format("2006-01-02 15:04:05"))
	}
	if r.ErrorMsg != "" {
		fmt.Printf("  Error:     %s\n", red(r.ErrorMsg))
	}
	fmt.Println()
}

// ─── sim env-status ───────────────────────────────────────────────────────────

var simEnvStatusCmd = &cobra.Command{
	Use:   "env-status",
	Short: "Check environment health (LLM API, database, storage)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("\n%s Environment Health\n\n", bold("→"))

		// DB check
		dbOK := false
		database, err := db.Open(cfg.DBPath)
		if err == nil {
			_, dbErr := database.ProjectByName(cfg.Project)
			dbOK = dbErr == nil || true // even "not found" means DB is readable
			database.Close()
			dbOK = true
		}
		dbStatus := green("✓  reachable")
		if !dbOK {
			dbStatus = red("✗  " + err.Error())
		}
		fmt.Printf("  Database:    %s\n", dbStatus)

		// Storage dirs
		dirs := []string{".fishnet/sessions", ".fishnet/tasks", ".fishnet/simruns", ".fishnet/reports", ".fishnet/interactions"}
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0755); err == nil {
				fmt.Printf("  %-14s %s\n", d+":", green("✓  writable"))
			} else {
				fmt.Printf("  %-14s %s\n", d+":", red("✗  "+err.Error()))
			}
		}

		// LLM key presence (no actual API call — that would cost tokens)
		resolveAPIKey()
		llmStatus := green("✓  key configured")
		if cfg.LLM.APIKey == "" {
			if cfg.LLM.Provider == "ollama" {
				llmStatus = cyan("○  ollama (no key needed)")
			} else {
				llmStatus = yellow("!  no API key set")
			}
		}
		fmt.Printf("  LLM %-10s %s  [provider: %s  model: %s]\n",
			"API:", llmStatus, cfg.LLM.Provider, cfg.LLM.Model)

		// Running simulation check
		if pid, err := readPID(); err == nil {
			fmt.Printf("  Active sim:  %s (PID %d)\n", green("● running"), pid)
		} else {
			fmt.Printf("  Active sim:  %s\n", cyan("○ none"))
		}

		fmt.Println()
		return nil
	},
}

// ─── sim close-env ────────────────────────────────────────────────────────────

var simCloseEnvCmd = &cobra.Command{
	Use:   "close-env <run-id>",
	Short: "Terminate a specific simulation run by run ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := simrun.NewManager(".")
		r, err := mgr.Load(args[0])
		if err != nil {
			return err
		}
		if r.Status != "running" && r.Status != "pending" {
			fmt.Printf("%s Run %s is already %s\n", yellow("!"), r.ID, r.Status)
			return nil
		}
		if r.PID <= 0 {
			return fmt.Errorf("no PID recorded for run %s", r.ID)
		}
		proc, err := os.FindProcess(r.PID)
		if err != nil {
			return fmt.Errorf("process %d not found: %w", r.PID, err)
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("failed to signal PID %d: %w", r.PID, err)
		}
		r.MarkStopped()
		_ = mgr.Save(r)
		fmt.Printf("%s Sent SIGTERM to run %s (PID %d)\n", green("✓"), r.ID, r.PID)
		return nil
	},
}

// ─── sim copy-react ───────────────────────────────────────────────────────────

var simCopyCmd = &cobra.Command{
	Use:   "copy-react",
	Short: "Test how agents react to a piece of copy/content (BD use case)",
	Long: `Simulate agent reactions to marketing copy or product messaging.
Injects the copy as a Brand post at the specified round and collects agent reactions.`,
	Example: `  fishnet sim copy-react --copy "Our new AI assistant..." --round 2 --platform twitter
  fishnet sim copy-react --copy "Introducing MiroFish" --title "Big announcement" --agents 20`,
	RunE: func(cmd *cobra.Command, args []string) error {
		copyText, _ := cmd.Flags().GetString("copy")
		title, _ := cmd.Flags().GetString("title")
		platform, _ := cmd.Flags().GetString("platform")
		injectRound, _ := cmd.Flags().GetInt("round")
		maxAgents, _ := cmd.Flags().GetInt("agents")
		rounds, _ := cmd.Flags().GetInt("rounds")
		concurrency, _ := cmd.Flags().GetInt("concurrency")

		if copyText == "" {
			return fmt.Errorf("--copy is required")
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
		ctx := context.Background()

		displayCopy := copyText
		if len(displayCopy) > 60 {
			displayCopy = displayCopy[:60] + "…"
		}
		fmt.Printf("\n%s Copy reaction simulation\n  Copy:     %s\n  Platform: %s\n  Round:    %d\n\n",
			cyan("→"), bold(displayCopy), platform, injectRound)

		copyReactCfg := &sim.CopyReactionConfig{
			CopyText:    copyText,
			CopyTitle:   title,
			Platform:    platform,
			InjectRound: injectRound,
		}

		// Run a short platform simulation with copy injection to build context,
		// then run focused copy reactions using the same personality pool.
		progressCh := make(chan sim.RoundProgress, 64)
		done := make(chan error, 1)
		go func() {
			done <- ps.Run(ctx, projectID, sim.RoundConfig{
				Scenario:     copyText,
				MaxRounds:    rounds,
				MaxAgents:    maxAgents,
				Platforms:    []string{platform},
				Concurrency:  concurrency,
				CopyReaction: copyReactCfg,
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
				fmt.Printf("\r  Round %2d/%d [%s]", prog.Round, prog.MaxRounds, bar)
			}
		}
		if err := <-done; err != nil {
			return err
		}

		fmt.Printf("\n\n%s Running copy reactions…\n", cyan("→"))

		// Run focused per-agent copy reactions via the high-level wrapper.
		reactions, err := ps.RunCopyReactFromProject(ctx, projectID, *copyReactCfg, maxAgents)
		if err != nil {
			return err
		}

		fmt.Printf("\n%s Copy Reaction Results (%d agents):\n\n", green("✓"), len(reactions))
		fmt.Print(sim.FormatCopyReactions(reactions))

		// Summary stats
		pos, neg, neu := 0, 0, 0
		for _, r := range reactions {
			switch r.Sentiment {
			case "positive":
				pos++
			case "negative":
				neg++
			default:
				neu++
			}
		}
		fmt.Printf("\nSentiment: +%d / -%d / ~%d\n", pos, neg, neu)
		return nil
	},
}

// ─── sim export ───────────────────────────────────────────────────────────────

var simExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Generate BD report, key agents, tactics and quotes from a completed simulation",
	Example: `  fishnet sim export
  fishnet sim export --input .fishnet/simulations/actions.jsonl --output ./export
  fishnet sim export --scenario "AI regulation debate"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		inputPath, _ := cmd.Flags().GetString("input")
		if inputPath == "" {
			inputPath = ".fishnet/simulations/actions.jsonl"
		}
		outputDir, _ := cmd.Flags().GetString("output")
		scenario, _ := cmd.Flags().GetString("scenario")

		fmt.Printf("%s Loading actions from %s…\n", cyan("→"), inputPath)
		actions, err := sim.LoadActionsFromJSONL(inputPath)
		if err != nil {
			return fmt.Errorf("load actions: %w", err)
		}
		if len(actions) == 0 {
			return fmt.Errorf("no actions found in %s", inputPath)
		}
		fmt.Printf("%s Loaded %d actions\n", green("✓"), len(actions))

		// Infer scenario from actions if not provided
		if scenario == "" {
			scenario = "social media simulation"
		}

		// Infer rounds from max Round field
		maxRound := 0
		for _, a := range actions {
			if a.Round > maxRound {
				maxRound = a.Round
			}
		}

		client := llm.New(cfg.LLM)
		input := &sim.ExportInput{
			Scenario: scenario,
			Rounds:   maxRound,
			Actions:  actions,
		}

		fmt.Printf("%s Generating export documents (1 LLM call)…\n", cyan("→"))
		doc, err := sim.GenerateExport(cmd.Context(), client, input)
		if err != nil {
			return fmt.Errorf("generate export: %w", err)
		}

		if err := sim.SaveExport(doc, outputDir); err != nil {
			return fmt.Errorf("save export: %w", err)
		}

		fmt.Printf("\n%s Export saved to %s/\n", green("✓"), outputDir)
		fmt.Printf("  bd-report.md    — BD insights and audience analysis\n")
		fmt.Printf("  key-agents.md   — Influential agent analysis\n")
		fmt.Printf("  tactics.md      — Strategic recommendations\n")
		fmt.Printf("  quotes.json     — Top %d quotes + agent highlights\n", len(doc.TopQuotes))
		fmt.Printf("  export.json     — Full export data\n")

		// Print top 5 quotes inline
		if len(doc.TopQuotes) > 0 {
			fmt.Printf("\n%s Top quotes:\n", bold("✦"))
			for i, q := range doc.TopQuotes {
				if i >= 5 {
					break
				}
				fmt.Printf("  [%s @%s] %s\n", q.Platform, q.Agent, q.Content)
			}
		}

		return nil
	},
}
