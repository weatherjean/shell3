package tui

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/tui/dialog"
)

func nanoNow() int64 { return time.Now().UnixNano() }

// Model is the root BubbleTea model for shell3.
//
// Layout (top to bottom):
//
//	viewport  fills the remaining space
//	hints     1 line: contextual key hints
//	╭──────╮  border + dynamic textarea
//	│ > _  │
//	╰──────╯
//	status    1 line: nvim-style status bar (app name, model, tokens)
//
// Content layers inside the viewport:
//
//	history  completed rendered turns (immutable while streaming)
//	nonLLM   current turn's non-LLM parts (labels, tool output)
//	llmBuf   current turn's LLM text — rendered with glamour on TurnDoneMsg
type Model struct {
	viewport   viewport.Model
	input      textarea.Model
	inputLines int // current textarea height in lines (drives viewport resize)
	appName    string
	statusMsg  string // provider · model · personality  (updated via StatusMsg)
	modeLabel  string // "c", "a", or "cst" — set once at startup
	tokenCount int    // updated after each turn
	history    string
	nonLLM     *strings.Builder
	llmBuf     *strings.Builder
	ready      bool
	width      int
	height     int
	// busy blocks input from the moment the user submits until the turn
	// fully completes (TurnDoneMsg / TurnErrMsg / shellDoneMsg). This closes
	// the gap where m.streaming is false before the first ChunkMsg arrives.
	busy      bool
	streaming bool // true once the first ChunkMsg is received
	cancelFn  func()
	submitFn  func(string) tea.Cmd

	lastEscTime   int64 // UnixNano of most recent Esc keypress (double-esc to clear)
	lastCtrlCTime int64 // UnixNano of most recent Ctrl+C when idle (double ctrl+c to quit)

	// pendingStreamNext holds the next ReadCh command while a TTYExecMsg is
	// being serviced. The model resumes the stream on resumeStreamMsg.
	pendingStreamNext tea.Cmd

	spinner spinner.Model
	overlay *dialog.Overlay
}

// New returns an initialized Model.
// appName is displayed on the left of the header.
// statusMsg is displayed in the middle (provider, model).
// modeLabel is one of "c" (code), "a" (agent), "cst" (custom).
// submitFn is called when the user submits a message.
func New(appName, statusMsg, modeLabel string, submitFn func(string) tea.Cmd) Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 12
	ta.SetWidth(80) // updated on first WindowSizeMsg

	// Normalize focused/blurred appearance — no highlights, no dim text.
	s := ta.Styles()
	plain := lipgloss.NewStyle()
	s.Focused.CursorLine = plain
	s.Blurred.CursorLine = plain
	s.Blurred.Text = plain // prevent dim-gray text when input briefly blurs
	ta.SetStyles(s)

	ta.SetVirtualCursor(false)
	ta.Focus() //nolint:errcheck

	sp := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(colorMuted)),
	)

	return Model{
		input:      ta,
		inputLines: 1,
		appName:    appName,
		statusMsg:  statusMsg,
		modeLabel:  modeLabel,
		submitFn:   submitFn,
		nonLLM:     new(strings.Builder),
		llmBuf:     new(strings.Builder),
		spinner:    sp,
		overlay:    dialog.NewOverlay(),
	}
}

// ── viewport content helpers ──────────────────────────────────────────────────

func (m *Model) viewContent() string {
	if m.history == "" && m.nonLLM.Len() == 0 && m.llmBuf.Len() == 0 {
		return renderWelcome(m.width)
	}
	return m.history + m.nonLLM.String() + wrapToWidth(m.llmBuf.String(), m.width)
}

func (m *Model) refreshViewport() {
	if m.ready {
		m.viewport.SetContent(m.viewContent())
		m.viewport.GotoBottom()
	}
}

func (m *Model) finalizeTurn(usage llm.Usage) {
	if !m.busy {
		return
	}
	// Flush any remaining LLM text (final call had no trailing tool output).
	if m.llmBuf.Len() > 0 {
		m.nonLLM.WriteString(renderMarkdown(m.llmBuf.String(), m.width))
		m.llmBuf.Reset()
	}
	m.history += m.nonLLM.String()
	m.nonLLM.Reset()
	m.streaming = false
	m.busy = false
	m.cancelFn = nil
	if usage.TotalTokens > 0 {
		m.tokenCount = usage.TotalTokens
	}
	m.refreshViewport()
}

