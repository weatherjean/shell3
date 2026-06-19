package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	SwitchAgent(name string) error
	AgentNames() []string
	ActiveAgent() string
	Snapshot() shell3.Snapshot
	// HasQueuedInput reports whether steering text interjected during a turn is
	// still waiting (it arrived too late for an in-turn round boundary), so the
	// model can auto-run a follow-up turn once the current one ends.
	HasQueuedInput() bool
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
	pending     rune // multi-key prefix in NORMAL (g, y, z, d)

	helpOpen   bool
	confirm    *confirmReq // pending bash_safety ask modal (nil = none)
	confirmYes bool        // which button is selected (default Yes)
	safetyOff  bool        // :disable_safety — auto-allow every bash_safety ask
	follow     bool        // stick the viewport to the bottom as new content streams in
	busy       bool
	canceling  bool // user pressed ctrl+c; emit a clean marker when the turn ends
	cancel     context.CancelFunc
	spinner    int
	spinning   bool // a spinnerTick chain is live (guards against duplicates)
	quitArmed  bool

	agentName     string
	statusMsg     string
	tokens        int
	promptTokens  int
	completTokens int
	contextWindow int
	notice        string
}

func newModel(send func(string) (<-chan shell3.Event, context.CancelFunc), cmds sessionCmds, agentName, statusMsg string) *model {
	// Line numbers off, a "›" prompt, unlimited length, dynamic height up to
	// inputMaxRows, and custom newline keys; everything else stays default.
	ta := textarea.New()
	ta.Prompt = "› "
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

	return &model{
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
}

func (m *model) Init() tea.Cmd { return tea.Batch(m.ta.Focus(), waitWake(m.wakeEvents)) }

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
	maxIH := m.height - 1 - 3 // footer + at least 3 transcript rows
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
	vpH := m.height - 1 - ih // footer only (no top header)
	if vpH < 1 {
		vpH = 1
	}
	m.vp.SetWidth(m.width)
	m.vp.SetHeight(vpH)
	m.refresh(false)
}

