package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	// Self-registering adapters. Each package's init() calls llm.Register;
	// the main app dispatches generically via llm.Get.
	_ "github.com/weatherjean/shell3/internal/adapters/codex"  // codex: ChatGPT subscription auth
	_ "github.com/weatherjean/shell3/internal/adapters/openai" // openai-compatible: Ollama, OpenAI, OpenRouter, etc.
)

func main() {
	root := &cobra.Command{
		Use:   "shell3",
		Short: "Minimal Unix-composable coding agent",
	}

	runCmd := newRunCommand()
	root.RunE = runCmd.RunE
	root.Flags().AddFlagSet(runCmd.Flags())

	root.AddCommand(newInitCommand())
	root.AddCommand(newAuthCommand())
	root.AddCommand(newDocsCommand())
	root.AddCommand(newDestroyCommand())
	root.AddCommand(newWidgetCommand())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
