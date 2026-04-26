package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

func newAuthCommand() *cobra.Command {
	var providerFlag string
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Configure LLM provider credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			// If a registered Provider matches the flag, run its own auth flow
			// (e.g. OAuth). Otherwise fall back to the API-key prompt.
			if providerFlag != "" {
				if p, ok := llm.Get(providerFlag); ok {
					return p.Auth(cmd.Context(), os.Stdout)
				}
			}
			homeDir, _ := os.UserHomeDir()
			return config.RunAuthInteractive(homeDir, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&providerFlag, "provider", "", "Named provider with a registered auth flow (e.g. codex)")
	return cmd
}
