//go:build unix

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/tui"
	"github.com/weatherjean/shell3/pkg/shell3"
)

type runFlags struct {
	configPath string
	outPath    string
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the shell3 chat agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			input := strings.TrimSpace(strings.Join(args, " "))
			if input == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				input = strings.TrimSpace(string(b))
			}
			return runChat(cmd.Context(), f, input)
		},
	}
	cmd.Flags().StringVarP(&f.configPath, "config", "c", "", "Path to shell3.lua (default: ./shell3.lua, else ~/.shell3/shell3.lua)")
	cmd.Flags().StringVar(&f.outPath, "out", "", "Stream a JSONL audit log of this run to <path>. Enables headless mode.")
	return cmd
}

func runChat(ctx context.Context, f *runFlags, initialInput string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	headless := f.outPath != "" || (!term.IsTerminal(int(os.Stdin.Fd())) && initialInput != "")
	// These env vars are consumed by external hook subprocesses (not by
	// pkg/shell3, which never mutates global env). Preserve them exactly.
	if headless {
		_ = os.Setenv("SHELL3_HEADLESS", "1")
		if f.outPath != "" {
			_ = os.Setenv("SHELL3_OUT", f.outPath)
		}
	}

	// pkg/shell3 (via internal/tui) owns config assembly and teardown now; cmd
	// just builds the Spec and dispatches. Interactive is the inverse of
	// headless, mirroring how agentsetup.Options.Headless was computed before.
	spec := shell3.Spec{
		ConfigPath:  f.configPath,
		WorkDir:     cwd,
		Interactive: !headless,
		OutPath:     f.outPath,
	}

	if initialInput != "" {
		spec.Prompt = initialInput
		return tui.RunOnce(ctx, spec)
	}
	return tui.RunInteractive(ctx, spec)
}
