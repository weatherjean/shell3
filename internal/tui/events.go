package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/pkg/shell3"
)

type eventMsg struct {
	ev shell3.Event
	ok bool
	ch <-chan shell3.Event
}

// spinnerTickMsg drives the rainbow "thinking" animation while busy (the
// shift advances each tick); there is no glyph spinner.
type spinnerTickMsg struct{}

// wakeMsg carries one out-of-turn HostEvent from the wake bus.
type wakeMsg struct {
	ev shell3.HostEvent
	ok bool
}

// jobProgressMsg carries one background-job progress event from the job bus.
type jobProgressMsg shell3.JobProgress

// bgPollTickMsg periodically refreshes the footer's subprocess count.
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
		return wakeMsg{ev: ev, ok: ok}
	}
}

// waitJobProgress blocks for the next job-progress event. nil channel → no command.
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
		// the clean marker on the channel close so it always shows; AddCanceled
		// also folds any half-streamed thinking block.
		if m.canceling {
			m.canceling = false
			m.notice = ""
			m.tr.AddCanceled()
			m.follow = true
			m.refresh(true)
			return m, nil
		}
		// Steering that arrived during the turn's final round has no in-turn
		// boundary left to drain it, so run it now as a follow-up turn.
		if m.runQueued != nil && m.cmds != nil && m.cmds.HasQueuedInput() {
			ch, cancel := m.runQueued()
			m.busy = true
			m.cancel = cancel
			m.follow = true
			m.notice = ""
			return m, tea.Batch(waitEvent(ch), m.startSpin())
		}
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
		if msg.ev.PromptTokens > 0 {
			m.promptTokens = msg.ev.PromptTokens
		}
		if msg.ev.CompletionTokens > 0 {
			m.completTokens = msg.ev.CompletionTokens
		}
	}
	// Compaction rewrote history: drop the meter to the post-compaction estimate
	// at once, rather than waiting for the next provider usage. The estimate is
	// prompt-only (no response yet), so clear the completion count.
	if msg.ev.Kind == shell3.Compacted && msg.ev.TotalTokens > 0 {
		m.tokens = msg.ev.TotalTokens
		m.promptTokens = msg.ev.PromptTokens
		m.completTokens = 0
	}
	if m.tr.Apply(msg.ev) {
		m.refresh(false)
	}
	return m, waitEvent(msg.ch)
}

// handleWake drains the queued inbox as a follow-up turn when a Wake names this
// session and no turn is running (a subagent finished, or steering was left
// queued by a canceled turn). A running turn drains its own inbox.
func (m *model) handleWake(ev shell3.HostEvent) tea.Cmd {
	if ev.Kind != shell3.Wake || ev.Session != m.sessionName || m.busy {
		return nil
	}
	if m.runQueued == nil || m.cmds == nil || !m.cmds.HasQueuedInput() {
		return nil
	}
	ch, cancel := m.runQueued()
	m.busy = true
	m.cancel = cancel
	m.follow = true
	m.notice = "responding to queued input"
	return tea.Batch(waitEvent(ch), m.startSpin())
}

func spinnerTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// startSpin begins the thinking animation, but only if a tick chain isn't
// already running — otherwise a back-to-back turn (a queued-steering follow-up)
// would leave two chains ticking at once.
func (m *model) startSpin() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return spinnerTick()
}

// bgPollTick schedules the next subprocess-count refresh. The count drives the
// footer's "bg: N" pill and changes out-of-band (a subagent finishes, a bash_bg
// exits) with no event to react to, so a steady poll keeps it honest; 2s is
// invisible to the eye and cheap (a jobs-dir glob). The steady tick also lets the
// footer's timed notice fade when the app is otherwise idle.
func bgPollTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return bgPollTickMsg{} })
}

// countRunningJobs counts jobs still running. Finished jobs are retained in the
// list (so they stay viewable in the :background modal) but must NOT inflate the
// footer's "bg: N" pill, which reflects active work only.
func countRunningJobs(jobs []shell3.JobInfo) int {
	n := 0
	for _, j := range jobs {
		if !j.Done {
			n++
		}
	}
	return n
}
