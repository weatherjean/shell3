//go:build unix

package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	acpfe "github.com/weatherjean/shell3/internal/acp"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/pkg/shell3"
)

func newAcpCommand() *cobra.Command {
	var configPath, agent string
	cmd := &cobra.Command{
		Use:   "acp",
		Short: "Serve the Agent Client Protocol (ACP) over stdio",
		Long: "Runs shell3 as an ACP agent: JSON-RPC 2.0 over stdin/stdout for editors " +
			"(Zed, ...) and bridges (OpenACP). All logs go to the app log; stdout carries " +
			"only protocol messages.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := shell3.NewRuntime(cmd.Context(), shell3.RuntimeSpec{
				ConfigPath: configPath,
			})
			if err != nil {
				return err
			}
			defer rt.Close()
			return acpfe.Run(cmd.Context(), rt, os.Stdin, os.Stdout, acpfe.Options{
				DefaultAgent: agent,
				Logger:       applogSlog(),
			})
		},
	}
	addConfigAgentFlags(cmd, &configPath, &agent,
		"Initial agent for new sessions (default: first declared)")
	return cmd
}

// applogSlog returns a *slog.Logger that writes to the app log file.
// It uses paths.NewGlobal + applog.OpenFile to locate and open the rotating
// log. If the log file cannot be opened (e.g. home dir unavailable), it falls
// back to stderr — stdout is never used.
//
// The rotation bounds MUST be applog's shared defaults: agentsetup (via
// NewRuntime) opens the same path, and mismatched bounds would let one opener
// rotate the file out from under the other's descriptor.
func applogSlog() *slog.Logger {
	home, err := os.UserHomeDir()
	if err == nil {
		p := paths.NewGlobal(home)
		if f, ferr := applog.OpenFile(p.LogFile, applog.DefaultMaxBytes, applog.DefaultMaxArchives); ferr == nil {
			return slog.New(slog.NewTextHandler(f, nil))
		}
	}
	// Fallback: stderr (never stdout, as stdout carries ACP protocol messages).
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}
