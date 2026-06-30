package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// inputMaxRows caps how tall the input grows before it scrolls internally.
const inputMaxRows = 15

type eventMsg struct {
	ev shell3.Event
	ok bool
	ch <-chan shell3.Event
}

// spinnerTickMsg drives the rainbow "thinking" animation while busy (the
// shift advances each tick); there is no glyph spinner.
type spinnerTickMsg struct{}

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
	Prune(id string) (string, bool)
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
	wakeEvents  <-chan shell3.HostEvent
	sessionName string
	cmds        sessionCmds

	mode          editMode
	width, height int
	ready         bool

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
	confirm          *confirmReq // pending bash_safety ask modal (nil = none)
	confirmYes       bool        // which button is selected (default Yes)
	safetyOff        bool        // :disable_safety — auto-allow every bash_safety ask
	safetyConfigured bool        // bash_safety enabled in the lua config (else the shell is unsafe by default)
	follow           bool        // stick the viewport to the bottom as new content streams in
	busy             bool
	canceling        bool // user pressed ctrl+c; emit a clean marker when the turn ends
	cancel           context.CancelFunc
	spinner          int
	spinning         bool // a spinnerTick chain is live (guards against duplicates)
	quitArmed        bool

	agentName     string
	statusMsg     string
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
	// Give every input row the same subtle background so the input reads as one
	// solid box. Each StyleState field inherits Base, but CursorLine carries its
	// own contrasting highlight by default — override it to match so the current
	// row isn't shaded differently. (No SetVirtualCursor/prompt-func: the stock
	// cursor stays visible.)
	tint := func(s textarea.StyleState) textarea.StyleState {
		s.Base = s.Base.Background(cInputBg)
		s.Text = s.Text.Background(cInputBg)
		s.Prompt = s.Prompt.Background(cInputBg)
		s.Placeholder = s.Placeholder.Background(cInputBg)
		s.EndOfBuffer = s.EndOfBuffer.Background(cInputBg)
		s.CursorLine = lipgloss.NewStyle().Background(cInputBg)
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
		agentName: agentName,
		statusMsg: statusMsg,
	}
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
	return tea.Batch(m.ta.Focus(), waitWake(m.wakeEvents), bgPollTick())
}

// bgPollTickMsg periodically refreshes the footer's subprocess count.
type bgPollTickMsg struct{}

// bgPollTick schedules the next subprocess-count refresh. The count drives the
// footer's "bg: N" pill and changes out-of-band (a subagent finishes, a bash_bg
// exits) with no event to react to, so a steady poll keeps it honest; 2s is
// invisible to the eye and cheap (a jobs-dir glob). The steady tick also lets the
// footer's timed notice fade when the app is otherwise idle.
func bgPollTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return bgPollTickMsg{} })
}

// openEditorMsg carries the result of composing a prompt in an external editor.
type openEditorMsg struct {
	text string
	err  error
}

// confirmReq is a bash_safety ask routed to the TUI: the Asker (on the turn
// goroutine) blocks on reply while the model shows a Yes/No modal.
type confirmReq struct {
	command string
	reason  string
	reply   chan bool
}

// confirmMsg delivers a confirmReq into the bubbletea loop via Program.Send.
type confirmMsg struct{ req *confirmReq }

// confirmAbortMsg tells the TUI to dismiss a pending confirm modal that the
// Asker has abandoned (its context was canceled — an ask_timeout fired, or the
// turn was canceled). Without it the modal would linger as a zombie that traps
// every keypress in handleConfirmKey for an ask already resolved as denied.
type confirmAbortMsg struct{ req *confirmReq }

// shellDoneMsg reports the result of a `:! <cmd>` terminal-handoff run.
type shellDoneMsg struct {
	cmd string
	err error
}

// resolveEditor returns the editor command string from $VISUAL/$EDITOR, falling
// back to nvim → vim → nano. The string may contain arguments and quoting; it is
// run through `sh -c` so spaces and flags are handled by the shell. "" if none.
func resolveEditor() string {
	for _, env := range []string{os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if strings.TrimSpace(env) != "" {
			return env
		}
	}
	for _, e := range []string{"nvim", "vim", "nano"} {
		if _, err := exec.LookPath(e); err == nil {
			return e
		}
	}
	return ""
}

// openEditor composes the current draft in $EDITOR. It seeds a temp .md file
// with the draft, suspends the TUI to run the editor, then loads the saved text
// back into the input. tea.ExecProcess handles releasing/restoring the terminal.
func (m *model) openEditor() tea.Cmd {
	ed := resolveEditor()
	if ed == "" {
		m.notice = "no editor found (set $EDITOR)"
		return nil
	}
	f, err := os.CreateTemp("", "shell3_prompt_*.md")
	if err != nil {
		m.notice = "editor: " + err.Error()
		return nil
	}
	path := f.Name()
	_, _ = f.WriteString(m.ta.Value())
	_ = f.Close()

	// Run via the shell so $EDITOR may carry args/quoting (e.g. `code --wait`,
	// `"/Apps/Sublime Text/subl" -w`); $1 is the temp file path.
	cmd := exec.Command("sh", "-c", ed+` "$1"`, "sh", path)
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		defer func() { _ = os.Remove(path) }()
		if runErr != nil {
			return openEditorMsg{err: runErr}
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return openEditorMsg{err: rerr}
		}
		return openEditorMsg{text: string(b)}
	})
}

func waitEvent(ch <-chan shell3.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		return eventMsg{ev: ev, ok: ok, ch: ch}
	}
}

