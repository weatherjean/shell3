//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/media"
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
		configDir  string
		promptFlag string
		resume     bool
		hbFire     bool
	)
	cmd := &cobra.Command{
		Use:   "dev [message]",
		Short: "Drive the bot's agent locally with full verbose output (dev/troubleshooting)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")
			if prompt == "" {
				prompt = promptFlag
			} else if promptFlag != "" {
				return fmt.Errorf("dev: give the message either as an argument or via -p, not both")
			}
			if prompt != "" && hbFire {
				return fmt.Errorf("dev: --heartbeat fires the configured heartbeat prompt; drop the message")
			}
			if prompt == "" && !hbFire {
				// No message given: ask for one interactively (headless runs
				// must pass it, e.g. shell3 dev -p "list the files here").
				var err error
				if prompt, err = askDevPrompt(); err != nil {
					return err
				}
			}
			ctx := cmd.Context()

			resolved, err := resolveConfig(configDir)
			if err != nil {
				return err
			}
			// Anchor the runtime to the config dir, exactly like `shell3 telegram`,
			// so dev shares the bot's runs store + workdir and sees the same state.
			rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigDir: resolved, WorkDir: resolved})
			if err != nil {
				return err
			}
			defer rt.Close()

			// Use the Telegram host's workdir so dev exercises the exact
			// configuration the bot runs.
			tg := rt.Telegram()
			sess, err := rt.Session(shell3.SessionOpts{
				Name:         "dev",
				WorkDir:      tg.WorkDir,
				ResumeLatest: resume,
				// A human is at the terminal: auto-approve tool-call hook ask verdicts
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

			// Register the image_generate host tool on this session and any
			// subagent children it spawns (a no-op when no shell3.imagegen{} is
			// declared) so dev drives the same tool set the Telegram and web
			// hosts run. No SetMedia equivalent: dev is text-only (no inbound
			// voice notes or photos to transcribe/describe).
			rt.SetSessionDecorator(func(s *shell3.Session) {
				_ = media.RegisterImageTool(s, buildMediaClients(rt))
			})

			// The brand banner already printed from the root PersistentPreRun.
			fmt.Printf("agent=%s  config=%s\n\n", sess.ActiveAgent(), resolved)
			if hbFire {
				// Fire the configured heartbeat once, exactly as the Telegram
				// host's ticker would, and show the suppression verdict.
				hb := rt.HeartbeatConfig()
				if hb == nil {
					return fmt.Errorf("dev: no shell3.heartbeat{} declared in %s", resolved)
				}
				if err := cli.RunDevHeartbeat(ctx, os.Stdout, sess, *hb); err != nil {
					return err
				}
			} else if err := cli.RunDevTurn(ctx, os.Stdout, sess, prompt); err != nil {
				return err
			}
			// Follow through on any subagent/bash_bg jobs the turn spawned, so dev
			// shows their results the way the Telegram host's wake loop would.
			return cli.FollowDevJobs(ctx, os.Stdout, rt, sess, 3*time.Minute)
		},
	}
	addConfigFlag(cmd, &configDir)
	cmd.Flags().StringVarP(&promptFlag, "prompt", "p", "", "Message for the agent (skips the interactive prompt)")
	cmd.Flags().BoolVar(&resume, "resume", false, "Continue the latest session (multi-turn across invocations)")
	cmd.Flags().BoolVar(&hbFire, "heartbeat", false, "Fire the configured shell3.heartbeat{} prompt once and show the suppression verdict")
	return cmd
}

// askDevPrompt asks for the dev message with a brand-themed huh input when no
// argument or -p was given. Headless invocations get a pointer to -p instead.
func askDevPrompt() (string, error) {
	// Both ends must be a terminal: the form reads keys from stdin and renders
	// its TUI to stdout (a piped stdout would capture control codes).
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return "", fmt.Errorf(`dev: no message and no terminal — pass one, e.g. shell3 dev -p "list the files here"`)
	}
	var prompt string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Message").
			Placeholder("what should the agent do?").
			Validate(huh.ValidateNotEmpty()).
			Value(&prompt),
	)).WithTheme(cli.HuhTheme())
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", fmt.Errorf("dev: aborted")
		}
		return "", fmt.Errorf("dev: %w", err)
	}
	return prompt, nil
}
