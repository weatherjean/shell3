package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "shell3",
		Short: "Minimal Unix-composable coding agent",
	}

	runCmd := newRunCommand()
	root.RunE = runCmd.RunE
	root.Flags().AddFlagSet(runCmd.Flags())

	root.AddCommand(newInitCommand())
	root.AddCommand(newAuthCommand())
	root.AddCommand(newDocsCommand())
	root.AddCommand(newDestroyCommand())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
