package tui

import (
	"context"
	"image/color"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// inputMaxRows caps how tall the input grows before it scrolls internally.
const inputMaxRows = 15

// editMode is the vim-style mode the TUI is in.
type editMode int

const (
	modeInsert  editMode = iota // typing into the input
	modeNormal                  // navigating the transcript (cursor, folds, yank)
	modeCommand                 // ":" command line
)

// sessionCmds is the slice of *shell3.Session that : commands drive.
type sessionCmds interface {
	Clear() error
	Rollback() (bool, error)
	Prune(id string) (string, error)
	QueueCompact()
	SwitchAgent(name string) error
	AgentNames() []string
	ActiveAgent() string
	Snapshot() shell3.Snapshot
	// HasQueuedInput reports whether steering text interjected during a turn is
	// still waiting (it arrived too late for an in-turn round boundary), so the
	// model can auto-run a follow-up turn once the current one ends.
	HasQueuedInput() bool
	// Jobs lists the live background jobs (bash_bg processes + subagents);
	// JobOutput returns the tail of one job's stdout log; JobTranscript returns a
	// subagent's structured --out transcript ("" for a plain bash_bg job);
	// KillJob signals one to stop. They drive the :background modal.
	Jobs() []shell3.JobInfo
	JobOutput(id string) string
	JobTranscript(id string) string
	KillJob(id string) error
	// SetSafetyOff toggles the session-level auto-allow for on_tool_call asks
	// (:disable_safety). The model keeps a local mirror for the footer "!".
	SetSafetyOff(off bool)
}

type model struct {
	tr   *Transcript
	vp   viewport.Model
	ta   textarea.Model
	send func(prompt string) (<-chan shell3.Event, context.CancelFunc)
	// steer queues a message for delivery to the running turn (Interject). nil
	// disables steering (e.g. in tests without a session).
	steer func(text string)
	// runQueued starts a follow-up turn seeded from the queued steering inbox.
	runQueued func() (<-chan shell3.Event, context.CancelFunc)
	// wakeEvents is the runtime's out-of-turn bus; a Wake for this session while
	// idle drains the queued inbox as a follow-up turn. nil disables it.
	wakeEvents <-chan shell3.HostEvent
	// jobEvents is the runtime's background-job progress bus; each event carries
	// incremental output (Chunk) or a Done signal. nil disables live-tail.
	jobEvents   <-chan shell3.JobProgress
	sessionName string
	cmds        sessionCmds

	mode          editMode
	width, height int
	ready         bool
	isDark        bool                   // sensed terminal background; drives the active palette (default dark)
	themeOverride map[string]color.Color // shell3.theme{} color overrides, applied atop the sensed palette

	cursorLine  int   // NORMAL-mode line cursor (flattened content line)
	totalLines  int   // total rendered content lines
	blockStarts []int // first content line of each block
	cmdline     string
	pending     rune // multi-key prefix in NORMAL (g, z, d)

	// Line-level mouse selection over the transcript viewport.
	selecting     bool     // a left-button drag is in progress
	dragged       bool     // motion occurred since the last mouse-down
	hasSel        bool     // a selection exists (highlight + copy target)
	selAnchor     int      // content line where the drag started
	selHead       int      // content line of the drag's current end
	renderedLines []string // flattened viewport content lines (set in refresh)
	selExcluded   []bool   // parallel to renderedLines: meta lines excluded from select/copy

	// :background modal — list, inspect, and kill background jobs (bash_bg
	// processes and fire-and-forget subagents).
	bgOpen         bool
	bgJobs         []shell3.JobInfo
	bgSel          int      // selected row in the list view
	bgViewID       string   // non-empty = viewing this job's output (else the list)
	bgOutput       string   // loaded output of the viewed job (raw transcript JSONL or stdout)
	bgIsTranscript bool     // true ⇒ bgOutput is a subagent --out transcript (render structured)
	bgScroll       int      // first visible output line in the output view
	bgNotice       string   // status line shown inside the modal (e.g. a kill result)
	bgRows         []string // memoized rendered output rows (nil = recompute; see bgWrappedLines)
	bgRowsW        int      // content width bgRows was rendered at (re-render on resize)
	bgCount        int      // live count of background jobs, shown as "bg: N" on the footer (polled)

	helpOpen         bool
	confirm          *confirmReq // pending on_tool_call ask modal (nil = none)
	confirmYes       bool        // which button is selected (default Yes)
	safetyOff        bool        // :disable_safety — auto-allow every on_tool_call ask
	safetyConfigured bool        // on_tool_call enabled in the lua config (else the shell is unsafe by default)
	follow           bool        // stick the viewport to the bottom as new content streams in
	busy             bool
	canceling        bool // user pressed ctrl+c; emit a clean marker when the turn ends
	cancel           context.CancelFunc
	spinner          int
	spinning         bool // a spinnerTick chain is live (guards against duplicates)
	quitArmed        bool

	agentName     string
	welcome       string // custom welcome card (shell3.welcome); empty = built-in
	statusMsg     string
	modelName     string // footer model label, from Snapshot.Model (chat.SplitStatus)
	tokens        int
	promptTokens  int
	completTokens int
	contextWindow int
	notice        string
	noticeAt      time.Time // when notice was last set; the footer hides it after noticeTTL
}

func newModel(send func(string) (<-chan shell3.Event, context.CancelFunc), cmds sessionCmds, agentName, statusMsg string) *model {
	// Line numbers off, a dynamic "›" prompt (set below), unlimited length,
	// dynamic height up to inputMaxRows, and custom newline keys; everything else
	// stays default.
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
	// shaded differently from the rest. (No SetVirtualCursor/prompt-func: the
	// stock cursor stays visible.)
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
		tr:        NewTranscript(),
		vp:        viewport.New(),
		ta:        ta,
		send:      send,
		cmds:      cmds,
		mode:      modeInsert,
		follow:    true,
		isDark:    true, // assume dark until the terminal reports its background
		agentName: agentName,
		statusMsg: statusMsg,
	}
	// The footer's model label comes from the canonical status-line parser —
	// applyAgent refreshes it from Snapshot.Model on agent switches.
	_, m.modelName = chat.SplitStatus(statusMsg)
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
	// RequestBackgroundColor lets us sense a light vs. dark terminal and pick the
	// matching palette (we default to dark until the reply arrives, so terminals
	// that never answer stay on the historical look).
	return tea.Batch(m.ta.Focus(), waitWake(m.wakeEvents), waitJobProgress(m.jobEvents), bgPollTick(),
		tea.RequestBackgroundColor)
}

