//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/cli"
)

// version is the build version, set at link time via -X main.version (the
// Makefile derives it from the latest git tag); "dev" for a plain go build.
var version = "dev"

// main wires the cobra command tree (telegram, dev, dash, boot, health; the
// bare root prints help) and executes it.
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
	root.AddCommand(newHealthCommand())

	// Print the brand header for subcommands and --help (TTY only).
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

// addConfigFlag registers the shared --config/-c flag with the one canonical
// description; every subcommand resolves it through resolveConfig.
func addConfigFlag(cmd *cobra.Command, configPath *string) {
	cmd.Flags().StringVarP(configPath, "config", "c", "", "Config name (→ ~/.shell3/<name>.lua) or path to a *.lua file")
}

// resolveConfig turns the shared --config flag value (a name or a *.lua path;
// "" for the default ~/.shell3/shell3.lua) into the config path every
// subcommand loads.
func resolveConfig(configPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return agentsetup.ResolveConfigPath(configPath, home)
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
