package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/config"
)

func newAuthCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Configure LLM provider credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := os.UserHomeDir()
			return config.RunAuthInteractive(homeDir, os.Stdin, os.Stdout)
		},
	}
}
