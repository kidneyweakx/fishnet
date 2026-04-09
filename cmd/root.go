package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"fishnet/internal/config"
	"fishnet/internal/db"
	"fishnet/internal/tui"
)

var cfg *config.Config

var rootCmd = &cobra.Command{
	Use:   "fishnet",
	Short: "Local GraphRAG + social simulation CLI",
	Long: `fishnet — lightweight document analysis & social simulation

Read docs → build knowledge graph → simulate Twitter/Reddit → generate report → interview agents.

Examples:
  fishnet init myproject --dir ./docs
  fishnet analyze
  fishnet graph stats
  fishnet sim platform --scenario "AI regulation debate" --rounds 10
  fishnet report generate
  fishnet interview Alice`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Launch interactive TUI when called with no subcommand
		if err := resolveAPIKey(); err != nil {
			return err
		}
		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer database.Close()
		projectID, err := database.ProjectByName(cfg.Project)
		if err != nil {
			// No project yet — still launch TUI, it will guide the user
			projectID = ""
		}
		return tui.Run(cfg, database, projectID)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(loadConfig)

	rootCmd.PersistentFlags().String("model", "", "LLM model (overrides config)")
	rootCmd.PersistentFlags().String("api-key", "", "API key (overrides config)")
	rootCmd.PersistentFlags().String("base-url", "", "API base URL (overrides config)")
	rootCmd.PersistentFlags().String("provider", "", "Provider: openai|anthropic|ollama|codex|codex-cli|clicliproxy (overrides config)")
}

func loadConfig() {
	var err error
	cfg, err = config.Load()
	if err != nil {
		cfg = config.Default()
	}

	if v, _ := rootCmd.PersistentFlags().GetString("model"); v != "" {
		cfg.LLM.Model = v
	}
	if v, _ := rootCmd.PersistentFlags().GetString("api-key"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v, _ := rootCmd.PersistentFlags().GetString("base-url"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v, _ := rootCmd.PersistentFlags().GetString("provider"); v != "" {
		cfg.LLM.Provider = v
	}
}

// resolveAPIKey fills cfg.LLM.APIKey from env if not set.
// For codex providers, CODEX_API_KEY is also checked (falls through to OPENAI_API_KEY).
func resolveAPIKey() error {
	if cfg.LLM.APIKey != "" {
		return nil
	}
	envVars := []string{"FISHNET_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"}
	// Prepend CODEX_API_KEY for codex providers so it takes precedence.
	if cfg.LLM.Provider == "codex" || cfg.LLM.Provider == "codex-cli" {
		envVars = append([]string{"CODEX_API_KEY"}, envVars...)
	}
	for _, env := range envVars {
		if v := os.Getenv(env); v != "" {
			cfg.LLM.APIKey = v
			return nil
		}
	}
	return nil // not an error — some commands don't need it
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func bold(s string) string  { return "\033[1m" + s + "\033[0m" }
func green(s string) string { return "\033[32m" + s + "\033[0m" }
func cyan(s string) string  { return "\033[36m" + s + "\033[0m" }
func yellow(s string) string { return "\033[33m" + s + "\033[0m" }
func red(s string) string   { return "\033[31m" + s + "\033[0m" }

func die(msg string, err error) {
	fmt.Fprintf(os.Stderr, red("Error")+": %s: %v\n", msg, err)
	os.Exit(1)
}