// refresh rebuilds the viewport content. It preserves the scroll position in
// NORMAL (so line-scrolling and streaming don't fight); in INSERT it follows
// the bottom when already there (or forced).
func (m *model) refresh(forceBottom bool) {
	// Before any message, fill the viewport with the centered welcome card.
	if m.tr.count() == 0 {
		card := lipgloss.Place(m.vp.Width(), m.vp.Height(),
			lipgloss.Center, lipgloss.Center, m.welcomeCard(),
			lipgloss.WithWhitespaceChars("/"),
			lipgloss.WithWhitespaceStyle(stSlashBg))
		m.vp.SetContent(card)
		m.blockStarts = nil
		m.totalLines = 0
		m.cursorLine = 0
		return
	}
	off := m.vp.YOffset()
	content, starts, total := m.tr.renderBlocks(m.cursorLine, m.mode == modeNormal, m.vp.Width())
	m.blockStarts = starts
	m.totalLines = total
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
		mode("NORMAL", "j/k move · enter folds · yy copy · i types"),
		mode("COMMAND", ": commands (:clear :agent :q …)"),
		"",
		stDim.Render("?")+stFgDim.Render(" help")+stDim.Render("   ·   tab")+stFgDim.Render(" switch agent"),
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cPrimary).
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

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.relayout()
		return m, nil
	case spinnerTickMsg:
		if m.busy {
			m.spinner++
			return m, spinnerTick()
		}
		m.spinning = false // chain ends when the turn is no longer busy
		return m, nil
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
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	if m.mode == modeInsert {
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
	case "right", "l", "tab":
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

	// Multi-key prefixes: gg, yy, zM, zR, dd.
	if m.pending != 0 {
		p := m.pending
		m.pending = 0
		switch {
		case p == 'g' && s == "g":
			m.cursorLine = 0
			m.follow = false
			m.refresh(false)
			m.vp.SetYOffset(0)
		case p == 'y' && s == "y":
			m.notice = "yanked to clipboard"
			return m, tea.SetClipboard(m.tr.raw(m.blockAtLine(m.cursorLine)))
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
	case "g", "y", "z", "d":
		m.pending = rune(s[0])
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

// runCommand executes a : command. Returns tea.Quit for :q.
func (m *model) runCommand(line string) tea.Cmd {
	if line == "" {
		return nil
	}
	parts := strings.SplitN(line, " ", 2)
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	info := func(s string) { m.tr.AddInfo(s); m.refresh(true) }

	switch cmd {
	case "q", "quit":
		return tea.Quit
	case "edit", "p":
		// Compose the draft in $EDITOR (doesn't need a session).
		return m.openEditor()
	case "disable_safety", "safety":
		// Toggle auto-allow for bash_safety asks (the "!" indicator).
		m.safetyOff = !m.safetyOff
		if m.safetyOff {
			info("bash_safety asks auto-allowed (!) — run :disable_safety again to re-enable")
		} else {
			info("bash_safety prompts re-enabled")
		}
		return nil
	case "help":
		info("commands: :clear :rollback :prune <id> :usage :prompt :edit :agent <name> :agents :info :q")
		return nil
	}
	if m.cmds == nil {
		info("unknown command: " + cmd)
		return nil
	}
	switch cmd {
	case "clear":
		if err := m.cmds.Clear(); err != nil {
			info("error: " + err.Error())
		} else {
			info("context cleared")
		}
	case "rollback":
		ok, err := m.cmds.Rollback()
		switch {
		case err != nil:
			info("error: " + err.Error())
		case !ok:
			info("nothing to roll back")
		default:
			info("last turn removed")
		}
	case "prune":
		if arg == "" {
			info("usage: :prune <tool_call_id>")
		} else {
			out, _ := m.cmds.Prune(arg)
			info(out)
		}
	case "usage":
		usage := fmt.Sprintf("tokens: %d total", m.tokens)
		if m.promptTokens > 0 || m.completTokens > 0 {
			usage += fmt.Sprintf(" (prompt %d · completion %d)", m.promptTokens, m.completTokens)
		}
		if m.contextWindow > 0 {
			usage += fmt.Sprintf(" · context window %d", m.contextWindow)
		}
		info(usage)
	case "prompt":
		snap := m.cmds.Snapshot()
		info("system prompt:\n" + strings.TrimSpace(snap.SystemPrompt))
	case "agent":
		if arg == "" {
			info("agents: " + strings.Join(m.cmds.AgentNames(), ", "))
		} else if err := m.cmds.SwitchAgent(arg); err != nil {
			info("error: " + err.Error())
		} else {
			m.applyAgent()
			info("switched to agent: " + m.agentName)
		}
	case "agents":
		info("agents: " + strings.Join(m.cmds.AgentNames(), ", "))
	case "info":
		snap := m.cmds.Snapshot()
		info(fmt.Sprintf("agent: %s · %s", snap.Agent, snap.StatusLine))
	default:
		info("unknown command: " + cmd)
	}
	return nil
}

func (m *model) View() tea.View {
	var v tea.View
	v.AltScreen = true // mouse not captured → native select+copy
	v.WindowTitle = "shell3"
	if !m.ready || m.width <= 0 {
		return v
	}
	base := lipgloss.JoinVertical(lipgloss.Left,
		m.vp.View(),
		m.ta.View(),
		m.renderFooter(),
	)
	switch {
	case m.confirm != nil:
		base = m.placeModal(m.confirmBox())
	case m.helpOpen:
		base = m.placeModal(m.helpBox())
	case m.mode == modeCommand:
		// The command palette floats just above the input so the typed line in
		// the footer stays visible.
		base = overlayAbove(base, m.commandPalette(), m.vp.Height())
	}
	v.Content = base
	return v
}

// placeModal centers a modal box on a dim "/" field (lipgloss fills the
// surrounding whitespace), replacing the transcript while the modal is open.
func (m *model) placeModal(box string) string {
	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center, box,
		lipgloss.WithWhitespaceChars("/"),
		lipgloss.WithWhitespaceStyle(stSlashBg))
}

// modalWidth clamps a preferred modal content width to [min,max] and to what
// the terminal can actually fit, so a narrow window never overflows the border.
func (m *model) modalWidth(preferred, min, max int) int {
	w := preferred
	if w < min {
		w = min
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

// exCommand is one ":" command for the palette + help.
type exCommand struct{ name, args, desc string }

var exCommands = []exCommand{
	{"clear", "", "reset the conversation context"},
	{"rollback", "", "undo the last turn"},
	{"prune", "<id>", "drop a tool result by id"},
	{"usage", "", "show token usage"},
	{"prompt", "", "print the system prompt"},
	{"p", "", "compose the draft in $EDITOR (:edit, ctrl+o)"},
	{"agent", "<name>", "switch agent (blank = list)"},
	{"agents", "", "list agents"},
	{"info", "", "session info"},
	{"disable_safety", "", "toggle auto-allow for bash_safety (!)"},
	{"help", "", "command help"},
	{"q", "", "quit"},
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
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cPrimary).
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
	boxW := m.modalWidth(m.width/2, 30, 72)
	lines := []string{
		stErr.Render("⚠ bash_safety") + stDim.Render("  allow this command?"),
		"",
		lipgloss.NewStyle().Foreground(cUser).Width(boxW).Render(m.confirm.command),
	}
	if m.confirm.reason != "" {
		lines = append(lines, stDim.Render(m.confirm.reason))
	}
	lines = append(lines,
		"",
		yes+"  "+no,
		"",
		stDim.Render("y / enter: allow   ·   n / esc: deny   ·   ← →: select"),
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cRed).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
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
		row("yy / dd", "copy block / clear input"),
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
		desc.Render(" :clear  :rollback  :prune <id>  :usage"),
		desc.Render(" :prompt  :agent <name>  :agents  :info  :q"),
		"",
		desc.Render(" ctrl+c: cancel turn / quit   ·   any key: close"),
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cPrimary).
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
		mode = stModeNormal.Render(" NORMAL ")
	default:
		mode = stModeInsert.Render(" INSERT ")
	}
	var parts []string
	switch {
	case m.quitArmed:
		// Ctrl+C once: red middle bar telling you to press again.
		parts = append(parts, stCtrlCArmed.Render(" press ctrl+c again to quit "))
	case m.busy:
		// Thinking: white text on an animated rainbow background (no spinner).
		parts = append(parts, rainbowBg(" thinking ", m.spinner))
	}
	if m.tokens > 0 {
		tok := fmt.Sprintf("t:%d", m.tokens)
		// Show how full the context window is, when known.
		if m.contextWindow > 0 {
			tok += fmt.Sprintf("/%d (%d%%)", m.contextWindow, m.tokens*100/m.contextWindow)
		}
		parts = append(parts, stDim.Render(tok))
	}
	// notice is shown red in the quitArmed bar above; don't duplicate it here.
	if m.notice != "" && !m.quitArmed {
		parts = append(parts, stFgDim.Render(m.notice))
	}
	// Not-at-bottom indicator: scrolled up while content sits below (#6).
	if m.totalLines > m.vp.Height() && !m.vp.AtBottom() {
		parts = append(parts, stChevron.Render("↓ G to follow"))
	}
	left := mode + " " + strings.Join(parts, stDim.Render("  ·  "))

	// Right side: "? help" hint (only at rest), the model (dim), then the active
	// agent badge (Tab cycles it). The agent is named once — just the pill.
	var right string
	if strings.TrimSpace(m.ta.Value()) == "" {
		right = stDim.Render("? help")
	}
	if model := modelLabel(m.statusMsg); model != "" {
		if right != "" {
			right += "  "
		}
		// Green "!" when bash_safety asks are auto-allowed (:disable_safety).
		if m.safetyOff {
			right += stYolo.Render(" ! ") + " "
		}
		right += stDim.Render(model)
	}
	if m.agentName != "" {
		if right != "" {
			right += " "
		}
		right += stAgent.Render(" " + m.agentName + " ")
	}
	if right == "" {
		return left
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left // no room; drop the right side rather than wrap
	}
	return left + strings.Repeat(" ", gap) + right
}
