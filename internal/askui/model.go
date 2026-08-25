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
	// HasQueuedInput: steering that arrived too late for a round boundary is
	// still waiting, so a follow-up turn should run once this one ends.
	HasQueuedInput() bool
	// Jobs lists the live background jobs, whose count is the footer's pill.
	Jobs() []shell3.JobInfo
}

type model struct {
	tr *transcript
	vp viewport.Model
	ta textarea.Model
	// send starts a turn; the returned cancel stops it (ctrl+c).
	send func(prompt string) (<-chan shell3.Event, context.CancelFunc)
	// steer interjects into the running turn; nil disables steering.
	steer func(text string)
	// runQueued starts a follow-up turn seeded from the queued inbox.
	runQueued func() (<-chan shell3.Event, context.CancelFunc)
	// wakeEvents is the out-of-turn bus: an idle Wake drains the queued inbox
	// as a follow-up turn. nil disables it.
	wakeEvents <-chan shell3.HostEvent
	// jobEvents is the job-progress bus; nil disables the live pill refresh.
	jobEvents <-chan shell3.JobProgress
	sessionID string
	cmds      sessionCmds

	width, height int
	ready         bool
	isDark        bool // sensed terminal background; drives the active palette (default dark)

	totalLines  int   // total rendered content lines
	blockStarts []int // first content line of each block

	// Line-level selection over the viewport. The mouse is captured, so the
	// terminal cannot draw its own and the app draws it instead.
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
	// No line numbers, a dynamic "›" prompt, unlimited length, height up to
	// inputMaxRows, custom newline keys; everything else default.
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // unlimited
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = inputMaxRows
	// MaxHeight sizes only the visible viewport; without MaxContentHeight the
	// textarea BLOCKS new lines past MaxHeight logical ones. A high content
	// cap lets them keep coming and scroll inside the input.
	ta.MaxContentHeight = 10000
	// No background of its own — a fixed surface becomes a dark band on a
	// light terminal — and CursorLine's default contrast is neutralized, so
	// the current row is not shaded differently.
	tint := func(s textarea.StyleState) textarea.StyleState {
		s.CursorLine = lipgloss.NewStyle()
		return s
	}
	st := ta.Styles()
	st.Focused = tint(st.Focused)
	st.Blurred = tint(st.Blurred)
	ta.SetStyles(st)
	// Enter submits; newline is Shift+Enter where supported, with Alt+Enter
	// and Ctrl+J as fallbacks.
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
	// "› " only on a single logical line's first visual row, so a wrapped
	// input is not marked on every row. Width 2 keeps text aligned either way.
	m.ta.SetPromptFunc(2, func(pi textarea.PromptInfo) string {
		if pi.LineNumber == 0 && m.ta.LineCount() <= 1 {
			return "› "
		}
		return "  "
	})
	return m
}

func (m *model) Init() tea.Cmd {
	// Senses light vs dark and picks the matching palette, defaulting to dark
	// until the reply arrives so silent terminals keep the historical look.
	return tea.Batch(m.ta.Focus(), waitWake(m.wakeEvents), waitJobProgress(m.jobEvents), bgPollTick(),
		tea.RequestBackgroundColor)
}

// applyTerminalBackground switches palette to the sensed background, a no-op
// when unchanged so a repeated report rebuilds nothing.
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

// noticeTTL is how long the footer keeps a last-action notice. bgPollTick
// forces the re-render, so it fades even when the app is idle.
const noticeTTL = 10 * time.Second

// Update restarts the notice window whenever the text changes, so every site
// that sets m.notice gets the timed fade for free.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prev := m.notice
	res, cmd := m.update(msg)
	if m.notice != prev {
		m.noticeAt = time.Now()
	}
	return res, cmd
}

// activeNotice is the notice while it is still within its window, else "".
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
		// A bracketed paste is not a KeyPressMsg, so it skips the path that
		// recomputes layout, leaving the footer and viewport mangled until
		// the next key. Scoped to PasteMsg: the catch-all below must NOT
		// relayout, or BlinkMsg would re-render the transcript twice a second.
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		m.relayout()
		return m, cmd
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	// Everything else forwards to the textarea WITHOUT relayout — see above.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// refreshJobCount re-reads the running count for the footer pill. The runtime
// retains finished jobs, which must not inflate it.
func (m *model) refreshJobCount() {
	if m.cmds == nil {
		return
	}
	n := 0
	for _, j := range m.cmds.Jobs() {
		// A subagent reports Done while its child lingers for a bash_bg that
		// outlived the turn. Still running: the user is waiting on it.
		if !j.Done || j.ChildOpen {
			n++
		}
	}
	m.bgCount = n
}