// wakeMsg carries one out-of-turn HostEvent from the wake bus.
type wakeMsg struct {
	ev shell3.HostEvent
	ok bool
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

func (m *model) relayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.ta.SetWidth(m.width)
	// Cap the input's max height to fit this terminal — leave the footer plus a
	// few transcript rows — so a tall paste/draft can't overflow the layout and
	// freeze input. Content beyond this scrolls inside the textarea.
	maxIH := m.height - 2 - 3 // footer + blank spacer + at least 3 transcript rows
	if maxIH > inputMaxRows {
		maxIH = inputMaxRows
	}
	if maxIH < 1 {
		maxIH = 1
	}
	m.ta.MaxHeight = maxIH
	// DynamicHeight sizes the textarea itself; read it back for layout.
	ih := m.ta.Height()
	if ih < 1 {
		ih = 1
	}
	// footer (1) + one blank spacer line above the input (1).
	vpH := m.height - 2 - ih
	if vpH < 1 {
		vpH = 1
	}
	m.vp.SetWidth(m.width)
	m.vp.SetHeight(vpH)
	m.refresh(false)
}

// inputScrollIndicator is the one-line gutter above the input. It is blank
// unless the input has grown past its visible height, in which case it shows a
// dim, right-aligned ▲ (more above), ▼ (more below), or ▲▼ — so a long
// paste/draft doesn't silently hide content off the top or bottom.
//
// Overflow is measured by logical line count vs the visible height: the
// textarea's ScrollPercent is unreliable here (it pads its viewport content to
// the view height, so a fitting single line reports a non-1.0 percent and would
// show a spurious ▼). Logical lines undercount only when a line soft-wraps, so
// at worst an arrow is omitted — never shown for input that actually fits.
func (m *model) inputScrollIndicator() string {
	visible := m.ta.Height()
	off := m.ta.ScrollYOffset()
	total := m.ta.LineCount()
	above := off > 0
	below := total > off+visible
	if !above && !below {
		return ""
	}
	marker := "▼"
	switch {
	case above && below:
		marker = "▲▼"
	case above:
		marker = "▲"
	}
	return lipgloss.NewStyle().Width(m.width).Align(lipgloss.Right).Render(stFgDim.Render(marker))
}

// refresh rebuilds the viewport content. It preserves the scroll position in
// NORMAL (so line-scrolling and streaming don't fight); in INSERT it follows
// the bottom when already there (or forced).
func (m *model) refresh(forceBottom bool) {
	// Before any message, fill the viewport with the centered welcome card.
	if m.tr.count() == 0 {
		card := lipgloss.Place(m.vp.Width(), m.vp.Height(),
			lipgloss.Center, lipgloss.Center, m.welcomeCard())
		m.vp.SetContent(card)
		m.blockStarts = nil
		m.totalLines = 0
		m.cursorLine = 0
		return
	}
	off := m.vp.YOffset()
	selLo, selHi := -1, -1
	if m.hasSel {
		selLo, selHi = m.selRange()
	}
	content, starts, total, excluded := m.tr.renderBlocks(m.cursorLine, m.mode == modeNormal, m.vp.Width(), selLo, selHi)
	m.blockStarts = starts
	m.totalLines = total
	m.renderedLines = strings.Split(content, "\n")
	m.selExcluded = excluded
	if m.cursorLine >= total {
		m.cursorLine = total - 1
	}
	if m.cursorLine < 0 {
		m.cursorLine = 0
	}
	m.vp.SetContent(content)
	// follow locks the view to the bottom as content streams; navigation up
	// clears it (see moveLine/jumpBlock), G re-locks it.
	if forceBottom || m.follow {
		m.vp.GotoBottom()
	} else {
		m.vp.SetYOffset(off)
	}
}

// welcomeCard is the centered greeting shown in the viewport before the first
// message is sent. lipgloss.Place centers
// it within the viewport in refresh().
func (m *model) welcomeCard() string {
	title := stBrand.Render("๑ï shell3") + "  " + stDim.Render("/ˈʃɛli/")
	sub := stFgDim.Render("minimal Unix-composable coding agent")
	lines := []string{title, sub, ""}
	if m.agentName != "" {
		lines = append(lines, stUserPrompt.Render("agent")+"  "+stUserText.Render(m.agentName), "")
	}
	mode := func(name, desc string) string {
		return stUserPrompt.Render(fmt.Sprintf("%-8s", name)) + stFgDim.Render(desc)
	}
	lines = append(lines,
		mode("INSERT", "type, enter sends  ·  esc → NORMAL"),
		mode("NORMAL", "j/k move · click/enter folds · drag or y copies · i types"),
		mode("COMMAND", ": commands (:clear :agent :q …)"),
		"",
		stDim.Render("?")+stFgDim.Render(" help")+stDim.Render("   ·   tab")+stFgDim.Render(" switch agent"),
	)
	return lipgloss.NewStyle().
		Padding(1, 4).
		Render(strings.Join(lines, "\n"))
}

// blockAtLine maps a content line to the block (item index) that owns it.
func (m *model) blockAtLine(line int) int {
	b := 0
	for i, s := range m.blockStarts {
		if s <= line {
			b = i
		} else {
			break
		}
	}
	return b
}

// eventLine maps a mouse screen-Y to a transcript content-line index. inViewport
// is false when the event is below the transcript (in the input or footer). The
// viewport is the top region of the screen, so content line = YOffset + y.
func (m *model) eventLine(y int) (line int, inViewport bool) {
	if y < 0 || y >= m.vp.Height() {
		return 0, false
	}
	line = m.vp.YOffset() + y
	if line < 0 {
		line = 0
	}
	if m.totalLines > 0 && line >= m.totalLines {
		line = m.totalLines - 1
	}
	return line, true
}

