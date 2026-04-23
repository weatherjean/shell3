package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newDestroyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Remove .shell3/ config from current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			shell3Dir := filepath.Join(cwd, ".shell3")

			if _, err := os.Stat(shell3Dir); os.IsNotExist(err) {
				return fmt.Errorf("no .shell3/ found in %s", cwd)
			}

			fmt.Printf("This will permanently delete: %s\n", shell3Dir)
			fmt.Print("Type 'destroy' to confirm: ")

			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "destroy" {
				fmt.Println("Aborted.")
				return nil
			}

			if err := os.RemoveAll(shell3Dir); err != nil {
				return fmt.Errorf("destroy: %w", err)
			}
			fmt.Printf("Destroyed %s\n", shell3Dir)
			return nil
		},
	}
}