func (m *Model) abortTurn(reason string) {
	if !m.busy {
		return
	}
	content := strings.TrimRight(m.nonLLM.String()+m.llmBuf.String(), "\n")
	if content != "" {
		m.history += content + "\n"
	}
	m.history += dimStyle.Render("["+reason+"]") + "\n"
	m.nonLLM.Reset()
	m.llmBuf.Reset()
	m.streaming = false
	m.busy = false
	m.cancelFn = nil
	m.refreshViewport()
}

// ── BubbleTea interface ───────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), func() tea.Msg { return m.spinner.Tick() })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Border takes 2 columns (left │ + right │).
		m.input.SetWidth(m.width - 2)
		vpHeight := m.calcViewportHeight()
		if !m.ready {
			m.viewport = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(vpHeight))
			m.viewport.SetContent(m.viewContent())
			m.ready = true
		} else {
			m.viewport.SetWidth(m.width)
			m.viewport.SetHeight(vpHeight)
		}

	case SetCancelMsg:
		m.cancelFn = msg.Cancel

	case resumeStreamMsg:
		// TTY exec completed; resume draining the stream channel.
		if m.pendingStreamNext != nil {
			cmds = append(cmds, m.pendingStreamNext)
			m.pendingStreamNext = nil
		}

	case shellDoneMsg:
		if msg.errMsg != "" {
			m.nonLLM.WriteString(msg.errMsg)
			m.refreshViewport()
		}
		m.busy = false
		m.streaming = false
		cmds = append(cmds, m.input.Focus())

	// AppendMsg / StatusMsg may arrive directly from slash commands (not via
	// the stream channel), so handle them at the top level too.
	case OpenDialogMsg:
		m.overlay.Open(msg.Dialog)

	case AppendMsg:
		m.nonLLM.WriteString(wrapToWidth(string(msg), m.width))
		m.refreshViewport()

	case StatusMsg:
		m.statusMsg = string(msg)

	case streamMsg:
		inner := msg.msg
		turnDone := false
		switch v := inner.(type) {
		case ChunkMsg:
			m.llmBuf.WriteString(string(v))
			m.streaming = true // busy was set on submit; streaming set on first chunk
			m.refreshViewport()

		case TurnDoneMsg:
			turnDone = true
			m.finalizeTurn(v.Usage)

		case TurnErrMsg:
			turnDone = true
			errMsg := v.Err.Error()
			if strings.Contains(errMsg, "context canceled") {
				m.abortTurn("cancelled")
			} else {
				m.history += m.nonLLM.String() + m.llmBuf.String()
				m.history += "\n" + errorStyle.Render("[error: "+errMsg+"]") + "\n"
				m.nonLLM.Reset()
				m.llmBuf.Reset()
				m.streaming = false
				m.busy = false
				m.cancelFn = nil
				m.refreshViewport()
			}

		case AppendMsg:
			// Commit any in-flight LLM text before appending non-LLM content,
			// so tool output follows the text that preceded it in the stream.
			if m.llmBuf.Len() > 0 {
				m.nonLLM.WriteString(renderMarkdown(m.llmBuf.String(), m.width))
				m.llmBuf.Reset()
			}
			m.nonLLM.WriteString(wrapToWidth(string(v), m.width))
			m.refreshViewport()

		case StatusMsg:
			m.statusMsg = string(v)

		case TTYExecMsg:
			// Suspend TUI and hand terminal to the requested command.
			// The stream goroutine is blocked on v.ReplyC; we resume on completion.
			m.pendingStreamNext = msg.next
			c := exec.Command("bash", "-c", v.Cmd)
			if v.WorkDir != "" {
				c.Dir = v.WorkDir
			}
			return m, tea.Exec(newExecCmd(c), func(err error) tea.Msg {
				result := "(completed)"
				if err != nil {
					result = "error: " + err.Error()
				}
				v.ReplyC <- result
				return resumeStreamMsg{}
			})
		}

		if msg.next != nil && !turnDone {
			cmds = append(cmds, msg.next)
		}

	case tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		if !m.busy {
			var taCmd tea.Cmd
			m.input, taCmd = m.input.Update(msg)
			cmds = append(cmds, taCmd)
			m.updateInputSize()
		}

	case tea.KeyPressMsg:
		// Overlay consumes key events when a dialog is open.
		if m.overlay.HasDialogs() {
			if action := m.overlay.Update(msg); action != nil {
				switch a := action.(type) {
				case dialog.ActionClose:
					m.overlay.CloseFront()
				case dialog.ActionCmd:
					m.overlay.CloseFront()
					if a.Cmd != nil {
						cmds = append(cmds, a.Cmd)
					}
				}
			}
			return m, tea.Batch(cmds...)
		}

		switch {
		case msg.Code == 'c' && msg.Mod&tea.ModCtrl != 0:
			if m.busy && m.cancelFn != nil {
				m.cancelFn()
				m.abortTurn("cancelled")
			} else if !m.busy {
				const doubleCtrlCNs = 500_000_000
				now := nanoNow()
				if now-m.lastCtrlCTime < doubleCtrlCNs {
					return m, tea.Quit
				}
				m.lastCtrlCTime = now
			}

		case msg.Code == tea.KeyEsc && !m.busy:
			const doubleEscNs = 500_000_000
			now := nanoNow()
			if now-m.lastEscTime < doubleEscNs {
				m.input.Reset()
				m.lastEscTime = 0
			} else {
				m.lastEscTime = now
			}

		// shift+enter or alt+enter: insert newline into textarea
		case msg.Code == tea.KeyEnter && (msg.Mod&tea.ModShift != 0 || msg.Mod&tea.ModAlt != 0) && !m.busy:
			m.input.InsertString("\n")
			m.updateInputSize()

		// plain enter: submit
		case msg.Code == tea.KeyEnter && msg.Mod == 0 && !m.busy:
			input := strings.TrimSpace(m.input.Value())
			if input != "" {
				m.input.Reset()
				m.updateInputSize()
				if strings.HasPrefix(input, "!") {
					m.busy = true
					m.input.Blur()
					c := exec.Command("bash", "-c", input[1:])
					cmds = append(cmds, tea.Exec(newExecCmd(c), func(err error) tea.Msg {
						if err != nil {
							return shellDoneMsg{errMsg: "\n" + errorStyle.Render("[exit: "+err.Error()+"]") + "\n"}
						}
						return shellDoneMsg{}
					}))
				} else {
					// slash commands (/help etc.) are instant — don't set busy
					if !strings.HasPrefix(input, "/") {
						m.busy = true
						m.input.Blur()
					}
					// flush pending slash-command output before anchoring "you:" in history
					if m.nonLLM.Len() > 0 {
						m.history += m.nonLLM.String()
						m.nonLLM.Reset()
					}
					userMsg := strings.TrimRight(renderMarkdown(input, m.width), "\n")
					sep := separatorStyle.Render(strings.Repeat("─", m.width))
					m.history += sep + "\n" + userLabelStyle.Render("you:") + " " + userMsg + "\n" + sep + "\n"
					m.refreshViewport()
					cmds = append(cmds, m.submitFn(input))
				}
			}

		default:
			if !m.busy {
				var taCmd tea.Cmd
				m.input, taCmd = m.input.Update(msg)
				cmds = append(cmds, taCmd)
				m.updateInputSize()
			}
		}
	}

	// Re-focus input when no longer busy (e.g. after a slash command response).
	if !m.busy && !m.input.Focused() {
		cmds = append(cmds, m.input.Focus())
	}

	// Always tick the spinner so it stays alive.
	var spCmd tea.Cmd
	m.spinner, spCmd = m.spinner.Update(msg)
	cmds = append(cmds, spCmd)

	if m.ready {
		switch msg.(type) {
		case tea.KeyPressMsg, tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg:
			// block keyboard/paste events from leaking into viewport scroll
		default:
			var vpCmd tea.Cmd
			m.viewport, vpCmd = m.viewport.Update(msg)
			cmds = append(cmds, vpCmd)
		}
	}

	return m, tea.Batch(cmds...)
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	if !m.ready {
		v.Content = "initializing…"
		return v
	}
	w := m.width
	if w == 0 {
		w = 80
	}

	inputBox := m.renderInputBox(w)
	statusBar := m.renderStatusBar(w)

	if m.overlay.HasDialogs() {
		// Modal is active: dialog fills the viewport area, input is hidden.
		dialogH := m.height - 1 // reserve 1 line for status bar
		v.Content = m.overlay.View(w, dialogH) + "\n" + statusBar
		return v
	}

	vp := m.viewport.View()
	thinkingLine := m.renderThinkingLine(w)
	v.Content = vp + "\n" +
		thinkingLine + "\n" +
		inputBox + "\n" +
		statusBar

	// Position the real terminal cursor inside the input box.
	// Layout rows: viewport + thinking(1) + border-top(1) + content.
	if cur := m.input.Cursor(); cur != nil {
		cur.X += 1                            // border left
		cur.Y += m.viewport.Height() + 2     // viewport rows + thinking + border-top
		v.Cursor = cur
	}

	return v
}

