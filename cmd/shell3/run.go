//go:build unix

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/tui"
	"github.com/weatherjean/shell3/pkg/shell3"
)

type runFlags struct {
	configPath     string
	outPath        string
	agent          string
	appendSinkFile string
	id             string
	noSubagents    bool
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
	cmd.Flags().StringVar(&f.agent, "agent", "", "Select the active agent by name (default: first declared). May also name a registered subagent.")
	cmd.Flags().StringVar(&f.appendSinkFile, "append-sinkfile", "", "On completion, append one agent_done notification to this sink file (subagent self-report).")
	cmd.Flags().StringVar(&f.id, "id", "", "Caller-chosen id stamped into the agent_done notification (used with --append-sinkfile).")
	cmd.Flags().BoolVar(&f.noSubagents, "no-subagents", false, "Suppress the delegation context so this run cannot spawn subagents (depth limit 1).")
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

	// When self-reporting to a sink (a subagent invocation) without an explicit
	// --id, mint one so the agent_done notification still carries a stable id the
	// parent can correlate with the transcript.
	id := f.id
	if f.appendSinkFile != "" && id == "" {
		id = fmt.Sprintf("a%d", time.Now().UnixNano())
	}

	// pkg/shell3 (via internal/tui) owns config assembly and teardown now; cmd
	// just builds the Spec and dispatches. Interactive is the inverse of
	// headless, mirroring how agentsetup.Options.Headless was computed before.
	spec := shell3.Spec{
		ConfigPath:     f.configPath,
		WorkDir:        cwd,
		Agent:          f.agent,
		Interactive:    !headless,
		OutPath:        f.outPath,
		NoSubagents:    f.noSubagents,
		AppendSinkFile: f.appendSinkFile,
		ID:             id,
	}

	if initialInput != "" {
		spec.Prompt = initialInput
		return tui.RunOnce(ctx, spec)
	}
	return tui.RunInteractive(ctx, spec)
}
