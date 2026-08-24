package askui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/shell3"
)

// inputMaxRows caps how tall the input grows before it scrolls internally.
const inputMaxRows = 15

// sessionCmds is the slice of *shell3.Session the UI drives. It is an
// interface so the model can be unit-tested without a live runtime.
type sessionCmds interface {
	// HasQueuedInput reports whether steering text interjected during a turn is
	// still waiting (it arrived too late for an in-turn round boundary), so the
	// model can auto-run a follow-up turn once the current one ends.
	HasQueuedInput() bool
	// Jobs lists the live background jobs (bash_bg processes + subagents); the
	// running count drives the footer's "bg: N" pill.
	Jobs() []shell3.JobInfo
}

type model struct {
	tr *transcript
	vp viewport.Model
	ta textarea.Model
	// send starts a turn; the returned cancel stops it (ctrl+c).
	send func(prompt string) (<-chan shell3.Event, context.CancelFunc)
	// steer queues a message for delivery to the running turn (Interject). nil
	// disables steering (e.g. in tests without a session).
	steer func(text string)
	// runQueued starts a follow-up turn seeded from the queued inbox.
	runQueued func() (<-chan shell3.Event, context.CancelFunc)
	// wakeEvents is the runtime's out-of-turn bus; a Wake for this session while
	// idle drains the queued inbox as a follow-up turn. nil disables it.
	wakeEvents <-chan shell3.HostEvent
	// jobEvents is the runtime's background-job progress bus. nil disables the
	// live "bg: N" refresh on job completion.
	jobEvents <-chan shell3.JobProgress
	sessionID string
	cmds      sessionCmds

	width, height int
	ready         bool
	isDark        bool // sensed terminal background; drives the active palette (default dark)

	totalLines  int   // total rendered content lines
	blockStarts []int // first content line of each block

	// Line-level mouse selection over the transcript viewport. The mouse is
	// captured, so the terminal cannot draw its own selection while the app
	// runs — the app draws it instead (see mouse.go).
	selecting     bool     // a left-button drag is in progress
	dragged       bool     // motion occurred since the last mouse-down
	hasSel        bool     // a selection exists (highlight + copy target)
	selAnchor     int      // content line where the drag started
	selHead       int      // content line of the drag's current end
	renderedLines []string // flattened viewport content lines (set in refresh)
	selExcluded   []bool   // parallel to renderedLines: meta lines excluded from select/copy

	bgCount int // live count of running background jobs → footer "bg: N" pill

	follow    bool // stick the viewport to the bottom as new content streams in
	busy      bool
	canceling bool // user pressed ctrl+c during a turn; emit a clean marker when it ends
	cancel    context.CancelFunc
	spinner   int
	spinning  bool // a spinnerTick chain is live (guards against duplicates)
	quitArmed bool

	agentName     string
	modelName     string // footer model label, from Snapshot.Model
	tokens        int
	contextWindow int
	notice        string
	noticeAt      time.Time // when notice was last set; the footer hides it after noticeTTL
}

func newModel(send func(string) (<-chan shell3.Event, context.CancelFunc), cmds sessionCmds, agentName, statusLine string) *model {
	// Line numbers off, a dynamic "›" prompt (set below), unlimited length,
	// dynamic height up to inputMaxRows, and custom newline keys; everything
	// else stays default.
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // unlimited
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = inputMaxRows
	// MaxHeight only sizes the visible viewport; without MaxContentHeight the
	// textarea reverts to legacy behavior and BLOCKS new lines once you reach
	// MaxHeight logical lines. Set a high content cap so you can keep adding
	// lines — they scroll inside the input past MaxHeight.
	ta.MaxContentHeight = 10000
	// Passthrough: the input carries no background of its own (a fixed surface
	// would become a dark band on a light terminal). CursorLine otherwise gets a
	// contrasting highlight by default — neutralize it so the current row isn't
	// shaded differently from the rest.
	tint := func(s textarea.StyleState) textarea.StyleState {
		s.CursorLine = lipgloss.NewStyle()
		return s
	}
	st := ta.Styles()
	st.Focused = tint(st.Focused)
	st.Blurred = tint(st.Blurred)
	ta.SetStyles(st)
	// Enter submits (handled by us); newline is Shift+Enter (terminals that
	// support it), plus Alt+Enter / Ctrl+J as reliable fallbacks.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	ta.Focus()

	m := &model{
		tr:        newTranscript(),
		vp:        viewport.New(),
		ta:        ta,
		send:      send,
		cmds:      cmds,
		follow:    true,
		isDark:    true, // assume dark until the terminal reports its background
		agentName: agentName,
	}
	// The footer's model label comes from the canonical status-line parser.
	_, m.modelName = chat.SplitStatus(statusLine)
	// Prompt marker: show "› " only when the input is a single logical line, and
	// only on its first visual row — so a multi-line (or wrapped) input isn't
	// cluttered with a marker on every row. Width 2 keeps text aligned either way.
	m.ta.SetPromptFunc(2, func(pi textarea.PromptInfo) string {
		if pi.LineNumber == 0 && m.ta.LineCount() <= 1 {
			return "› "
		}
		return "  "
	})
	return m
}

