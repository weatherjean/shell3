//go:build unix

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/shell3"
)

// newDevCommand builds `shell3 dev` — a local one-shot driver for the same
// config + agent the Telegram bot runs, printing full verbose output (reply,
// every tool call + args, untruncated tool results, reasoning, token usage).
// It exists to drive and polish the agent without going through Telegram; it is
// also handy for quick local queries and troubleshooting. --resume continues
// the latest session so successive invocations form one conversation.
func newDevCommand() *cobra.Command {
	var (
		configPath string
		agent      string
		resume     bool
	)
	cmd := &cobra.Command{
		Use:   "dev [message]",
		Short: "Drive the bot's agent locally with full verbose output (dev/troubleshooting)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")
			if prompt == "" {
				return fmt.Errorf("dev: give a message, e.g. shell3 dev \"list the files here\"")
			}
			ctx := cmd.Context()

			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			resolved, err := agentsetup.ResolveConfigPath(configPath, home)
			if err != nil {
				return err
			}
			// Anchor the runtime to the config dir, exactly like `shell3 telegram`,
			// so dev shares the bot's runs store + workdir and sees the same state.
			rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigPath: resolved, WorkDir: filepath.Dir(resolved)})
			if err != nil {
				return err
			}
			defer rt.Close()

			// Default to the Telegram host's agent + workdir so dev exercises the
			// exact configuration the bot runs. --agent overrides.
			tg := rt.Telegram()
			if agent == "" {
				agent = tg.Agent
			}
			sess, err := rt.Session(shell3.SessionOpts{
				Name:         "dev",
				Agent:        agent,
				WorkDir:      tg.WorkDir,
				ResumeLatest: resume,
				// A human is at the terminal: auto-approve on_tool_call ask verdicts
				// (and say so) so dev runs unattended but stays transparent about
				// what a gate would have prompted.
				Asker: func(_ context.Context, command, reason string) bool {
					fmt.Fprintf(os.Stderr, "[dev auto-approved ask: %s] %s\n", reason, command)
					return true
				},
			})
			if err != nil {
				return err
			}

			cli.PrintHeader(os.Stdout)
			fmt.Printf("agent=%s  config=%s\n\n", sess.ActiveAgent(), resolved)
			if err := cli.RunDevTurn(ctx, os.Stdout, sess, prompt); err != nil {
				return err
			}
			// Follow through on any subagent/bash_bg jobs the turn spawned, so dev
			// shows their results the way the Telegram host's wake loop would.
			return cli.FollowDevJobs(ctx, os.Stdout, rt, sess, 3*time.Minute)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Config name (→ ~/.shell3/<name>.lua) or path to a *.lua file")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent to run (default: the shell3.telegram{} agent)")
	cmd.Flags().BoolVar(&resume, "resume", false, "Continue the latest session (multi-turn across invocations)")
	return cmd
}
