package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/docs"
)

func newDocsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Print shell3 documentation",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(docs.Content)
		},
	}
}
