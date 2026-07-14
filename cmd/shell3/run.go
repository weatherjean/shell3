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

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/shell3"
)

type runFlags struct {
	configPath string
	outPath    string
	agent      string
	prompt     string
	resume     string
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "run [message]",
		Short: "Run shell3 headlessly (prompt, subagent, or resume)",
		Example: `  shell3 run "fix the failing test"
  shell3 run --prompt "summarize the diff" --out audit.jsonl
  git diff | shell3 run "review this change"
  shell3 run --resume 20260701T120000.000000000-abcd "continue"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			input := strings.TrimSpace(f.prompt)
			if input == "" {
				input = strings.TrimSpace(strings.Join(args, " "))
			}
			if input == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				input = strings.TrimSpace(string(b))
			}
			return runHeadless(cmd.Context(), f, input)
		},
	}
	addConfigAgentFlags(cmd, &f.configPath, &f.agent,
		"Select the active agent by name (default: first declared). May also name a registered subagent")
	addResumeFlag(cmd, &f.resume)
	cmd.Flags().StringVar(&f.outPath, "out", "", "Stream a JSONL audit log of this run to <path>")
	cmd.Flags().StringVar(&f.prompt, "prompt", "", "The prompt for this run (alternative to positional args / stdin)")
	return cmd
}

func runHeadless(ctx context.Context, f *runFlags, input string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// These env vars are read by external hook subprocesses, not by internal/shell3.
	_ = os.Setenv("SHELL3_HEADLESS", "1")
	if f.outPath != "" {
		_ = os.Setenv("SHELL3_OUT", f.outPath)
	}

	resumedCfg, err := resolveResumeConfig(f.resume, f.configPath)
	if err != nil {
		return err
	}

	spec := shell3.Spec{
		Prompt:      input,
		ConfigPath:  resumedCfg,
		WorkDir:     cwd,
		Agent:       f.agent,
		Interactive: false,
		OutPath:     f.outPath,
		ResumeID:    f.resume,
	}
	return cli.RunOnce(ctx, spec)
}
