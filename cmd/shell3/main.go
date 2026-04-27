package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	// Self-registering adapters. Each package's init() calls llm.Register;
	// the main app dispatches generically via llm.Get.
	_ "github.com/weatherjean/shell3/internal/adapters/codex"  // codex: ChatGPT subscription auth
	_ "github.com/weatherjean/shell3/internal/adapters/openai" // openai-compatible: Ollama, OpenAI, OpenRouter, etc.

	"github.com/weatherjean/shell3/internal/patchapp"
)

func main() {
	root := &cobra.Command{
		Use:   "shell3",
		Short: "Minimal Unix-composable coding agent",
	}

	runCmd := newRunCommand()
	root.RunE = runCmd.RunE
	root.Args = cobra.ArbitraryArgs
	root.Flags().AddFlagSet(runCmd.Flags())

	// Print brand header on every subcommand and on --help output. The
	// root chat command suppresses it (handled inside RunE) since chat
	// renders its own welcome banner. Skip when stdout is not a terminal
	// so piped output stays clean.
	maybeHeader := func() {
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return
		}
		patchapp.PrintHeader(os.Stdout)
	}
	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if cmd == root {
			return
		}
		maybeHeader()
	}
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		maybeHeader()
		defaultHelp(cmd, args)
	})

	root.AddCommand(newInitCommand())
	root.AddCommand(newAuthCommand())
	root.AddCommand(newSecretsCommand())
	root.AddCommand(newDocsCommand())
	root.AddCommand(newWidgetCommand())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
