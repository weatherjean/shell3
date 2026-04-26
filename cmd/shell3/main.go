package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	// Self-registering OAuth providers. Each package's init() calls
	// llm.Register; the main app dispatches generically via llm.Get.
	_ "github.com/weatherjean/shell3/internal/providers/codex" // codex-compat: ChatGPT subscription auth
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