// selRange returns the selection bounds low..high (inclusive), regardless of
// drag direction.
func (m *model) selRange() (lo, hi int) {
	lo, hi = m.selAnchor, m.selHead
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// handleMouse drives line-level selection, click-to-collapse, and wheel scroll.
// It is active in every mode — the mouse acts on the transcript while the
// keyboard does its mode-specific thing.
func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	e := msg.Mouse()
	// The :background modal owns the mouse while open: the wheel scrolls it, and
	// clicks/drags don't reach the (hidden) transcript underneath.
	if m.bgOpen {
		if _, ok := msg.(tea.MouseWheelMsg); ok {
			m.handleBackgroundWheel(e)
		}
		return m, nil
	}
	switch msg.(type) {
	case tea.MouseWheelMsg:
		return m.handleWheel(e)
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return m, nil
		}
		line, ok := m.eventLine(e.Y)
		if !ok {
			return m, nil
		}
		m.selecting = true
		m.dragged = false
		m.selAnchor = line
		m.selHead = line
		if m.hasSel { // starting a new gesture clears the old highlight
			m.hasSel = false
			m.refresh(false)
		}
		return m, nil
	case tea.MouseMotionMsg:
		if !m.selecting || e.Button != tea.MouseLeft {
			return m, nil
		}
		// Scroll first when the drag reaches an edge, then map the pointer (clamped
		// into the viewport) against the new offset — so the selection end tracks
		// content scrolling under it instead of freezing one line short.
		m.edgeScroll(e.Y)
		y := e.Y
		if y < 0 {
			y = 0
		}
		if y >= m.vp.Height() {
			y = m.vp.Height() - 1
		}
		if line, ok := m.eventLine(y); ok {
			m.selHead = line
		}
		// Only a span beyond the anchor line is a drag; a jittery click that stays
		// on one line still folds (decided on release).
		if m.selHead != m.selAnchor {
			m.dragged = true
			m.hasSel = true
		}
		m.refresh(false)
		return m, nil
	case tea.MouseReleaseMsg:
		if !m.selecting {
			return m, nil
		}
		m.selecting = false
		if m.dragged && m.selHead != m.selAnchor {
			return m.finishSelection()
		}
		return m.handleClick(e.Y)
	}
	return m, nil
}

// selectedText returns the plain text of the current selection: the flattened
// lines in range, with the 2-cell gutter and all ANSI stripped and trailing
// spaces trimmed per line. Empty when there is no selection.
func (m *model) selectedText() string {
	if !m.hasSel || len(m.renderedLines) == 0 {
		return ""
	}
	lo, hi := m.selRange()
	var b strings.Builder
	for i := lo; i <= hi && i < len(m.renderedLines); i++ {
		// Copy exactly what is highlighted: excluded lines (banner, system
		// reminders, the thinking indicator, inter-block separators) are not
		// highlighted, so they are not copied either (WYSIWYG). Thinking *content*
		// stays selectable; only its indicator line is excluded.
		if i < len(m.selExcluded) && m.selExcluded[i] {
			continue
		}
		s := ansi.Strip(m.renderedLines[i])
		r := []rune(s)
		if len(r) >= 2 {
			r = r[2:] // drop the gutter ("▌ " or "  ")
		}
		b.WriteString(strings.TrimRight(string(r), " "))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// finishSelection copies the dragged selection and reports it. The highlight
// stays visible until the next click so the user sees what was copied.
func (m *model) finishSelection() (tea.Model, tea.Cmd) {
	text := m.selectedText()
	if text == "" {
		return m, nil
	}
	n := strings.Count(text, "\n") + 1
	m.notice = fmt.Sprintf("copied %d line(s)", n)
	return m, copyToClipboard(text)
}

// handleClick handles a plain (non-drag) left click: toggle a foldable block at
// the clicked line, otherwise just clear any existing selection. A click always
// dismisses the prior selection highlight.
func (m *model) handleClick(y int) (tea.Model, tea.Cmd) {
	m.hasSel = false
	line, ok := m.eventLine(y)
	if !ok {
		m.refresh(false)
		return m, nil
	}
	b := m.blockAtLine(line)
	if b >= 0 && b < len(m.tr.items) && m.tr.items[b].foldable() {
		m.tr.ToggleFold(b)
	}
	m.refresh(false)
	return m, nil
}

// handleWheel scrolls the transcript viewport. Scrolling up breaks autoscroll
// (follow); reaching the bottom re-engages it.
func (m *model) handleWheel(e tea.Mouse) (tea.Model, tea.Cmd) {
	switch e.Button {
	case tea.MouseWheelUp:
		m.vp.ScrollUp(3)
		m.follow = false
	case tea.MouseWheelDown:
		m.vp.ScrollDown(3)
		m.syncFollow()
	}
	return m, nil
}

// edgeScroll nudges the viewport by one line when a drag reaches the top or
// bottom edge, so a selection can extend past the visible page (edge-scroll
// during drag). The caller refreshes after, preserving the new offset, and the
// next motion event at the edge maps to a further line.
func (m *model) edgeScroll(y int) {
	switch {
	case y <= 0:
		m.vp.ScrollUp(1)
		m.follow = false
	case y >= m.vp.Height()-1:
		m.vp.ScrollDown(1)
	}
}

// syncFollow re-derives the autoscroll lock from the viewport: new output is
// followed only while the view is parked at the bottom.
func (m *model) syncFollow() { m.follow = m.vp.AtBottom() }

// ensureLineVisible scrolls the viewport so the cursor line is on screen.
func (m *model) ensureLineVisible() {
	off, h := m.vp.YOffset(), m.vp.Height()
	if m.cursorLine < off {
		m.vp.SetYOffset(m.cursorLine)
	} else if m.cursorLine >= off+h {
		m.vp.SetYOffset(m.cursorLine - h + 1)
	}
}

// moveLine moves the line cursor by d, redraws, and keeps it visible.
func (m *model) moveLine(d int) {
	m.cursorLine += d
	if m.cursorLine < 0 {
		m.cursorLine = 0
	}
	if m.cursorLine >= m.totalLines {
		m.cursorLine = m.totalLines - 1
	}
	m.follow = false // navigating: don't let refresh yank to the bottom
	m.refresh(false)
	m.ensureLineVisible()
	m.syncFollow()
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
			m.bgCount = len(m.cmds.Jobs()) // seed the footer count before the first poll
		}
		m.relayout()
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
			m.bgCount = len(m.cmds.Jobs())
		}
		return m, bgPollTick()
	case eventMsg:
		return m.handleEvent(msg)
	case wakeMsg:
		if !msg.ok {
			return m, nil // bus closed
		}
		return m, tea.Batch(m.handleWake(msg.ev), waitWake(m.wakeEvents))
	case openEditorMsg:
		return m.handleEditorResult(msg)
	case confirmMsg:
		if m.safetyOff {
			msg.req.reply <- true // :disable_safety — auto-allow, no modal
			return m, nil
		}
		m.confirm = msg.req
		m.confirmYes = true // default to Yes so a quick Enter allows
		return m, nil
	case confirmAbortMsg:
		// Dismiss only if this is still the same pending ask: a user keypress may
		// have resolved (and replaced/cleared) it just before the abort arrived.
		if m.confirm == msg.req {
			m.confirm = nil
			m.notice = "bash_safety prompt timed out — command denied"
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

// handleEditorResult loads the externally-composed prompt back into the input.
func (m *model) handleEditorResult(msg openEditorMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "editor: " + msg.err.Error()
		return m, nil
	}
	text := strings.TrimRight(msg.text, "\n")
	// An empty save keeps the existing draft rather than silently wiping it.
	if strings.TrimSpace(text) == "" {
		m.mode = modeInsert
		m.notice = "editor returned empty — draft kept"
		return m, m.ta.Focus()
	}
	m.ta.SetValue(text)
	m.mode = modeInsert
	m.relayout()
	m.notice = "draft loaded from editor"
	return m, m.ta.Focus()
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

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := msg.String()

	// A pending bash_safety ask is modal and takes priority over everything.
	if m.confirm != nil {
		return m.handleConfirmKey(s)
	}

	// The help overlay is modal: any key dismisses it.
	if m.helpOpen {
		m.helpOpen = false
		return m, nil
	}

	// The :background modal owns every key while open (so esc/q close it and
	// ctrl+c can't arm quit underneath).
	if m.bgOpen {
		return m.handleBackgroundKey(s)
	}

	// Ctrl+C is global: cancel a running turn, else require a second press.
	if s == "ctrl+c" {
		if m.busy && m.cancel != nil {
			m.cancel()
			m.canceling = true
			m.notice = "cancelling…"
			return m, nil
		}
		if m.quitArmed {
			return m, tea.Quit
		}
		m.quitArmed = true
		m.notice = "press ctrl+c again to quit"
		return m, nil
	}
	if m.quitArmed {
		m.quitArmed = false
		m.notice = ""
	}

	// esc also dismisses a mouse selection (without early-returning, so it still
	// does its mode-specific work below).
	if s == "esc" && m.hasSel {
		m.hasSel = false
		m.refresh(false)
	}

	switch m.mode {
	case modeCommand:
		return m.handleCommandKey(s)
	case modeNormal:
		return m.handleNormalKey(s)
	default:
		return m.handleInsertKey(msg, s)
	}
}

// handleConfirmKey drives the Yes/No bash_safety modal. Enter confirms the
// selected button (Yes by default); y/n are shortcuts; esc/ctrl+c deny.
func (m *model) handleConfirmKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "left", "h":
		m.confirmYes = true
	case "right", "l":
		m.confirmYes = false
	case "tab":
		m.confirmYes = !m.confirmYes
	case "y", "Y":
		return m.resolveConfirm(true)
	case "n", "N", "esc", "ctrl+c":
		return m.resolveConfirm(false)
	case "enter", " ":
		return m.resolveConfirm(m.confirmYes)
	}
	return m, nil
}

