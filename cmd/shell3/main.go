//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/tui"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// version is the build version, set at link time via -X main.version (the
// Makefile derives it from the latest git tag); "dev" for a plain go build.
var version = "dev"

// main wires the cobra command tree (interactive root + run/boot/
// read-session/acp subcommands) and executes it.
func main() {
	root := &cobra.Command{
		Use:     "shell3",
		Short:   "Minimal Unix-composable coding agent",
		Version: version,
	}

	var (
		rootResume     string
		rootConfigPath string
		rootAgent      string
	)
	// NoArgs: a typo'd subcommand or a bare prompt ("shell3 fix this bug")
	// must error, not silently open the interactive TUI and discard the input
	// (use `shell3 run "..."` for one-shot prompts).
	root.Args = cobra.NoArgs
	addConfigAgentFlags(root, &rootConfigPath, &rootAgent, "")
	addResumeFlag(root, &rootResume)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		// A resume without --config runs under the session's recorded config,
		// matching `run --resume` (explicit --config always wins).
		configPath, err := resolveResumeConfig(rootResume, rootConfigPath)
		if err != nil {
			return err
		}
		spec := shell3.Spec{
			ConfigPath:  configPath,
			WorkDir:     cwd,
			Agent:       rootAgent,
			Interactive: true,
			ResumeID:    rootResume,
		}
		return tui.RunInteractive(cmd.Context(), spec)
	}
	root.AddCommand(newRunCommand())
	root.AddCommand(newBootCommand())
	root.AddCommand(newReadSessionCommand())
	root.AddCommand(newAcpCommand())

	// Print the brand header for subcommands and --help (TTY only). Root chat
	// suppresses it — it renders its own banner.
	maybeHeader := func() {
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return
		}
		tui.PrintHeader(os.Stdout)
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
