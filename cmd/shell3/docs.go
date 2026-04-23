package main

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed shell3.md
var docsContent string

func newDocsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Print shell3 documentation",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(docsContent)
		},
	}
}