// resolveConfirm replies to the blocked Asker and dismisses the modal.
func (m *model) resolveConfirm(allow bool) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		m.confirm.reply <- allow // buffered; never blocks
		m.confirm = nil
		if allow {
			m.notice = "command allowed"
		} else {
			m.notice = "command denied"
		}
		m.refresh(false)
	}
	return m, nil
}

func (m *model) handleInsertKey(msg tea.KeyPressMsg, s string) (tea.Model, tea.Cmd) {
	switch s {
	case "enter":
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			return m, nil
		}
		// Mid-turn: queue the message as steering instead of refusing it. The
		// session delivers it at the next round boundary (or we auto-run it when
		// the turn ends — see handleEvent).
		if m.busy {
			if m.steer == nil {
				return m, nil
			}
			m.steer(text)
			m.tr.AddSteer(text)
			m.ta.Reset()
			m.relayout()
			m.follow = true
			m.refresh(true)
			m.notice = "steering queued"
			return m, nil
		}
		m.tr.AddUser(text)
		m.ta.Reset()
		m.relayout()
		m.follow = true // stick to the bottom to watch the reply stream in
		m.refresh(true)
		m.busy = true
		m.notice = ""
		ch, cancel := m.send(text)
		m.cancel = cancel
		return m, tea.Batch(waitEvent(ch), m.startSpin())
	case "?":
		// On an empty input, '?' opens help (matching the footer hint) rather
		// than typing a literal '?'. With text present it inserts normally.
		if strings.TrimSpace(m.ta.Value()) == "" {
			m.helpOpen = true
			return m, nil
		}
	case "ctrl+u", "shift+backspace":
		// Clear the whole input draft. ctrl+u is the reliable binding; most
		// terminals can't distinguish shift+backspace from plain backspace
		// (same 0x7f byte), so we intercept ctrl+u before the textarea sees it.
		m.ta.Reset()
		m.relayout()
		m.notice = "input cleared"
		return m, nil
	case "ctrl+o":
		// Compose the draft in $EDITOR (same as :edit).
		return m, m.openEditor()
	case "esc":
		// Leave the draft intact; just switch to NORMAL (use shift+backspace or
		// dd to clear).
		m.enterNormal()
		return m, nil
	case "tab":
		// Tab cycles agents (gated while busy); never inserts a literal tab.
		m.cycleAgent()
		return m, nil
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.syncFollow()
		return m, cmd
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	m.relayout()
	return m, cmd
}

