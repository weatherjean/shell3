//go:build unix

package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestShouldPrintHeaderInPreRun(t *testing.T) {
	root := &cobra.Command{Use: "shell3"}
	sub := &cobra.Command{Use: "init"}
	root.AddCommand(sub)

	tests := []struct {
		name string
		cmd  *cobra.Command
		prep func(*cobra.Command)
		want bool
	}{
		{
			name: "root command does not print",
			cmd:  root,
			want: false,
		},
		{
			name: "normal subcommand prints",
			cmd:  sub,
			want: true,
		},
		{
			name: "subcommand help flag suppresses pre-run header",
			cmd:  sub,
			prep: func(c *cobra.Command) {
				c.InitDefaultHelpFlag()
				_ = c.Flags().Set("help", "true")
			},
			want: false,
		},
		{
			name: "help command suppresses pre-run header",
			cmd:  &cobra.Command{Use: "help"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prep != nil {
				tt.prep(tt.cmd)
			}
			got := shouldPrintHeaderInPreRun(root, tt.cmd)
			if got != tt.want {
				t.Fatalf("shouldPrintHeaderInPreRun() = %v, want %v", got, tt.want)
			}
		})
	}
}
