//go:build unix

package main

import (
	"context"
	"os"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/cli"
)

// version is the build version, set at link time via -X main.version (the
// Makefile derives it from the latest git tag); "dev" for a plain go build.
var version = "dev"

// main wires the cobra command tree (telegram, web, dev, dash, boot, health;
// the bare root prints help) and executes it through fang, which owns help,
// usage, error, and --version styling.
func main() {
	root := &cobra.Command{
		Use:   "shell3",
		Short: "Minimal Unix-composable personal agent",
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
	root.AddCommand(newWebCommand())
	root.AddCommand(newDevCommand())
	root.AddCommand(newDashCommand())
	root.AddCommand(newBootCommand())
	root.AddCommand(newHealthCommand())

	// Print the brand header (the ๑ï snail): the full two-line banner when a
	// subcommand actually runs (PersistentPreRun), and the slim one-line logo
	// above help pages. fang owns the help func outright — and must keep
	// owning the out writer, since it sniffs it for terminal color support —
	// so help invocations are detected up front from the raw args instead.
	// Both are TTY-only.
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if tty && shouldPrintHeaderInPreRun(root, cmd) {
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

// wantsHelp reports whether the invocation renders a help page: the bare
// root, the help subcommand, or a -h/--help token before a "--" terminator.
// A deliberate approximation of pflag's grammar: a literal "--help" passed as
// a flag VALUE (e.g. dev -p "--help") false-positives an extra logo line —
// harmless, and far cheaper than re-parsing flags or wrapping fang's output
// stream (which breaks its color detection).
func wantsHelp(args []string) bool {
	if len(args) == 0 {
		return true
	}
	if args[0] == "help" {
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

// addConfigFlag registers the shared --config/-c flag with the one canonical
// description; every subcommand resolves it through resolveConfig.
func addConfigFlag(cmd *cobra.Command, configDir *string) {
	cmd.Flags().StringVarP(configDir, "config", "c", "", "Path to the config directory containing shell3.yaml (default ~/.shell3)")
}

// resolveConfig turns the shared --config flag value (a directory path; "" for
// the default ~/.shell3) into the config directory every subcommand loads.
func resolveConfig(configDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return agentsetup.ResolveConfigDir(configDir, home)
}

// shouldPrintHeaderInPreRun gates the full banner to real subcommand runs:
// the bare root and the help command render through the logo path instead.
// (-h/--help never reaches PersistentPreRun — cobra short-circuits to the
// help func first.)
func shouldPrintHeaderInPreRun(root, cmd *cobra.Command) bool {
	return cmd != nil && cmd != root && cmd.Name() != "help"
}