// cycleAgent switches to the next agent in the registry (Tab). No-op while busy
// (a turn owns the agent) or when fewer than two agents exist.
func (m *model) cycleAgent() {
	if m.busy || m.cmds == nil {
		return
	}
	names := m.cmds.AgentNames()
	if len(names) < 2 {
		return
	}
	cur := 0
	active := m.cmds.ActiveAgent()
	for i, n := range names {
		if n == active {
			cur = i
			break
		}
	}
	next := names[(cur+1)%len(names)]
	if err := m.cmds.SwitchAgent(next); err != nil {
		m.notice = "error: " + err.Error()
		return
	}
	m.applyAgent()
	m.notice = "agent: " + m.agentName
}

// applyAgent refreshes the agent name and status line (model label, context
// window) from a fresh snapshot after the active agent changes.
func (m *model) applyAgent() {
	snap := m.cmds.Snapshot()
	m.agentName = snap.Agent
	m.statusMsg = snap.StatusLine
	m.contextWindow = snap.ContextWindow
}

func (m *model) enterNormal() {
	m.mode = modeNormal
	m.ta.Blur()
	m.ta.Placeholder = "press i to type" // hint: NORMAL doesn't capture text
	m.cursorLine = 1 << 30               // clamp to last line in refresh
	m.refresh(false)
	m.ensureLineVisible()
}

func (m *model) enterInsert() tea.Cmd {
	m.mode = modeInsert
	m.ta.Placeholder = ""
	m.refresh(false)
	return m.ta.Focus()
}

func (m *model) handleNormalKey(s string) (tea.Model, tea.Cmd) {
	// esc cancels any pending prefix.
	if s == "esc" {
		m.pending = 0
		return m, nil
	}

	// Multi-key prefixes: gg, zM, zR, dd.
	if m.pending != 0 {
		p := m.pending
		m.pending = 0
		switch {
		case p == 'g' && s == "g":
			m.cursorLine = 0
			m.follow = false
			m.refresh(false)
			m.vp.SetYOffset(0)
		case p == 'z' && s == "M":
			m.tr.FoldAll(true)
			m.refresh(false)
		case p == 'z' && s == "R":
			m.tr.FoldAll(false)
			m.refresh(false)
		case p == 'd' && s == "d":
			// dd clears the input draft.
			m.ta.Reset()
			m.relayout()
			m.notice = "input cleared"
		}
		return m, nil
	}

	switch s {
	case "g", "z", "d":
		m.pending = rune(s[0])
	case "y":
		m.notice = "yanked to clipboard"
		return m, copyToClipboard(m.tr.raw(m.blockAtLine(m.cursorLine)))
	case "j", "down":
		m.moveLine(1) // move the line cursor (scrolls only at the edge)
	case "k", "up":
		m.moveLine(-1)
	case "}":
		m.jumpBlock(1) // next block
	case "{":
		m.jumpBlock(-1) // previous block
	case "G":
		// G / shift+g jumps to the bottom AND locks autoscroll (follow).
		m.cursorLine = m.totalLines - 1
		m.follow = true
		m.refresh(false)
		m.vp.GotoBottom()
		m.notice = "following"
	case "ctrl+d":
		m.moveLine(m.vp.Height() / 2)
	case "ctrl+u":
		m.moveLine(-m.vp.Height() / 2)
	case "enter", " ":
		b := m.blockAtLine(m.cursorLine)
		if m.tr.ToggleFold(b) {
			m.refresh(false)
			if b < len(m.blockStarts) {
				m.cursorLine = m.blockStarts[b]
			}
			m.refresh(false)
			m.ensureLineVisible()
		}
	case "tab":
		m.cycleAgent()
	case "i", "a":
		return m, m.enterInsert()
	case ":":
		m.mode = modeCommand
		m.cmdline = ""
	case "?":
		m.helpOpen = true
	}
	return m, nil
}

// jumpBlock moves the line cursor to the start of the adjacent block. Pressing
// } while already on the last block jumps to the very bottom (and re-locks
// autoscroll), so a final long block can be reached without line-stepping.
func (m *model) jumpBlock(d int) {
	cur := m.blockAtLine(m.cursorLine)
	if d > 0 && cur >= len(m.blockStarts)-1 {
		m.cursorLine = m.totalLines - 1
		m.follow = true
		m.refresh(false)
		m.vp.GotoBottom()
		return
	}
	b := cur + d
	if b < 0 {
		b = 0
	}
	if b >= len(m.blockStarts) {
		b = len(m.blockStarts) - 1
	}
	if b >= 0 && b < len(m.blockStarts) {
		m.cursorLine = m.blockStarts[b]
	}
	m.follow = false
	m.refresh(false)
	m.ensureLineVisible()
	m.syncFollow()
}

func (m *model) handleCommandKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "enter":
		line := strings.TrimSpace(m.cmdline)
		m.cmdline = ""
		m.mode = modeNormal
		return m, m.runCommand(line)
	case "esc":
		m.cmdline = ""
		m.mode = modeNormal
		m.refresh(false)
		return m, nil
	case "tab":
		m.completeCommand()
		return m, nil
	case "backspace":
		if m.cmdline != "" {
			r := []rune(m.cmdline)
			m.cmdline = string(r[:len(r)-1])
		} else {
			m.mode = modeNormal
			m.refresh(false)
		}
		return m, nil
	default:
		if len(s) == 1 { // printable
			m.cmdline += s
		}
		return m, nil
	}
}

// completeCommand Tab-completes the command word against the palette: a single
// match fills it in; several extend to their longest common prefix. No-op once
// you've started typing an argument (a space is present).
func (m *model) completeCommand() {
	if m.cmdline == "" || strings.Contains(m.cmdline, " ") {
		return
	}
	q := strings.ToLower(m.cmdline)
	var matches []string
	for _, c := range exCommands {
		if strings.HasPrefix(c.name, q) {
			matches = append(matches, c.name)
		}
	}
	switch {
	case len(matches) == 1:
		m.cmdline = matches[0]
	case len(matches) > 1:
		if lcp := longestCommonPrefix(matches); len(lcp) > len(m.cmdline) {
			m.cmdline = lcp
		}
	}
}

func longestCommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	pre := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, pre) {
			pre = pre[:len(pre)-1]
			if pre == "" {
				return ""
			}
		}
	}
	return pre
}