// calcViewportHeight returns how many lines the viewport may occupy.
func (m Model) calcViewportHeight() int {
	h := m.height - m.inputLines - 4 // border-top(1) + border-bottom(1) + status(1) + thinking(1)
	if h < 1 {
		h = 1
	}
	return h
}

// updateInputSize syncs inputLines with the textarea's current height and
// resizes the viewport accordingly.
func (m *Model) updateInputSize() {
	newLines := m.input.Height()
	if newLines < 1 {
		newLines = 1
	}
	if newLines == m.inputLines && m.ready {
		return
	}
	m.inputLines = newLines
	if m.ready {
		vpH := m.height - m.inputLines - 4
		if vpH < 1 {
			vpH = 1
		}
		m.viewport.SetHeight(vpH)
	}
	m.refreshViewport()
}

// ── Sub-renders ───────────────────────────────────────────────────────────────

func (m Model) renderThinkingLine(_ int) string {
	if m.busy {
		label := dimStyle.Render(" thinking")
		return " " + m.spinner.View() + label
	}
	return ""
}

func (m Model) renderInputBox(w int) string {
	style := inputBorderIdle
	if m.busy {
		style = inputBorderStreaming
	}
	if w < 1 {
		w = 1
	}
	return style.Width(w).Render(m.input.View())
}

