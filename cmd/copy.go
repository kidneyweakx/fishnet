package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"fishnet/internal/db"
	"fishnet/internal/llm"
	"fishnet/internal/sim"
)

var copyCmd = &cobra.Command{
	Use:   "copy",
	Short: "Generate copy variants from a scenario",
	Long: `Run a quick simulation then generate N distinct copy variants.
The personas from the knowledge graph provide feedback to guide the copy generation.`,
	Example: `  fishnet copy --brief "Launch email for AI product" --count 5
  fishnet copy --brief "$(cat brief.txt)" --count 10 --style professional
  fishnet copy --brief "Product launch" --count 3 --style viral --output copy.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		brief, _ := cmd.Flags().GetString("brief")
		count, _ := cmd.Flags().GetInt("count")
		style, _ := cmd.Flags().GetString("style")
		output, _ := cmd.Flags().GetString("output")
		noSim, _ := cmd.Flags().GetBool("no-sim")

		if brief == "" {
			return fmt.Errorf("--brief is required")
		}
		if count <= 0 {
			count = 5
		}
		if style == "" {
			style = "clear and compelling"
		}

		if cfg.LLM.APIKey == "" {
			if k := os.Getenv("FISHNET_API_KEY"); k != "" {
				cfg.LLM.APIKey = k
			} else if k := os.Getenv("OPENAI_API_KEY"); k != "" {
				cfg.LLM.APIKey = k
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
		engine := sim.New(database, client)
		ctx := context.Background()

		var responses []sim.AgentResponse

		if !noSim {
			// Step 1: Quick simulation to gather persona feedback
			fmt.Printf("\n%s Gathering persona feedback...\n", cyan("→"))
			maxAgents := cfg.Sim.MaxAgents
			if maxAgents > 15 {
				maxAgents = 15 // fast mode for copy gen
			}
			result, err := engine.RunCopySimulation(ctx, projectID, brief, maxAgents,
				func(resp sim.AgentResponse) {
					fmt.Printf("  [%d/10] %s\n", resp.Score, cyan(resp.AgentName))
				})
			if err != nil {
				fmt.Printf("  %s Simulation failed, generating without feedback: %v\n", yellow("!"), err)
			} else {
				responses = result.Responses
				fmt.Printf("  %d persona responses collected\n", len(responses))
			}
		}

		// Step 2: Generate copy variants
		fmt.Printf("\n%s Generating %d copy variants (style: %s)...\n",
			cyan("→"), count, style)

		variants, err := engine.GenerateCopy(ctx, brief, style, count, responses)
		if err != nil {
			return fmt.Errorf("generate copy: %w", err)
		}

		// Display
		fmt.Printf("\n%s Copy Variants\n", bold("══"))
		sep := strings.Repeat("─", 60)
		for i, v := range variants {
			fmt.Printf("\n%s\n%s %d\n%s\n%s\n", sep, bold("Variant"), i+1, sep, v)
		}
		fmt.Printf("\n%s\n", sep)

		// Save
		if output != "" {
			type outFormat struct {
				Brief    string   `json:"brief"`
				Style    string   `json:"style"`
				Variants []string `json:"variants"`
			}
			data, _ := json.MarshalIndent(outFormat{
				Brief:    brief,
				Style:    style,
				Variants: variants,
			}, "", "  ")
			if err := os.WriteFile(output, data, 0644); err != nil {
				fmt.Printf("%s Could not write output: %v\n", yellow("!"), err)
			} else {
				fmt.Printf("%s Saved to %s\n", green("✓"), output)
			}
		}
		return nil
	},
}

func init() {
	copyCmd.Flags().String("brief", "", "Copy brief or scenario (required)")
	copyCmd.Flags().Int("count", 5, "Number of copy variants to generate")
	copyCmd.Flags().String("style", "", "Style: viral|professional|casual|storytelling|technical")
	copyCmd.Flags().String("output", "", "Save variants as JSON to this file")
	copyCmd.Flags().Bool("no-sim", false, "Skip simulation, generate copy directly")
	rootCmd.AddCommand(copyCmd)
}