// cmdInfo prints a one-line result into the transcript and refreshes. Shared by
// every ":" command handler.
func (m *model) cmdInfo(s string) { m.tr.AddInfo(s); m.refresh(true) }

// runCommand executes a ":" command by dispatching to its exCommands entry (the
// single source of truth). Returns the entry's tea.Cmd (e.g. tea.Quit for :q).
func (m *model) runCommand(line string) tea.Cmd {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	parts := strings.SplitN(line, " ", 2)
	name := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	c := findCommand(name)
	// An unknown name, or a session-only command with no session attached, both
	// surface as "unknown command" (the latter matches the prior behavior).
	if c == nil || (c.session && m.cmds == nil) {
		m.cmdInfo("unknown command: " + name)
		return nil
	}
	return c.run(m, arg)
}

func (m *model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // capture mouse for select/copy + click-to-collapse
	v.WindowTitle = "shell3"
	if !m.ready || m.width <= 0 {
		return v
	}
	// Render the input first: ta.View() refreshes the textarea's internal scroll
	// state, which inputScrollIndicator then reads for the same frame (no 1-frame
	// lag on the ▲/▼ markers).
	taView := m.ta.View()
	base := lipgloss.JoinVertical(lipgloss.Left,
		m.vp.View(),
		m.inputScrollIndicator(), // blank, or ▲/▼ when the input scrolls off-screen
		taView,
		m.renderFooter(),
	)
	switch {
	case m.confirm != nil:
		base = m.placeModal(m.confirmBox())
	case m.helpOpen:
		base = m.placeModal(m.helpBox())
	case m.bgOpen:
		base = m.placeModal(m.backgroundBox())
	case m.mode == modeCommand:
		// The command palette floats just above the input so the typed line in
		// the footer stays visible.
		base = overlayAbove(base, m.commandPalette(), m.vp.Height())
	}
	v.Content = base
	return v
}

// placeModal centers a modal box on the otherwise-blank screen, replacing the
// transcript while the modal is open.
func (m *model) placeModal(box string) string {
	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center, box)
}

// minModalWidth is the floor for any modal's content width; below this a box
// becomes too cramped to read.
const minModalWidth = 40

// modalWidth clamps a preferred modal content width to [minModalWidth,max] and
// to what the terminal can actually fit, so a narrow window never overflows the
// edge.
func (m *model) modalWidth(preferred, max int) int {
	w := preferred
	if w < minModalWidth {
		w = minModalWidth
	}
	if w > max {
		w = max
	}
	if fit := m.width - 4; w > fit {
		w = fit
	}
	if w < 1 {
		w = 1
	}
	return w
}

// exCommand is one ":" command — the SINGLE source of truth for the command
// palette, the help overlay, AND dispatch (runCommand). Adding a command here is
// all it takes for it to be handled, completed, listed in the palette, and shown
// in help — there are no parallel lists to keep in sync.
type exCommand struct {
	name    string
	aliases []string
	args    string
	desc    string
	// session marks commands that need a live session (m.cmds); they degrade to
	// "unknown command" when none is attached.
	session bool
	run     func(m *model, arg string) tea.Cmd
}

var exCommands = []exCommand{
	{name: "!", args: "<cmd>", desc: "run a shell command (terminal handoff)", run: func(m *model, arg string) tea.Cmd {
		if arg == "" {
			m.cmdInfo("usage: :! <command>")
			return nil
		}
		return tea.ExecProcess(exec.Command("bash", "-c", arg), func(err error) tea.Msg {
			return shellDoneMsg{cmd: arg, err: err}
		})
	}},
	{name: "compact", desc: "summarize history now to free context", session: true, run: func(m *model, _ string) tea.Cmd {
		m.cmds.QueueCompact()
		m.cmdInfo("compaction queued — runs before your next turn")
		return nil
	}},
	{name: "clear", desc: "reset the conversation context", session: true, run: func(m *model, _ string) tea.Cmd {
		if err := m.cmds.Clear(); err != nil {
			m.cmdInfo("error: " + err.Error())
		} else {
			m.cmdInfo("context cleared")
		}
		return nil
	}},
	{name: "rollback", desc: "undo the last turn", session: true, run: func(m *model, _ string) tea.Cmd {
		ok, err := m.cmds.Rollback()
		switch {
		case err != nil:
			m.cmdInfo("error: " + err.Error())
		case !ok:
			m.cmdInfo("nothing to roll back")
		default:
			m.cmdInfo("last turn removed")
		}
		return nil
	}},
	{name: "prune", args: "<id>", desc: "drop a tool result by id", session: true, run: func(m *model, arg string) tea.Cmd {
		if arg == "" {
			m.cmdInfo("usage: :prune <tool_call_id>")
		} else {
			out, _ := m.cmds.Prune(arg)
			m.cmdInfo(out)
		}
		return nil
	}},
	{name: "usage", desc: "show token usage", session: true, run: func(m *model, _ string) tea.Cmd {
		usage := fmt.Sprintf("tokens: %d total", m.tokens)
		if m.promptTokens > 0 || m.completTokens > 0 {
			usage += fmt.Sprintf(" (prompt %d · completion %d)", m.promptTokens, m.completTokens)
		}
		if m.contextWindow > 0 {
			usage += fmt.Sprintf(" · context window %d", m.contextWindow)
		}
		m.cmdInfo(usage)
		return nil
	}},
	{name: "prompt", desc: "print the system prompt", session: true, run: func(m *model, _ string) tea.Cmd {
		m.cmdInfo("system prompt:\n" + strings.TrimSpace(m.cmds.Snapshot().SystemPrompt))
		return nil
	}},
	{name: "p", aliases: []string{"edit"}, desc: "compose the draft in $EDITOR (:edit, ctrl+o)", run: func(m *model, _ string) tea.Cmd {
		return m.openEditor()
	}},
	{name: "agent", args: "<name>", desc: "switch agent (blank = list)", session: true, run: func(m *model, arg string) tea.Cmd {
		switch {
		case arg == "":
			m.cmdInfo("agents: " + strings.Join(m.cmds.AgentNames(), ", "))
		default:
			if err := m.cmds.SwitchAgent(arg); err != nil {
				m.cmdInfo("error: " + err.Error())
			} else {
				m.applyAgent()
				m.cmdInfo("switched to agent: " + m.agentName)
			}
		}
		return nil
	}},
	{name: "agents", desc: "list agents", session: true, run: func(m *model, _ string) tea.Cmd {
		m.cmdInfo("agents: " + strings.Join(m.cmds.AgentNames(), ", "))
		return nil
	}},
	{name: "info", desc: "session info", session: true, run: func(m *model, _ string) tea.Cmd {
		snap := m.cmds.Snapshot()
		m.cmdInfo(fmt.Sprintf("agent: %s · %s", snap.Agent, snap.StatusLine))
		return nil
	}},
	{name: "background", aliases: []string{"bg", "jobs"}, desc: "list & kill background jobs", session: true, run: func(m *model, _ string) tea.Cmd {
		m.openBackground()
		return nil
	}},
	{name: "disable_safety", aliases: []string{"safety"}, desc: "toggle auto-allow for bash_safety (!)", run: func(m *model, _ string) tea.Cmd {
		m.safetyOff = !m.safetyOff
		if m.safetyOff {
			m.cmdInfo("bash_safety asks auto-allowed (!) — run :disable_safety again to re-enable")
		} else {
			m.cmdInfo("bash_safety prompts re-enabled")
		}
		return nil
	}},
	{name: "help", desc: "show keys & commands", run: func(m *model, _ string) tea.Cmd {
		m.helpOpen = true
		return nil
	}},
	{name: "q", aliases: []string{"quit"}, desc: "quit", run: func(m *model, _ string) tea.Cmd {
		return tea.Quit
	}},
}