// applyTheme rebuilds the active palette from the current mode (dark/light) with
// the shell3.theme{} overrides applied on top. Call after changing isDark or
// themeOverride.
func (m *model) applyTheme() {
	base := darkPalette
	if !m.isDark {
		base = lightPalette
	}
	applyPalette(base.withOverrides(m.themeOverride))
}

// applyTerminalBackground switches the active palette to match the sensed
// terminal background. It's a no-op when the mode is unchanged, so a repeated or
// same-mode report doesn't rebuild styles or re-render needlessly.
func (m *model) applyTerminalBackground(dark bool) {
	if dark == m.isDark {
		return
	}
	m.isDark = dark
	m.applyTheme()
	if m.ready {
		m.refresh(false) // re-render the transcript in the new palette
	}
}

// noticeTTL is how long the footer keeps showing a last-action notice before it
// fades. The 2s bgPollTick forces a re-render so it disappears even when the app
// is otherwise idle.
const noticeTTL = 10 * time.Second

// Update wraps update and restarts the notice's display window whenever the
// notice text changes, so every place that sets m.notice gets the timed fade for
// free.
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
		if m.cmds != nil {
			m.bgCount = countRunningJobs(m.cmds.Jobs()) // running-only; done jobs are retained but don't count
		}
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
		if m.cmds != nil {
			jobs := m.cmds.Jobs()
			m.bgCount = countRunningJobs(jobs)
			if m.bgOpen {
				m.bgJobs = jobs
				m.clampJobSel()
				if m.bgViewID != "" {
					m.loadJobOutput(m.bgViewID)
				}
			}
		}
		return m, bgPollTick()
	case eventMsg:
		return m.handleEvent(msg)
	case wakeMsg:
		if !msg.ok {
			return m, nil // bus closed
		}
		return m, tea.Batch(m.handleWake(msg.ev), waitWake(m.wakeEvents))
	case jobProgressMsg:
		m.handleJobProgress(shell3.JobProgress(msg))
		return m, waitJobProgress(m.jobEvents)
	case openEditorMsg:
		return m.handleEditorResult(msg)
	case confirmMsg:
		// :disable_safety auto-allow happens inside the Session's Asker wrapper
		// (Session.SetSafetyOff) — a confirm request only reaches the modal when
		// the gate is on.
		m.confirm = msg.req
		m.confirmYes = true // default to Yes so a quick Enter allows
		return m, nil
	case confirmAbortMsg:
		// Dismiss only if this is still the same pending ask: a user keypress may
		// have resolved (and replaced/cleared) it just before the abort arrived.
		if m.confirm == msg.req {
			m.confirm = nil
			m.notice = "command gate prompt timed out — command denied"
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		// A bracketed paste isn't a KeyPressMsg, so it skips the keystroke path
		// that recomputes layout — without relayout a multi-line paste grows the
		// input but leaves the footer/viewport stale (mangled) until the next key.
		// Scoped to PasteMsg specifically: the catch-all below must NOT relayout,
		// or the cursor's recurring BlinkMsg would re-render the transcript ~2x/sec.
		if m.mode != modeInsert {
			return m, nil
		}
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		m.relayout()
		return m, cmd
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case shellDoneMsg:
		if msg.err != nil {
			m.notice = "! " + msg.cmd + ": " + msg.err.Error()
		} else {
			m.notice = "! " + msg.cmd
		}
		m.refresh(false)
		return m, nil
	}
	if m.mode == modeInsert {
		// Catch-all for other insert-mode messages (e.g. the cursor BlinkMsg):
		// forward to the textarea WITHOUT relayout — see the PasteMsg case above.
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	}
	return m, nil
}

// minModalWidth is the floor for any modal's content width; below this a box
// becomes too cramped to read.
const minModalWidth = 40
