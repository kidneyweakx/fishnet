package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"fishnet/internal/config"
	"fishnet/internal/db"
)

var initCmd = &cobra.Command{
	Use:   "init <project-name>",
	Short: "Initialize a fishnet project in the current directory",
	Args:  cobra.ExactArgs(1),
	Example: `  fishnet init myproject
  fishnet init copylab --dir ./brand-docs`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		dir, _ := cmd.Flags().GetString("dir")
		model, _ := cmd.Flags().GetString("model")
		provider, _ := cmd.Flags().GetString("provider")
		apiKey, _ := cmd.Flags().GetString("api-key")
		baseURL, _ := cmd.Flags().GetString("base-url")

		// Resolve source dir
		if dir == "" {
			dir = "."
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return err
		}

		// Create .fishnet/ dir
		if err := os.MkdirAll(".fishnet", 0755); err != nil {
			return fmt.Errorf("create .fishnet: %w", err)
		}

		// Build config
		c := config.Default()
		c.Project = name
		c.DBPath = ".fishnet/fishnet.db"
		if dir != "." {
			// Store as relative path so project is portable
			rel, err := filepath.Rel(".", absDir)
			if err == nil {
				c.DBPath = ".fishnet/fishnet.db"
				_ = rel
			}
		}
		if model != "" {
			c.LLM.Model = model
		}
		if provider != "" {
			c.LLM.Provider = provider
			if baseURL == "" {
				c.LLM.BaseURL = config.ProviderBaseURL(provider)
			}
		}
		if apiKey != "" {
			c.LLM.APIKey = apiKey
		}
		if baseURL != "" {
			c.LLM.BaseURL = baseURL
		}

		if err := config.Save(c); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		// Initialize DB
		database, err := db.Open(c.DBPath)
		if err != nil {
			return fmt.Errorf("init db: %w", err)
		}
		defer database.Close()

		projectID, err := database.UpsertProject(name, absDir)
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		_ = projectID

		fmt.Printf("%s Project %s initialized\n", green("✓"), bold(name))
		fmt.Printf("  Dir: %s\n", cyan(absDir))
		fmt.Printf("  DB:  %s\n", cyan(c.DBPath))
		fmt.Printf("  Provider: %s / %s\n", c.LLM.Provider, cyan(c.LLM.Model))
		fmt.Println()
		fmt.Printf("Next steps:\n")
		fmt.Printf("  1. Set your API key:  export FISHNET_API_KEY=sk-...\n")
		fmt.Printf("     Or edit:           %s\n", ".fishnet/config.json")
		fmt.Printf("  2. Analyze docs:      fishnet analyze --dir %s\n", dir)
		fmt.Printf("  3. Build graph:       fishnet graph stats\n")
		fmt.Printf("  4. Run simulation:    fishnet sim run --scenario \"...\"\n")
		return nil
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or set configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := config.Load()
		if err != nil {
			return err
		}
		fmt.Printf("Project:  %s\n", bold(c.Project))
		fmt.Printf("DB:       %s\n", c.DBPath)
		fmt.Printf("Provider: %s\n", c.LLM.Provider)
		fmt.Printf("Model:    %s\n", cyan(c.LLM.Model))
		fmt.Printf("Base URL: %s\n", c.LLM.BaseURL)
		apiKey := c.LLM.APIKey
		if len(apiKey) > 8 {
			apiKey = apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
		}
		fmt.Printf("API Key:  %s\n", apiKey)
		fmt.Printf("Rate:     %.0f req/s, %d concurrent\n", c.LLM.RateLimit, c.LLM.MaxConcurrency)
		fmt.Printf("Chunks:   size=%d overlap=%d\n", c.Graph.ChunkSize, c.Graph.ChunkOverlap)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value (model, provider, api-key, base-url)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := config.Load()
		if err != nil {
			c = config.Default()
		}
		key, val := args[0], args[1]
		switch key {
		case "model":
			c.LLM.Model = val
		case "provider":
			c.LLM.Provider = val
			if c.LLM.BaseURL == "" {
				c.LLM.BaseURL = config.ProviderBaseURL(val)
			}
		case "api-key":
			c.LLM.APIKey = val
		case "base-url":
			c.LLM.BaseURL = val
		default:
			return fmt.Errorf("unknown key %q; valid: model, provider, api-key, base-url", key)
		}
		if err := config.Save(c); err != nil {
			return err
		}
		fmt.Printf("%s Set %s = %s\n", green("✓"), bold(key), cyan(val))
		return nil
	},
}

func init() {
	initCmd.Flags().String("dir", ".", "Source documents directory")
	initCmd.Flags().String("model", "", "LLM model name")
	initCmd.Flags().String("provider", "", "LLM provider (openai|anthropic|ollama)")
	initCmd.Flags().String("api-key", "", "LLM API key")
	initCmd.Flags().String("base-url", "", "LLM base URL")

	configCmd.AddCommand(configShowCmd, configSetCmd)
	rootCmd.AddCommand(initCmd, configCmd)
}
