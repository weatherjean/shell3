//go:build unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/shell3"
)

// errAskAborted is returned by askPrompt when the user hits ctrl+c at the huh
// input. The loop matches on it to exit cleanly (vs. a real error).
var errAskAborted = errors.New("ask: aborted")

// askResumeHint tells the user how to pick the conversation back up.
func askResumeHint() string { return "↩ continue this conversation: shell3 ask --resume" }

// newAskCommand builds `shell3 ask` — a local driver for the same config +
// agent the bot runs, printing full verbose output (reply, every tool
// call + args, untruncated tool results, reasoning, token usage). It exists to
// drive and polish the agent without a Telegram chat; it is also handy
// for quick local queries and troubleshooting. With no message it opens an
// interactive multi-turn chat in this terminal; --resume continues the latest
// session so successive invocations form one conversation.
func newAskCommand() *cobra.Command {
	var (
		configDir  string
		promptFlag string
		resume     bool
		agentFlag  string
	)
	cmd := &cobra.Command{
		Use:   "ask [message]",
		Short: "Ask the agent locally with full verbose output",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")
			if prompt == "" {
				prompt = promptFlag
			} else if promptFlag != "" {
				return fmt.Errorf("ask: give the message either as an argument or via -p, not both")
			}
			// --agent is a scripted single shot: its stdout is the agent's
			// reply and nothing else, so there is no interactive form to fall
			// back to and no conversation to resume (each run dispatches a
			// fresh child session).
			if agentFlag != "" {
				if prompt == "" {
					return fmt.Errorf(`ask: --agent needs a message, e.g. shell3 ask --agent %s -p "draft for acme.co.uk"`, agentFlag)
				}
				if resume {
					return fmt.Errorf("ask: --agent runs a fresh subagent job, so it cannot --resume")
				}
			}
			// interactive: no message given. The first prompt is read below;
			// each completed turn then reads another.
			interactive := prompt == ""
			if interactive {
				// No message given: ask for one interactively (headless runs
				// must pass it, e.g. shell3 ask -p "list the files here").
				var err error
				if prompt, err = askPrompt(); err != nil {
					if errors.Is(err, errAskAborted) {
						return nil // ctrl+c before anything ran: no hint
					}
					return err
				}
				echoPrompt(os.Stdout, prompt)
			}
			ctx := cmd.Context()

			resolved, err := resolveConfig(configDir)
			if err != nil {
				return err
			}
			// Anchor the runtime to the config dir, exactly like `shell3 telegram`,
			// so ask shares the bot's runs store + workdir and sees the same state.
			rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigDir: resolved, WorkDir: resolved})
			if err != nil {
				return err
			}
			defer rt.Close()

			// Sessions run in the config dir (the runtime root), the same
			// default the hosts use. ask is host-agnostic: it reads nothing
			// from the telegram block. Headless when scripted (-p) or without
			// a TTY on both ends — the hook payload's headless flag says so.
			// --agent counts as scripted however the message arrived (-p or
			// argv): there is no human in its turn, so the hook payload must
			// say headless even when it was launched from a terminal.
			headless := !interactiveTTY(promptFlag != "" || agentFlag != "", term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stderr.Fd())))
			sess, err := rt.Session(shell3.SessionOpts{
				Name:         "ask",
				WorkDir:      resolved,
				ResumeLatest: resume,
				Headless:     headless,
			})
			if err != nil {
				return err
			}

			// --agent: dispatch the named subagent and print only its reply.
			// This returns before the interactive loop below — a scripted run
			// has no second turn to read.
			if agentFlag != "" {
				fmt.Fprintf(os.Stderr, "config=%s\n", resolved)
				return runAskAgent(ctx, os.Stdout, os.Stderr, sess, agentFlag, prompt)
			}

			// The brand banner already printed from the root PersistentPreRun.
			fmt.Printf("agent=%s  config=%s\n\n", sess.ActiveAgent(), resolved)

			// One turn per iteration, all on the SAME session. In interactive
			// mode each completed turn reads another message, so the process
			// becomes a real multi-turn local chat until ctrl+c.
			for {
				if err := cli.RunAskTurn(ctx, os.Stdout, sess, prompt); err != nil {
					return err
				}
				// Follow through on any subagent/bash_bg jobs the turn spawned, so
				// ask shows their results the way the bot's wake loop would.
				// This blocks until those in-process jobs complete (SIGINT to quit):
				// a -p run must never exit at turn end and silently kill in-flight work.
				if err := cli.FollowAskJobs(ctx, os.Stdout, rt, sess); err != nil {
					return err
				}
				if !interactive {
					break
				}
				next, err := askPrompt()
				if err != nil {
					if errors.Is(err, errAskAborted) {
						break // ctrl+c: clean exit, resume hint below
					}
					return err
				}
				prompt = next
				fmt.Println()
				echoPrompt(os.Stdout, prompt)
			}
			fmt.Println("\n" + askResumeHint())
			return nil
		},
	}
	addConfigFlag(cmd, &configDir)
	cmd.Flags().StringVarP(&promptFlag, "prompt", "p", "", "Message for the agent (skips the interactive prompt)")
	cmd.Flags().BoolVar(&resume, "resume", false, "Continue the latest session (multi-turn across invocations)")
	cmd.Flags().StringVar(&agentFlag, "agent", "", "Run one headless turn of this subagent and print only its reply (for scripts)")
	return cmd
}

// interactiveTTY reports whether `ask` has a human plausibly at the terminal:
// not scripted (-p) and a TTY on both stdin and stderr. Its inverse is the
// session's Headless flag, which the tool-call hook payload sees as
// .headless. Pure so the wiring is unit-testable without a real terminal.
func interactiveTTY(scripted, stdinTTY, stderrTTY bool) bool {
	return !scripted && stdinTTY && stderrTTY
}

// echoPrompt writes back the message the interactive huh form just collected.
// The form clears its own input line on submit, so without this the user's
// own message never appears anywhere in the terminal transcript — only the
// interactive TTY path needs it (askPrompt's caller): argv/-p mode is
// unchanged because the shell already echoed the command line.
func echoPrompt(w io.Writer, msg string) {
	fmt.Fprintln(w, cli.Meta("you › ")+msg)
}

// askPrompt asks for the message with a brand-themed huh input when no argument
// or -p was given. It returns errAskAborted when the user hits ctrl+c so the
// caller can exit cleanly. Headless invocations (no TTY) get an error pointing
// at -p instead.
func askPrompt() (string, error) {
	// Both ends must be a terminal: the form reads keys from stdin and renders
	// its TUI to stdout (a piped stdout would capture control codes). A
	// zero-size terminal (degenerate PTYs: CI, expect without stty) counts as
	// no terminal — bubbletea's layout panics at width 0.
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return "", fmt.Errorf(`ask: no message and no terminal — pass one, e.g. shell3 ask -p "list the files here"`)
	}
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err != nil || w <= 0 || h <= 0 {
		return "", fmt.Errorf(`ask: terminal reports no size — pass the message instead, e.g. shell3 ask -p "list the files here"`)
	}
	var prompt string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Message").
			Placeholder("what should the agent do?").
			Description("enter to send · ctrl+c to exit").
			Validate(huh.ValidateNotEmpty()).
			Value(&prompt),
	)).WithTheme(cli.HuhTheme())
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", errAskAborted
		}
		return "", fmt.Errorf("ask: %w", err)
	}
	return prompt, nil
}
