//go:build unix

package main

import (
	"context"
	"os"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/cli"
)

// version is the build version, set at link time via -X main.version (the
// Makefile derives it from the latest git tag); "dev" for a plain go build.
var version = "dev"

// main wires the cobra command tree and executes it through fang, which owns
// help, usage, error, and --version styling.
func main() {
	root := newRootCommand()

	// Print the brand header (the ๑ï snail): the full two-line banner when a
	// subcommand actually runs (PersistentPreRun), and the slim one-line logo
	// above help pages. fang owns the help func outright — and must keep
	// owning the out writer, since it sniffs it for terminal color support —
	// so help invocations are detected up front from the raw args instead.
	// Both are TTY-only.
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if tty && shouldPrintHeaderInPreRun(cmd) {
			cli.PrintHeader(os.Stdout)
		}
	}
	if tty && wantsHelp(os.Args[1:]) {
		cli.PrintLogo(os.Stdout)
	}

	// fang prints the styled error itself; the returned error only signals exit.
	if err := fang.Execute(context.Background(), root,
		fang.WithVersion(version),
		fang.WithColorSchemeFunc(cli.FangColorScheme),
	); err != nil {
		os.Exit(1)
	}
}

// wantsHelp reports whether the invocation renders a help page: the help
// subcommand or a -h/--help token before a "--" terminator.
// A deliberate approximation of pflag's grammar: a literal "--help" passed as
// a flag VALUE (e.g. dev -p "--help") false-positives an extra logo line —
// harmless, and far cheaper than re-parsing flags or wrapping fang's output
// stream (which breaks its color detection).
func wantsHelp(args []string) bool {
	if len(args) > 0 && args[0] == "help" {
		return true
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// shouldPrintHeaderInPreRun gates the full banner to real command runs. The
// help command renders through the logo path instead.
// (-h/--help never reaches PersistentPreRun — cobra short-circuits to the
// help func first.)
func shouldPrintHeaderInPreRun(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Name() != "help"
}