func (m *model) Init() tea.Cmd {
	// RequestBackgroundColor lets us sense a light vs. dark terminal and pick
	// the matching palette (we default to dark until the reply arrives, so
	// terminals that never answer stay on the historical look).
	return tea.Batch(m.ta.Focus(), waitWake(m.wakeEvents), waitJobProgress(m.jobEvents), bgPollTick(),
		tea.RequestBackgroundColor)
}

// applyTerminalBackground switches the active palette to match the sensed
// terminal background. It's a no-op when the mode is unchanged, so a repeated
// or same-mode report doesn't rebuild styles or re-render needlessly.
func (m *model) applyTerminalBackground(dark bool) {
	if dark == m.isDark {
		return
	}
	m.isDark = dark
	if dark {
		applyPalette(darkPalette)
	} else {
		applyPalette(lightPalette)
	}
	if m.ready {
		m.refresh(false) // re-render the transcript in the new palette
	}
}

// noticeTTL is how long the footer keeps showing a last-action notice before it
// fades. The 2s bgPollTick forces a re-render so it disappears even when the
// app is otherwise idle.
const noticeTTL = 10 * time.Second

// Update wraps update and restarts the notice's display window whenever the
// notice text changes, so every place that sets m.notice gets the timed fade
// for free.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prev := m.notice
	res, cmd := m.update(msg)
	if m.notice != prev {
		m.noticeAt = time.Now()
	}
	return res, cmd
}

// activeNotice returns the last-action notice while it is still within its
// display window, else "" (so the footer drops it after noticeTTL).
func (m *model) activeNotice() string {
	if m.notice == "" || m.noticeAt.IsZero() || time.Since(m.noticeAt) >= noticeTTL {
		return ""
	}
	return m.notice
}

func (m *model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.refreshJobCount()
		m.relayout()
		return m, nil
	case tea.BackgroundColorMsg:
		m.applyTerminalBackground(msg.IsDark())
		return m, nil
	case spinnerTickMsg:
		if m.busy {
			m.spinner++
			return m, spinnerTick()
		}
		m.spinning = false // chain ends when the turn is no longer busy
		return m, nil
	case bgPollTickMsg:
		m.refreshJobCount()
		return m, bgPollTick()
	case eventMsg:
		return m.handleEvent(msg)
	case wakeMsg:
		return m, tea.Batch(m.handleWake(msg.ev), waitWake(m.wakeEvents))
	case jobProgressMsg:
		m.refreshJobCount()
		return m, waitJobProgress(m.jobEvents)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		// A bracketed paste isn't a KeyPressMsg, so it skips the keystroke path
		// that recomputes layout — without relayout a multi-line paste grows the
		// input but leaves the footer/viewport stale (mangled) until the next
		// key. Scoped to PasteMsg specifically: the catch-all below must NOT
		// relayout, or the cursor's recurring BlinkMsg would re-render the
		// transcript ~2x/sec.
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		m.relayout()
		return m, cmd
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	// Catch-all for other messages (e.g. the cursor BlinkMsg): forward to the
	// textarea WITHOUT relayout — see the PasteMsg case above.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// refreshJobCount re-reads the running background-job count for the footer's
// "bg: N" pill. Finished jobs are retained by the runtime but must NOT inflate
// it — the pill reflects active work only.
func (m *model) refreshJobCount() {
	if m.cmds == nil {
		return
	}
	n := 0
	for _, j := range m.cmds.Jobs() {
		// A subagent job can report Done=true while its child session lingers to
		// run a follow-up turn for a bash_bg that outlived the turn. Treat that
		// as still running: the user is still waiting on it.
		if !j.Done || j.ChildOpen {
			n++
		}
	}
	m.bgCount = n
}
