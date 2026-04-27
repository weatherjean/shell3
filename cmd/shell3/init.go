package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/llm"
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
			if err := scaffold.InitProject(cwd, homeDir); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Available adapters:")
			names := llm.Registered()
			sort.Strings(names)
			for _, name := range names {
				p, _ := llm.Get(name)
				marker := ""
				if p.SingleInstance() {
					marker = " (single-instance)"
				}
				fmt.Fprintf(out, "  - %s%s\n", name, marker)
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Next: run `shell3 auth` to configure credentials.")
			return nil
		},
	}
}
