package askui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/weatherjean/shell3/internal/shell3"
)

type eventMsg struct {
	ev shell3.Event
	ok bool
	ch <-chan shell3.Event
}

// spinnerTickMsg drives the rainbow "thinking" animation while busy (the shift
// advances each tick); there is no glyph spinner.
type spinnerTickMsg struct{}

// wakeMsg carries one out-of-turn HostEvent from the wake bus. A closed bus
// returns a nil msg from the wait command instead (same idiom as
// waitJobProgress), which bubbletea drops — the chain simply stops re-arming.
type wakeMsg struct {
	ev shell3.HostEvent
}

// jobProgressMsg carries one background-job progress event from the job bus.
type jobProgressMsg shell3.JobProgress

// bgPollTickMsg periodically refreshes the footer's background-job count.
type bgPollTickMsg struct{}

func waitEvent(ch <-chan shell3.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		return eventMsg{ev: ev, ok: ok, ch: ch}
	}
}

// waitWake blocks for the next wake-bus event. nil channel → no command.
func waitWake(ch <-chan shell3.HostEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil // bus closed: stop re-arming
		}
		return wakeMsg{ev: ev}
	}
}

// waitJobProgress blocks for the next job-progress event. nil channel → no
// command.
func waitJobProgress(ch <-chan shell3.JobProgress) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return jobProgressMsg(p)
	}
}

func (m *model) handleEvent(msg eventMsg) (tea.Model, tea.Cmd) {
	if !msg.ok {
		m.busy = false
		m.cancel = nil
		// A canceled turn ends here regardless of whether an Error(canceled)
		// event was emitted (it isn't, e.g., when canceling mid-thinking). Emit
		// the clean marker on the channel close so it always shows; addCanceled
		// also folds any half-streamed thinking block.
		if m.canceling {
			m.canceling = false
			m.notice = ""
			m.tr.addCanceled()
			m.follow = true
			m.refresh(true)
			return m, nil
		}
		// Input that arrived during the turn's final round has no in-turn
		// boundary left to drain it, so run it now as a follow-up turn.
		if cmd := m.startQueuedTurn("responding to queued input"); cmd != nil {
			return m, cmd
		}
		m.refresh(false)
		return m, nil
	}
	// Suppress the raw Error(context.Canceled) — the channel-close handler above
	// emits the clean "⊘ canceled" marker instead of a red "✗" error.
	if msg.ev.Kind == shell3.Error && errors.Is(msg.ev.Err, context.Canceled) {
		return m, waitEvent(msg.ch)
	}
	if msg.ev.Kind == shell3.Usage || msg.ev.Kind == shell3.Done {
		if msg.ev.TotalTokens > 0 {
			m.tokens = msg.ev.TotalTokens
		}
	}
	// Compaction rewrote history: drop the meter to the post-compaction estimate
	// at once, rather than waiting for the next provider usage.
	if msg.ev.Kind == shell3.Compacted && msg.ev.TotalTokens > 0 {
		m.tokens = msg.ev.TotalTokens
	}
	if m.tr.apply(msg.ev) {
		m.refresh(false)
	}
	return m, waitEvent(msg.ch)
}

// handleWake drains the queued inbox as a follow-up turn when a Wake names this
// session and no turn is running (a background job finished, or steering was
// left queued by a canceled turn). A running turn drains its own inbox.
//
// This is the TUI's stand-in for cli.FollowAskJobs: `shell3 ask` installs no
// CompletionHost, so the runtime delivers every completion's raw notice
// straight to this session's inbox and wakes it — without draining it here, a
// subagent or bash_bg result would never be narrated.
func (m *model) handleWake(ev shell3.HostEvent) tea.Cmd {
	if ev.Kind != shell3.Wake || ev.Session != m.sessionID || m.busy {
		return nil
	}
	return m.startQueuedTurn("responding to a finished background task")
}

// startQueuedTurn runs a follow-up turn seeded from the queued inbox, if there
// is anything queued and the wiring for it exists. Returns nil when there is
// nothing to run.
func (m *model) startQueuedTurn(notice string) tea.Cmd {
	if m.runQueued == nil || m.cmds == nil || !m.cmds.HasQueuedInput() {
		return nil
	}
	ch, cancel := m.runQueued()
	m.busy = true
	m.cancel = cancel
	m.follow = true
	m.notice = notice
	return tea.Batch(waitEvent(ch), m.startSpin())
}

func spinnerTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// startSpin begins the thinking animation, but only if a tick chain isn't
// already running — otherwise a back-to-back turn (a queued-input follow-up)
// would leave two chains ticking at once.
func (m *model) startSpin() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return spinnerTick()
}

// bgPollTick schedules the next background-job-count refresh. The count drives
// the footer's "bg: N" pill and changes out-of-band with no event to react to
// (a job progress event covers most of it, but a job that starts between
// events would not), so a steady poll keeps it honest; 2s is invisible to the
// eye and cheap. The steady tick also lets the footer's timed notice fade when
// the app is otherwise idle.
func bgPollTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return bgPollTickMsg{} })
}
