//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/tui"
	"github.com/weatherjean/shell3/pkg/shell3"
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

	var (
		rootResume     int64
		rootConfigPath string
		rootAgent      string
	)
	root.Args = cobra.ArbitraryArgs
	root.Flags().Int64Var(&rootResume, "resume", 0, "Resume a stored session by id in the interactive TUI.")
	root.Flags().StringVarP(&rootConfigPath, "config", "c", "", "Path to shell3.lua (default: ./shell3.lua, else ~/.shell3/shell3.lua)")
	root.Flags().StringVar(&rootAgent, "agent", "", "Select the active agent by name (default: first declared).")
	root.RunE = func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		spec := shell3.Spec{
			ConfigPath:  rootConfigPath,
			WorkDir:     cwd,
			Agent:       rootAgent,
			Interactive: true,
			ResumeID:    rootResume,
		}
		return tui.RunInteractive(cmd.Context(), spec)
	}
	root.AddCommand(newRunCommand())
	root.AddCommand(newBootCommand())
	root.AddCommand(newTelegramCommand())

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
