//go:build unix

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/media"
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
			// Anchor the runtime to the config dir, exactly like `shell3 serve`,
			// so ask shares the bot's runs store + workdir and sees the same state.
			rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigDir: resolved, WorkDir: resolved})
			if err != nil {
				return err
			}
			defer rt.Close()

			// A tool-call hook ask verdict needs a human to decide. Attach an
			// interactive y/n terminal prompt only when a human is plausibly present
			// (no -p, and a TTY on both stdin — to read the answer — and stderr — to
			// show the prompt). With -p (scripted) or no TTY there is no human, so
			// attach NO asker: the Session treats the ask as headless and auto-DENIES
			// (AGENTS.md hooks contract), and the hook payload's headless flag is true.
			var asker func(context.Context, string, string) bool
			if interactiveAsk(promptFlag != "", term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stderr.Fd()))) {
				asker = func(ctx context.Context, command, reason string) bool {
					return confirmAsk(ctx, os.Stdin, os.Stderr, command, reason)
				}
			}

			// Sessions run in the config dir (the runtime root), the same
			// default the hosts use. ask is host-agnostic: it reads nothing
			// from the web block.
			sess, err := rt.Session(shell3.SessionOpts{
				Name:         "ask",
				WorkDir:      resolved,
				ResumeLatest: resume,
				Asker:        asker,
			})
			if err != nil {
				return err
			}

			// Register the image_generate host tool on this session and any
			// subagent children it spawns (a no-op when no media.imagegen: is
			// declared) so ask drives the same tool set the web
			// hosts run. No SetMedia equivalent: ask is text-only (no inbound
			// voice notes or photos to transcribe/describe).
			rt.SetSessionDecorator(func(s *shell3.Session) {
				_ = media.RegisterImageTool(s, buildMediaClients(rt))
			})

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
				// ask shows their results the way the web host's wake loop would.
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
	return cmd
}

// interactiveAsk reports whether `ask` should attach an interactive terminal
// confirm prompt for tool-call hook ask verdicts. It returns false — meaning no
// asker, so the Session auto-denies the ask headlessly — with -p (scripted) or
// without a TTY on both ends (piped stdin, no human to prompt). Pure so the
// wiring is unit-testable without a real runtime or terminal.
func interactiveAsk(scripted, stdinTTY, stderrTTY bool) bool {
	return !scripted && stdinTTY && stderrTTY
}

// confirmAsk prompts a human at the terminal to allow or deny a tool-call hook
// ask verdict, showing the reason and command so the decision is informed, then
// reading a y/n line from r (the prompt is written to w). It replaces ask's old
// blind auto-approve: anything but an explicit yes — including EOF / no input —
// denies. r and w are injectable so the prompt is testable; the command wires
// os.Stdin and os.Stderr (verbose turn output owns stdout).
func confirmAsk(_ context.Context, r io.Reader, w io.Writer, command, reason string) bool {
	fmt.Fprintf(w, "\n[hook ask] %s\n", reason)
	if command != "" {
		fmt.Fprintf(w, "  command: %s\n", command)
	}
	fmt.Fprint(w, "  allow this tool call? [y/N] ")
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
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
