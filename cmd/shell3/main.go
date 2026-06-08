package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/patchapp"
)

// version is the build version, set at link time via -X main.version (the
// Makefile derives it from the latest git tag); "dev" for a plain go build.
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:     "shell3",
		Short:   "Minimal Unix-composable coding agent",
		Version: version,
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
		if !shouldPrintHeaderInPreRun(root, cmd) {
			return
		}
		maybeHeader()
	}
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		maybeHeader()
		defaultHelp(cmd, args)
	})

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func shouldPrintHeaderInPreRun(root, cmd *cobra.Command) bool {
	if cmd == nil || cmd == root {
		return false
	}
	if cmd.Name() == "help" {
		return false
	}
	if f := cmd.Flags().Lookup("help"); f != nil && f.Changed {
		return false
	}
	return true
}
