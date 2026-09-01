// Package askui is the interactive terminal UI behind `shell3 ask` with no
// message: a compact full-screen chat built on bubbletea, bubbles, and
// lipgloss. The input is always live (no modes to switch between); tool and
// reasoning roundtrips render as collapsible blocks in a scrollable
// transcript, and assistant replies render as markdown.
//
// It is the interactive path only. `shell3 ask -p`, `--agent`, and any
// non-TTY invocation keep the plain streaming renderer in internal/cli, whose
// stdout is a scriptable contract.
package askui

import (
	"context"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Run drives an interactive chat on sess and blocks until the user quits.
// It renders in the alternate screen, so nothing it draws survives the exit —
// the conversation itself lives in the runs store either way.
//
// rt is required: it carries the out-of-turn wake bus and the app logger.
// resumed marks a session that reattached to a stored conversation, which the
// transcript surfaces as a marker so the user knows the context came back.
func Run(ctx context.Context, rt *shell3.Runtime, sess *shell3.Session, resumed bool) error {
	snap := sess.Snapshot()
	m := newModel(
		func(prompt string) (<-chan shell3.Event, context.CancelFunc) {
			turnCtx, cancel := context.WithCancel(ctx)
			return sess.Send(turnCtx, prompt), cancel
		},
		sess, snap.Agent, snap.Model,
	)
	m.contextWindow = snap.ContextWindow
	m.sessionID = sess.ID()
	m.steer = func(text string) { sess.Interject(text) }
	m.runQueued = func() (<-chan shell3.Event, context.CancelFunc) {
		turnCtx, cancel := context.WithCancel(ctx)
		return sess.RunQueued(turnCtx), cancel
	}
	// Out-of-turn wake bus: `ask` installs no CompletionHost, so a finished
	// background job's notice is queued straight onto this session and a Wake is
	// emitted; the model drains it as a follow-up turn (see handleWake).
	m.wakeEvents = rt.Events()
	m.jobEvents = sess.JobEvents()

	// Surface non-fatal config warnings in-band: they were printed to stderr at
	// load, but the alt-screen UI clears that line before the user sees it.
	for _, w := range snap.Warnings {
		m.tr.addInfo("config warning: " + w)
	}
	if resumed {
		m.tr.addInfo(fmt.Sprintf("⟲ resumed conversation — %d messages in context", sess.MessageCount()))
	}

	// applog mirrors WARN/ERROR to stderr, which this program does not own: a
	// gate refusal or a provider retry logged mid-frame paints raw text across
	// the rendered screen and stays there until the next full redraw. Silence
	// the mirror for as long as we hold the alternate screen — the lines still
	// reach the app log, and stderr is restored on the way out (deferred, so a
	// panic unwinding through here doesn't leave the user's terminal muted).
	if ms, ok := rt.Parts().Log().(applog.MirrorSetter); ok {
		ms.SetMirror(io.Discard)
		defer ms.SetMirror(os.Stderr)
	}

	prog := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := prog.Run()
	return err
}