// commandRefLines renders the ":" command reference from exCommands, grouped
// perLine tokens per line for the help overlay. Single source: same list the
// palette and runCommand use.
func commandRefLines(perLine int) []string {
	toks := make([]string, 0, len(exCommands))
	for _, c := range exCommands {
		t := ":" + c.name
		if c.args != "" {
			t += " " + c.args
		}
		toks = append(toks, t)
	}
	var lines []string
	for i := 0; i < len(toks); i += perLine {
		end := min(i+perLine, len(toks))
		lines = append(lines, " "+strings.Join(toks[i:end], "   "))
	}
	return lines
}

// findCommand resolves a command name (or alias) to its exCommand, or nil.
func findCommand(name string) *exCommand {
	for i := range exCommands {
		c := &exCommands[i]
		if c.name == name || slices.Contains(c.aliases, name) {
			return c
		}
	}
	return nil
}

// commandPalette renders the filtered ":" command list shown in COMMAND mode.
func (m *model) commandPalette() string {
	key := lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	desc := lipgloss.NewStyle().Foreground(cFgDim)
	q := strings.ToLower(strings.TrimSpace(m.cmdline))
	rows := []string{stBrand.Render("commands")}
	any := false
	for _, c := range exCommands {
		if q != "" && !strings.HasPrefix(c.name, q) {
			continue
		}
		any = true
		label := ":" + c.name
		if c.args != "" {
			label += " " + c.args
		}
		rows = append(rows, key.Render(fmt.Sprintf(" %-16s", label))+desc.Render(c.desc))
	}
	if !any {
		rows = append(rows, desc.Render(" (no match)"))
	}
	return lipgloss.NewStyle().
		Padding(0, 1).
		Render(strings.Join(rows, "\n"))
}

// overlayAbove pastes box's lines onto base ending at row vpBottom-1 (left
// aligned), so a panel can float just above the input row.
func overlayAbove(base, box string, vpBottom int) string {
	bl := strings.Split(base, "\n")
	xl := strings.Split(box, "\n")
	start := vpBottom - len(xl)
	if start < 0 {
		start = 0
	}
	for i, line := range xl {
		if r := start + i; r >= 0 && r < len(bl) {
			bl[r] = line
		}
	}
	return strings.Join(bl, "\n")
}

// confirmBox renders the bash_safety Yes/No modal, Yes selected by default.
func (m *model) confirmBox() string {
	selStyle := lipgloss.NewStyle().Foreground(cBlack).Background(cPrimary).Bold(true).Padding(0, 2)
	offStyle := lipgloss.NewStyle().Foreground(cFgDim).Padding(0, 2)
	yes, no := offStyle.Render("Yes"), offStyle.Render("No")
	if m.confirmYes {
		yes = selStyle.Render("Yes")
	} else {
		no = selStyle.Render("No")
	}
	// Content width capped so the box (content + 4-col padding) never exceeds the
	// terminal. Every long/variable line is hard-wrapped to it — a long command,
	// echoed into the gate's reason, otherwise overflows the screen.
	contentW := m.modalWidth(m.width/2, 72)
	if contentW > m.width-4 {
		contentW = m.width - 4
	}
	if contentW < 1 {
		contentW = 1
	}
	wrapLines := func(s string) []string {
		return strings.Split(ansi.Wrap(s, contentW, " "), "\n")
	}
	// Vertical budget so the whole box stays within the screen. Fixed chrome is the
	// header + three blank separators + the buttons row + the footer (6 lines),
	// plus vertical padding (2) = 8. The reason (short — it names the matched rule)
	// gets up to 3 lines; the command gets the rest. Both are truncated with a
	// "… +N more lines" marker rather than overflowing.
	bodyBudget := m.height - 8
	if bodyBudget < 2 {
		bodyBudget = 2
	}
	var reasonKept []string
	reasonMore := 0
	if m.confirm.reason != "" {
		rb := min(3, bodyBudget-1)
		reasonKept, reasonMore = clampLines(wrapLines(m.confirm.reason), rb)
	}
	cmdBudget := bodyBudget - len(reasonKept) - boolToInt(reasonMore > 0)
	if cmdBudget < 1 {
		cmdBudget = 1
	}
	cmdKept, cmdMore := clampLines(wrapLines(m.confirm.command), cmdBudget)

	lines := []string{
		stErr.Render("⚠ bash_safety") + stDim.Render("  allow this command?"),
		"",
		lipgloss.NewStyle().Foreground(cUser).Render(strings.Join(cmdKept, "\n")),
	}
	if cmdMore > 0 {
		lines = append(lines, stDim.Render(fmt.Sprintf("… +%d more lines", cmdMore)))
	}
	if len(reasonKept) > 0 {
		lines = append(lines, stDim.Render(strings.Join(reasonKept, "\n")))
		if reasonMore > 0 {
			lines = append(lines, stDim.Render(fmt.Sprintf("… +%d more lines", reasonMore)))
		}
	}
	lines = append(lines,
		"",
		yes+"  "+no,
		"",
		stDim.Render(ansi.Wrap("y/enter allow · n/esc deny · ←→ select", contentW, " ")),
	)
	return lipgloss.NewStyle().
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
}

