package cmd

import (
	"context"
	"fmt"
	"time"

	"fishnet/internal/config"
	"fishnet/internal/llm"

	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication for AI providers",
}

var authCodexCmd = &cobra.Command{
	Use:   "codex",
	Short: "Authenticate with OpenAI Codex via browser OAuth (PKCE)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Starting Codex OAuth authentication...")
		tokens, err := llm.CodexLogin()
		if err != nil {
			return fmt.Errorf("auth failed: %w", err)
		}
		if err := llm.SaveCodexTokens(tokens); err != nil {
			return fmt.Errorf("save tokens: %w", err)
		}
		fmt.Printf("\n✓ Authenticated! Tokens saved to: %s\n", llm.CodexTokenPath())
		fmt.Println("\nTo use Codex OAuth in fishnet, set provider in your config:")
		fmt.Println(`  "llm": {"provider": "codex-oauth", "model": "gpt-4o"}`)
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication status for all providers",
	RunE: func(cmd *cobra.Command, args []string) error {
		if llm.CodexLoggedIn() {
			tokens, _ := llm.LoadCodexTokens()
			fmt.Printf("codex-oauth:  ✓ logged in")
			if tokens != nil && tokens.ExpiresAtMs > 0 {
				remaining := tokens.ExpiresAtMs - llm.NowMs()
				if remaining > 0 {
					fmt.Printf(" (expires in %d min)", remaining/60000)
				} else {
					fmt.Printf(" (token expired — will auto-refresh on next use)")
				}
			}
			fmt.Println()
		} else {
			fmt.Println("codex-oauth:  ✗ not logged in  →  run: fishnet auth codex")
		}
		if llm.CheckCodexCLI() {
			fmt.Println("codex-cli:   ✓ codex binary found")
		} else {
			fmt.Println("codex-cli:   ✗ not found")
		}
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored Codex OAuth tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := llm.RemoveCodexTokens(); err != nil {
			return fmt.Errorf("remove tokens: %w", err)
		}
		fmt.Println("✓ Codex OAuth tokens removed")
		return nil
	},
}

var authPingCmd = &cobra.Command{
	Use:   "ping [model]",
	Short: "Test Codex OAuth by sending a real LLM request",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !llm.CodexLoggedIn() {
			return fmt.Errorf("not logged in — run: fishnet auth codex")
		}

		model := "gpt-4o"
		if len(args) > 0 {
			model = args[0]
		} else {
			// Try to load from project config
			if cfg, err := config.Load(); err == nil && cfg.LLM.Model != "" && cfg.LLM.Provider == "codex-oauth" {
				model = cfg.LLM.Model
			}
		}

		fmt.Printf("Testing codex-oauth with model: %s\n", model)
		fmt.Println("Sending: \"Reply with exactly: PONG\"")

		cfg := config.LLMConfig{
			Provider: "codex-oauth",
			Model:    model,
		}
		client := llm.New(cfg)

		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		resp, err := client.Chat(ctx, []llm.Message{
			{Role: "user", Content: "Reply with exactly: PONG"},
		})
		elapsed := time.Since(start).Round(time.Millisecond)

		if err != nil {
			fmt.Printf("\n✗ Error (%s): %v\n", elapsed, err)
			return err
		}
		fmt.Printf("\n✓ Response (%s): %s\n", elapsed, resp)
		return nil
	},
}

func init() {
	authCmd.AddCommand(authCodexCmd, authStatusCmd, authLogoutCmd, authPingCmd)
	rootCmd.AddCommand(authCmd)
}
