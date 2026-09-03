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
			name: "root command prints",
			cmd:  root,
			want: true,
		},
		{
			name: "normal subcommand prints",
			cmd:  sub,
			want: true,
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
			got := shouldPrintHeaderInPreRun(tt.cmd)
			if got != tt.want {
				t.Fatalf("shouldPrintHeaderInPreRun() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWantsHelp(t *testing.T) {
	if wantsHelp(nil) {
		t.Fatal("bare shell3 runs the console; it must not be classified as help")
	}
	if !wantsHelp([]string{"--help"}) || !wantsHelp([]string{"help", "wrk"}) {
		t.Fatal("explicit help invocation was not classified as help")
	}
}
