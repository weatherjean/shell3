//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/scaffold"
)

func newBootCommand() *cobra.Command {
	var here bool
	cmd := &cobra.Command{
		Use:   "boot [shell3.lisp]",
		Short: "Write one complete single-file shell3 kit",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if here && len(args) == 1 {
				return errors.New("boot: --here cannot be combined with an explicit path")
			}
			var path string
			switch {
			case len(args) == 1:
				path = args[0]
			case here:
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("boot: get working directory: %w", err)
				}
				path = filepath.Join(cwd, "shell3.lisp")
			default:
				defaults, err := defaultRuntimePaths()
				if err != nil {
					return err
				}
				path = defaults.config
			}
			path, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("boot: resolve path: %w", err)
			}
			if _, err := lispconfig.Parse(path, []byte(scaffold.Kit)); err != nil {
				return fmt.Errorf("boot: embedded kit is invalid: %w", err)
			}
			if err := scaffold.WriteKit(path); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\nedit the model block and export its api-key-env before starting shell3\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&here, "here", false, "Write ./shell3.lisp instead of the global default")
	return cmd
}