// clampLines caps a wrapped block to maxLines, reserving the last line for the
// "… +N more lines" marker the caller renders. Returns the kept lines and the
// count of dropped lines (0 when nothing was truncated).
func clampLines(lines []string, maxLines int) (kept []string, more int) {
	if maxLines < 1 {
		maxLines = 1
	}
	if len(lines) <= maxLines {
		return lines, 0
	}
	return lines[:maxLines-1], len(lines) - (maxLines - 1)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// helpBox renders the keybinding/command reference shown by '?'.
func (m *model) helpBox() string {
	key := lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	desc := lipgloss.NewStyle().Foreground(cFgDim)
	head := lipgloss.NewStyle().Foreground(cSage).Bold(true)
	row := func(k, d string) string {
		return key.Render(fmt.Sprintf(" %-12s", k)) + desc.Render(d)
	}
	lines := []string{
		stBrand.Render("shell3 — keys"),
		"",
		head.Render("NORMAL"),
		row("j / k", "move cursor"),
		row("{ / }", "previous / next block"),
		row("gg / G", "top / bottom (G locks autoscroll)"),
		row("ctrl+d / u", "half-page down / up"),
		row("enter", "fold / unfold block"),
		row("zM / zR", "fold / unfold all"),
		row("y / dd", "copy block / clear input"),
		row("mouse", "drag selects + copies · click folds · wheel scrolls"),
		row("i / a", "insert mode"),
		row("tab", "cycle agent (any mode)"),
		row(": / ?", "command mode / help"),
		"",
		head.Render("INSERT"),
		row("enter", "send"),
		row("ctrl+j", "newline (shift+enter if supported)"),
		row("ctrl+o", "compose in $EDITOR (:edit)"),
		row("ctrl+u", "clear input"),
		row("esc", "normal mode (keeps draft)"),
		"",
		head.Render("COMMAND"),
	}
	// Command reference derived from exCommands (the single source of truth), so
	// it can never drift from what the palette lists and runCommand handles.
	for _, l := range commandRefLines(4) {
		lines = append(lines, desc.Render(l))
	}
	lines = append(lines,
		"",
		desc.Render(" ctrl+c: cancel turn / quit   ·   any key: close"),
	)
	return lipgloss.NewStyle().
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// modelLabel pulls just the model out of a "provider │ model │ effort" status
// line, so the footer can show it dim next to the agent pill.
func modelLabel(status string) string {
	if status == "" {
		return ""
	}
	parts := strings.Split(status, "│")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) >= 2 {
		return parts[1]
	}
	return parts[0]
}

func (m *model) renderFooter() string {
	if m.mode == modeCommand {
		return stModeCommand.Render(":" + m.cmdline + "█")
	}
	var mode string
	switch m.mode {
	case modeNormal:
		mode = stModeNormal.Render(" N ")
	default:
		mode = stModeInsert.Render(" I ")
	}
	// Left: mode pill, then the model with its context-window fill (ctx: x%), then
	// the transient last-action notice (primary, auto-hidden after noticeTTL), then
	// the live turn state (quit-armed prompt / thinking shimmer).
	left := mode
	if model := modelLabel(m.statusMsg); model != "" {
		// Context-window fill sits right after the model name.
		if m.tokens > 0 && m.contextWindow > 0 {
			model += fmt.Sprintf("  (ctx: %d%%)", m.tokens*100/m.contextWindow)
		}
		left += " " + stDim.Render(model)
	}
	switch {
	case m.quitArmed:
		// Ctrl+C once: red middle bar telling you to press again.
		left += " " + stCtrlCArmed.Render(" press ctrl+c again to quit ")
	default:
		if n := m.activeNotice(); n != "" {
			left += " " + stNotice.Render(n)
		}
		if m.busy {
			// Thinking: white text on an animated rainbow background (no spinner).
			left += " " + rainbowBg(" thinking ", m.spinner)
		}
	}

	// Right side, left-to-right: "? help" hint (only at rest), the "!" danger pill
	// when the shell is unsafe, the live subprocess count (bg: N), then the brand
	// snail glued to the active agent badge (Tab cycles the agent).
	var seg []string
	if strings.TrimSpace(m.ta.Value()) == "" {
		seg = append(seg, stDim.Render("? help"))
	}
	// "!" when the shell is unsafe: runtime :disable_safety, or bash_safety not
	// enabled in the lua config (unsafe by default).
	if m.safetyOff || !m.safetyConfigured {
		seg = append(seg, stYolo.Render(" ! "))
	}
	if m.bgCount > 0 {
		seg = append(seg, stBgCount.Render(fmt.Sprintf(" bg: %d ", m.bgCount)))
	}
	// Snail brand + agent badge form one visual unit (no gap between them).
	badge := stSnail.Render(" ๑ï ")
	if m.agentName != "" {
		badge += agentBadge(m.agentName)
	}
	seg = append(seg, badge)

	right := strings.Join(seg, "  ")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left // no room; drop the right side rather than wrap
	}
	return left + strings.Repeat(" ", gap) + right
}
