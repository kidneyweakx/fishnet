package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"fishnet/internal/nlp"
)

var modelCmd = &cobra.Command{
	Use:   "model",
	Short: "Manage local NLP models",
}

var modelDownloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download ONNX NER model for offline extraction",
	Long: `Downloads the multilingual BERT NER model (~86 MB, one-time).
After downloading, use --onnx flag with analyze for fast local extraction:

  fishnet analyze --onnx

The model supports Chinese and English natively, with no API key required.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ready, _ := nlp.IsModelReady()
		if ready {
			dir, _ := nlp.ModelDir()
			fmt.Printf("%s Model already downloaded at %s\n", green("✓"), dir)
			return nil
		}
		fmt.Printf("%s Downloading ONNX NER model (~86 MB)...\n", cyan("→"))
		if err := nlp.EnsureModels(true); err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		dir, _ := nlp.ModelDir()
		fmt.Printf("%s Model ready at %s\n", green("✓"), dir)
		fmt.Printf("\n  Now run: %s\n", bold("fishnet analyze --onnx"))
		return nil
	},
}

var modelStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show ONNX model download status",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := nlp.ModelDir()
		if err != nil {
			return err
		}
		fmt.Printf("Model directory: %s\n", dir)
		ready, err := nlp.IsModelReady()
		if err != nil {
			return err
		}
		if ready {
			fmt.Printf("Status: %s ready (use --onnx flag with analyze)\n", green("✓"))
		} else {
			fmt.Printf("Status: %s not downloaded (run: fishnet model download)\n", yellow("!"))
		}
		return nil
	},
}

func init() {
	modelCmd.AddCommand(modelDownloadCmd)
	modelCmd.AddCommand(modelStatusCmd)
	rootCmd.AddCommand(modelCmd)
}
