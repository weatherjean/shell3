// Package tui is shell3's full-screen, vim-modal terminal UI, built on
// bubbletea, bubbles, and lipgloss. Tool and reasoning roundtrips render as
// collapsible blocks in a scrollable transcript; assistant replies render as
// markdown. The mouse is left uncaptured so the terminal's native
// select-and-copy keeps working, with OSC 52 copy available for the focused
// block.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// RunInteractive runs the interactive chat loop on a pkg/shell3 Session and
// blocks until the user quits.
func RunInteractive(ctx context.Context, spec shell3.Spec) (runErr error) {
	var prog *tea.Program

	spec.Interactive = true
	spec.ShellInteractive = func(_ context.Context, cmd, workdir string) string {
		result := "(completed)"
		if prog != nil {
			_ = prog.ReleaseTerminal()
			defer func() { _ = prog.RestoreTerminal() }()
		}
		c := exec.Command("bash", "-c", cmd)
		if workdir != "" {
			c.Dir = workdir
		}
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			result = "error: " + err.Error()
		}
		return result
	}
	spec.Asker = func(ctx context.Context, command, reason string) bool {
		if prog == nil {
			return false
		}
		// Route the ask into the TUI as a Yes/No modal (no terminal release, so
		// the app never drops out). Block this turn goroutine until the user
		// answers or the turn is canceled.
		reply := make(chan bool, 1)
		req := &confirmReq{command: command, reason: reason, reply: reply}
		prog.Send(confirmMsg{req: req})
		select {
		case ok := <-reply:
			return ok
		case <-ctx.Done():
			// Context canceled (ask_timeout fired, or the turn was canceled): tell
			// the TUI to dismiss the now-abandoned modal so it doesn't trap the
			// keyboard, then deny.
			prog.Send(confirmAbortMsg{req: req})
			return false
		}
	}

	sess, err := shell3.Start(ctx, spec)
	if err != nil {
		return err
	}
	defer sess.Close()

	snap := sess.Snapshot()
	m := newModel(
		func(prompt string) (<-chan shell3.Event, context.CancelFunc) {
			turnCtx, cancel := context.WithCancel(ctx)
			return sess.Send(turnCtx, prompt), cancel
		},
		sess, snap.Agent, snap.StatusLine,
	)
	m.contextWindow = snap.ContextWindow
	m.safetyConfigured = snap.ToolHooksOn
	// Surface non-fatal config warnings in-band: they were printed to stderr at
	// load, but the alt-screen TUI clears that line before the user sees it.
	for _, w := range snap.Warnings {
		m.tr.AddInfo("config warning: " + w)
	}
	// Mid-turn steering: queue interjected text, and run it as a follow-up turn
	// when the current one ends with input still queued.
	m.steer = func(text string) { sess.Interject(text) }
	m.runQueued = func() (<-chan shell3.Event, context.CancelFunc) {
		turnCtx, cancel := context.WithCancel(ctx)
		return sess.RunQueued(turnCtx), cancel
	}
	// Resuming a stored conversation: the reloaded messages stay in the session;
	// surface a marker so the user knows the context was restored.
	if spec.ResumeID != "" {
		m.tr.AddInfo(fmt.Sprintf("⟲ resumed conversation — %d messages in context", len(sess.History())))
	}
	// Out-of-turn wake bus: when a backgrounded subagent finishes (or idle
	// steering is queued) the runtime emits a Wake for this session; drain it as
	// a follow-up turn so the agent reacts without the user typing first.
	m.wakeEvents = sess.WakeEvents()
	m.sessionName = sess.Name()

	prog = tea.NewProgram(m, tea.WithContext(ctx))
	_, err = prog.Run()
	return err
}
