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

// askResumeHint tells the user how to pick the conversation back up.
func askResumeHint() string { return "↩ continue this conversation: shell3 ask --resume" }

// newAskCommand builds `shell3 ask` — a local driver for the same config +
// agent the bot runs. It exists to drive and polish the agent without a
// Telegram chat; it is also handy for quick local queries and troubleshooting.
//
// It has two faces. With NO message it opens the full-screen chat UI
// (internal/askui) — a real terminal alternative to the Telegram front-end.
// With a message (argv, -p, or --agent) it stays a one-shot scriptable
// command, printing full verbose output (reply, every tool call + args,
// untruncated tool results, reasoning, token usage) to stdout.
//
// Either way the conversation is ask's OWN, never a Telegram room's:
// --resume follows ask's thread marker, so both front-ends can run at once.
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
			// interactive: no message given → the full-screen chat UI, which
			// reads every message itself. A run with no terminal to draw on
			// must pass its message instead.
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
			// Anchor the runtime to the config dir, exactly like `shell3 telegram`,
			// so ask shares the bot's runs store + workdir and sees the same state.
			rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigDir: resolved, WorkDir: resolved})
			if err != nil {
				return err
			}
			defer rt.Close()

			// Sessions run in the config dir (the runtime root), the same
			// default the hosts use. ask is host-agnostic: it reads nothing
			// from the telegram block.
			headless := askHeadless(interactive, promptFlag != "" || agentFlag != "",
				term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stderr.Fd())))
			// ask keeps its OWN conversation, separate from every Telegram
			// room's: --resume follows ask's own thread marker rather than
			// "the newest session in this workdir", which would reattach to
			// whatever the bot was last talking in. Both front-ends can run at
			// once without either one inheriting the other's context.
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
			// --agent: dispatch the named subagent and print only its reply.
			// This returns before the chat below — a scripted run has no second
			// turn to read. It claims no resume marker either: it refuses
			// --resume because it holds no conversation, so a batch script
			// looping over it must not leave the user's next --resume pointing
			// at its empty parent session.
			if agentFlag != "" {
				fmt.Fprintf(os.Stderr, "config=%s\n", resolved)
				return runAskAgent(ctx, os.Stdout, os.Stderr, sess, agentFlag, prompt)
			}

			// Record this session as ask's current conversation so the NEXT
			// --resume finds it. Written for a fresh session too — otherwise the
			// first --resume after a fresh run has no marker to follow.
			rememberAskSession(rt.Parts().Store(), sess.ID())

			// Interactive: hand the terminal to the chat UI, which owns input,
			// rendering, and the background-job wake loop for the rest of the
			// run. It draws on the alternate screen, so the banner above is
			// hidden while it runs and restored on exit.
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
			// Follow through on any subagent/bash_bg jobs the turn spawned, so
			// ask shows their results the way the bot's wake loop would.
			// This blocks until those in-process jobs complete (SIGINT to quit):
			// a -p run must never exit at turn end and silently kill in-flight work.
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

// askHeadless decides the session's Headless flag — what a gate script reads
// as .headless, i.e. "is there a human who could be asked about this". Pure so
// the wiring is unit-testable without a real terminal.
//
// The chat UI is never headless: it only starts once requireTerminal has
// proved a real terminal on stdin AND stdout, and a human is typing into it.
// Where stderr points is irrelevant there — `shell3 ask 2>log` still has
// someone at the keyboard, and judging the UI by stderr marked a live session
// headless.
//
// Otherwise a human is plausibly present only when the run is not scripted
// and both stdin and stderr are terminals. --agent counts as scripted however
// its message arrived (-p or argv): no human is in its turn, so its payload
// must say headless even when it was launched from a terminal.
func askHeadless(interactive, scripted, stdinTTY, stderrTTY bool) bool {
	if interactive {
		return false
	}
	return scripted || !stdinTTY || !stderrTTY
}

// requireTerminal refuses the interactive chat UI when there is nothing to
// draw on. Both ends must be a terminal: the UI reads keys from stdin and
// renders to stdout (a piped stdout would capture control codes). A zero-size
// terminal (degenerate PTYs: CI, expect without stty) counts as no terminal —
// bubbletea's layout panics at width 0.
func requireTerminal() error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf(`ask: no message and no terminal — pass one, e.g. shell3 ask -p "list the files here"`)
	}
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err != nil || w <= 0 || h <= 0 {
		return fmt.Errorf(`ask: terminal reports no size — pass the message instead, e.g. shell3 ask -p "list the files here"`)
	}
	return nil
}

// askSurface is the thread-index key for `shell3 ask`'s own conversation. It
// is namespaced away from the Telegram front-end's "telegram:<chat>" keys, so
// --resume rejoins the terminal conversation and never a chat room's — the two
// front-ends share one runs store and can run at the same time.
const askSurface = "ask"

// askResumeID returns the session --resume should reattach to: ask's own
// recorded conversation, if it still exists. A missing marker (first run) or a
// swept session (the runs janitor) resolves to "", which starts a fresh
// conversation rather than failing.
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

// rememberAskSession records sess as ask's current conversation for the next
// --resume. Best-effort: a failed write costs the next run its resume, never
// this run its turn.
func rememberAskSession(st *runs.Store, id string) {
	if st == nil || id == "" {
		return
	}
	if err := st.SetCurrentSession(askSurface, id); err != nil {
		fmt.Fprintf(os.Stderr, "ask: could not record the conversation for --resume: %v\n", err)
	}
}