// renderStatusBar renders the nvim/tmux-style full-width status bar at the bottom.
func (m Model) renderStatusBar(w int) string {
	barStyle := statusBarNormal
	appStyle := statusBarAppName
	if m.busy {
		barStyle = statusBarStreaming
		appStyle = statusBarAppNameStreaming
	}

	// Left: app name badge.
	left := appStyle.Render(" shell3 ")

	// Mode badge (far right).
	var modeBadge string
	switch m.modeLabel {
	case "c":
		modeBadge = modeBadgeCode.Render(" c ")
	case "a":
		modeBadge = modeBadgeAgent.Render(" a ")
	default:
		modeBadge = modeBadgeCustom.Render(" cst ")
	}

	// Hints + mode badge.
	var right string
	if m.busy {
		right = statusBarHintStyle.Render("  ") +
			statusBarHintKeyStyle.Render("ctrl+c") +
			statusBarHintStyle.Render(" cancel  ") +
			modeBadge
	} else {
		right = statusBarHintStyle.Render("  ") +
			statusBarHintKeyStyle.Render("/h") +
			statusBarHintStyle.Render(" help  ") +
			modeBadge
	}

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	// Budget available for the middle section — never let it overflow.
	midBudget := w - leftW - rightW
	if midBudget < 0 {
		midBudget = 0
	}

	// Build middle: provider/model + optional ctx token count, truncated to budget.
	statusText := " " + m.statusMsg + " "
	if m.tokenCount > 0 {
		statusText += fmt.Sprintf("│ ctx: %d ", m.tokenCount)
	}
	// Truncate if needed so status bar never wraps.
	if len(statusText) > midBudget {
		statusText = statusText[:midBudget]
	}
	mid := barStyle.Render(statusText)
	midW := lipgloss.Width(mid)

	padW := midBudget - midW
	if padW < 0 {
		padW = 0
	}

	return left + mid + barStyle.Render(strings.Repeat(" ", padW)) + right
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// wrapToWidth soft-wraps lines in s that exceed width display columns.
// ANSI escape codes are accounted for via lipgloss.Width.
func wrapToWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		for lipgloss.Width(line) > width {
			// Binary-search for the byte position where display width hits the limit.
			lo, hi := 0, len(line)
			for lo < hi {
				mid := (lo + hi + 1) / 2
				if lipgloss.Width(line[:mid]) <= width {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			out = append(out, line[:lo])
			line = line[lo:]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func renderMarkdown(text string, width int) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if width <= 0 {
		width = 100
	}
	style := styles.DarkStyleConfig
	style.Document.Color = nil // inherit terminal default so color codes don't bleed across viewport lines
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width-2),
	)
	if err != nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return out
}

// execCmd wraps *exec.Cmd to implement tea.ExecCommand for TTY handoff.
type execCmd struct{ c *exec.Cmd }

func newExecCmd(c *exec.Cmd) *execCmd { return &execCmd{c} }

func (e *execCmd) SetStdin(r io.Reader)  { e.c.Stdin = r }
func (e *execCmd) SetStdout(w io.Writer) { e.c.Stdout = w }
func (e *execCmd) SetStderr(w io.Writer) { e.c.Stderr = w }
func (e *execCmd) Run() error            { return e.c.Run() }
