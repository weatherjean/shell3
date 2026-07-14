//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/cli"
)

// version is the build version, set at link time via -X main.version (the
// Makefile derives it from the latest git tag); "dev" for a plain go build.
var version = "dev"

// main wires the cobra command tree (telegram + boot subcommands; the bare
// root prints help) and executes it.
func main() {
	root := &cobra.Command{
		Use:     "shell3",
		Short:   "Minimal Unix-composable coding agent",
		Version: version,
	}

	// NoArgs: a typo'd subcommand or a bare prompt ("shell3 fix this bug") must
	// error rather than be silently swallowed. shell3 is a Telegram-first hosted
	// agent: the bare command prints help; `shell3 telegram` runs the service,
	// and `shell3 dev "..."` handles one-shot prompts.
	root.Args = cobra.NoArgs
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	}
	root.AddCommand(newTelegramCommand())
	root.AddCommand(newDevCommand())
	root.AddCommand(newDashCommand())
	root.AddCommand(newBootCommand())

	// Print the brand header for subcommands and --help (TTY only). Root chat
	// suppresses it — it renders its own banner.
	maybeHeader := func() {
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return
		}
		cli.PrintHeader(os.Stdout)
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
