package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/scaffold"
)

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init [git-url]",
		Short: "Initialize .shell3/ project config",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("git init not yet supported — coming soon")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("get home directory: %w", err)
			}
			return scaffold.InitProject(cwd, homeDir)
		},
	}
}
