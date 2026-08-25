//go:build unix

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/askui"
	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

func askResumeHint() string { return "↩ continue this conversation: shell3 ask --resume" }

// newAskCommand builds `shell3 ask`, a local driver for the same config the
// bot runs, with two faces: no message opens the full-screen chat UI, the
// terminal alternative to Telegram; a message makes it a one-shot scriptable
// command printing full verbose output to stdout.
//
// Either way the conversation is ask's OWN, never a room's: --resume follows
// ask's thread marker, so both front-ends can run at once.
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
			// --agent is a scripted single shot: stdout is the reply and
			// nothing else, so there is no interactive form to fall back to
			// and no conversation to resume.
			if agentFlag != "" {
				if prompt == "" {
					return fmt.Errorf(`ask: --agent needs a message, e.g. shell3 ask --agent %s -p "draft for acme.co.uk"`, agentFlag)
				}
				if resume {
					return fmt.Errorf("ask: --agent runs a fresh subagent job, so it cannot --resume")
				}
			}
			// No message: the chat UI, which reads input itself. A run with no
			// terminal to draw on must pass its message instead.
			interactive := prompt == ""
			if interactive {
				if err := requireTerminal(); err != nil {
					return err
				}
			}
			ctx := cmd.Context()

			resolved, err := resolveConfig(configDir)
			if err != nil {
				return err
			}
			// Anchored to the config dir like `shell3 telegram`, so ask shares
			// the bot's store and workdir and sees the same state.
			rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigDir: resolved, WorkDir: resolved})
			if err != nil {
				return err
			}
			defer rt.Close()

			// Sessions run in the runtime root, as the hosts default to. ask
			// is host-agnostic and reads nothing from the telegram block.
			headless := askHeadless(interactive, promptFlag != "" || agentFlag != "",
				term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stderr.Fd())))
			// ask's OWN conversation: --resume follows its own thread marker,
			// not "the newest session in this workdir", which would reattach
			// to whatever the bot last spoke in.
			resumeID := ""
			if resume {
				resumeID = askResumeID(rt.Parts().Store())
			}
			sess, err := rt.Session(shell3.SessionOpts{
				Name:     "ask",
				WorkDir:  resolved,
				ResumeID: resumeID,
				Headless: headless,
			})
			if err != nil {
				return err
			}
			// --agent dispatches the named subagent and prints only its reply,
			// returning before the chat below. It claims no resume marker: a
			// batch script looping over it must not leave the next --resume
			// pointing at its empty parent session.
			if agentFlag != "" {
				fmt.Fprintf(os.Stderr, "config=%s\n", resolved)
				return runAskAgent(ctx, os.Stdout, os.Stderr, sess, agentFlag, prompt)
			}

			// Record this session so the NEXT --resume finds it. Written for a
			// fresh one too, or the first --resume has no marker to follow.
			rememberAskSession(rt.Parts().Store(), sess.ID())

			// The chat UI owns input, rendering and the wake loop from here. It
			// draws on the alternate screen, so the banner above is hidden
			// while it runs and restored on exit.
			if interactive {
				if err := askui.Run(ctx, rt, sess, resumeID != ""); err != nil {
					return err
				}
				fmt.Println(askResumeHint())
				return nil
			}

			// The brand banner already printed from the root PersistentPreRun.
			fmt.Printf("agent=%s  config=%s\n\n", sess.ActiveAgent(), resolved)

			if err := cli.RunAskTurn(ctx, os.Stdout, sess, prompt); err != nil {
				return err
			}
			// Follow through on the jobs the turn spawned, the way the bot's
			// wake loop would. This blocks until they complete: a -p run must
			// never exit at turn end and silently kill in-flight work.
			if err := cli.FollowAskJobs(ctx, os.Stdout, rt, sess); err != nil {
				return err
			}
			fmt.Println("\n" + askResumeHint())
			return nil
		},
	}
	addConfigFlag(cmd, &configDir)
	cmd.Flags().StringVarP(&promptFlag, "prompt", "p", "", "Message for the agent (skips the chat UI — for scripts and headless runs)")
	cmd.Flags().BoolVar(&resume, "resume", false, "Continue ask's own last conversation (separate from the Telegram chat)")
	cmd.Flags().StringVar(&agentFlag, "agent", "", "Run one headless turn of this subagent and print only its reply (for scripts)")
	return cmd
}

// askHeadless decides what a gate script reads as .headless: whether a human
// could be asked about this.
//
// The chat UI is never headless — requireTerminal proved a terminal on stdin
// AND stdout, and someone is typing. Where stderr points is irrelevant there:
// `shell3 ask 2>log` still has a person at the keyboard. Otherwise a human is
// plausible only on an unscripted run with stdin and stderr both terminals;
// --agent counts as scripted however its message arrived.
func askHeadless(interactive, scripted, stdinTTY, stderrTTY bool) bool {
	if interactive {
		return false
	}
	return scripted || !stdinTTY || !stderrTTY
}

// requireTerminal refuses the chat UI with nothing to draw on: the UI reads
// keys from stdin and renders to stdout, and a piped stdout would capture
// control codes. A zero-size terminal — a degenerate CI PTY — counts as none,
// since bubbletea's layout panics at width 0.
func requireTerminal() error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf(`ask: no message and no terminal — pass one, e.g. shell3 ask -p "list the files here"`)
	}
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err != nil || w <= 0 || h <= 0 {
		return fmt.Errorf(`ask: terminal reports no size — pass the message instead, e.g. shell3 ask -p "list the files here"`)
	}
	return nil
}

// askSurface is ask's own thread-index key, namespaced away from the Telegram
// front-end's, so --resume rejoins the terminal conversation and never a
// room's. The two share one runs store and can run at once.
const askSurface = "ask"

// askResumeID is ask's recorded conversation, if it still exists. A missing
// marker or a swept session resolves to "", starting fresh rather than failing.
func askResumeID(st *runs.Store) string {
	if st == nil {
		return ""
	}
	id, ok := st.CurrentSession(askSurface)
	if !ok || id == "" {
		return ""
	}
	if _, err := st.SessionMeta(id); err != nil {
		return ""
	}
	return id
}

// rememberAskSession records sess for the next --resume. A failed write costs
// the next run its resume, never this run its turn.
func rememberAskSession(st *runs.Store, id string) {
	if st == nil || id == "" {
		return
	}
	if err := st.SetCurrentSession(askSurface, id); err != nil {
		fmt.Fprintf(os.Stderr, "ask: could not record the conversation for --resume: %v\n", err)
	}
}
