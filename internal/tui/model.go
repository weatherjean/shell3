package tui

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/weatherjean/shell3/internal/llm"
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
}

// New returns an initialized Model.
// appName is displayed on the left of the header.
// statusMsg is displayed on the right (provider, model, personality).
// submitFn is called when the user submits a message.
func New(appName, statusMsg string, submitFn func(string) tea.Cmd) Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 12
	ta.SetWidth(80) // updated on first WindowSizeMsg

	// Remove yellow cursor-line highlight.
	s := ta.Styles()
	plain := lipgloss.NewStyle()
	s.Focused.CursorLine = plain
	s.Blurred.CursorLine = plain
	ta.SetStyles(s)

	ta.SetVirtualCursor(false)
	ta.Focus() //nolint:errcheck

	return Model{
		input:      ta,
		inputLines: 1,
		appName:    appName,
		statusMsg:  statusMsg,
		submitFn:   submitFn,
		nonLLM:     new(strings.Builder),
		llmBuf:     new(strings.Builder),
	}
}

// ── viewport content helpers ──────────────────────────────────────────────────

func (m *Model) viewContent() string {
	if m.history == "" && m.nonLLM.Len() == 0 && m.llmBuf.Len() == 0 {
		return renderWelcome(m.width)
	}
	return m.history + m.nonLLM.String() + m.llmBuf.String()
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
	rendered := renderMarkdown(m.llmBuf.String(), m.width)
	m.history += m.nonLLM.String() + rendered
	m.nonLLM.Reset()
	m.llmBuf.Reset()
	m.streaming = false
	m.busy = false
	m.cancelFn = nil
	if usage.TotalTokens > 0 {
		base := strings.SplitN(m.statusMsg, " · tokens:", 2)[0]
		m.statusMsg = fmt.Sprintf("%s · tokens: %d", base, usage.TotalTokens)
	}
	m.refreshViewport()
}

func (m *Model) abortTurn(reason string) {
	if !m.busy {
		return
	}
	m.history += m.nonLLM.String() + m.llmBuf.String()
	m.history += dimStyle.Render("\n["+reason+"]\n")
	m.nonLLM.Reset()
	m.llmBuf.Reset()
	m.streaming = false
	m.busy = false
	m.cancelFn = nil
	m.refreshViewport()
}

// ── BubbleTea interface ───────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return m.input.Focus()
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
	case AppendMsg:
		m.nonLLM.WriteString(string(msg))
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
				m.history += errorStyle.Render("\n[error: "+errMsg+"]\n")
				m.nonLLM.Reset()
				m.llmBuf.Reset()
				m.streaming = false
				m.busy = false
				m.cancelFn = nil
				m.refreshViewport()
			}

		case AppendMsg:
			m.nonLLM.WriteString(string(v))
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
							return shellDoneMsg{errMsg: errorStyle.Render("\n[exit: " + err.Error() + "]\n")}
						}
						return shellDoneMsg{}
					}))
				} else {
					// slash commands (/help etc.) are instant — don't set busy
					if !strings.HasPrefix(input, "/") {
						m.busy = true
						m.input.Blur()
					}
					m.history += "\n" + userLabelStyle.Render("you:") + " " + input + "\n"
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

	if m.ready {
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true

	if !m.ready {
		v.Content = "initializing…"
		return v
	}
	w := m.width
	if w == 0 {
		w = 80
	}

	vp := m.viewport.View()
	inputBox := m.renderInputBox(w)
	statusBar := m.renderStatusBar(w)

	v.Content = vp + "\n" +
		inputBox + "\n" +
		statusBar

	// Position the real terminal cursor inside the input box.
	// Layout rows: 0..vpHeight-1 = viewport, vpHeight = border-top, vpHeight+1.. = content.
	if cur := m.input.Cursor(); cur != nil {
		cur.X += 1                            // border left
		cur.Y += m.viewport.Height() + 1     // viewport rows + border-top
		v.Cursor = cur
	}

	return v
}

// calcViewportHeight returns how many lines the viewport may occupy.
func (m Model) calcViewportHeight() int {
	h := m.height - m.inputLines - 3 // border-top(1) + border-bottom(1) + status(1)
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
		vpH := m.height - m.inputLines - 3
		if vpH < 1 {
			vpH = 1
		}
		m.viewport.SetHeight(vpH)
	}
	m.refreshViewport()
}

// ── Sub-renders ───────────────────────────────────────────────────────────────

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

	// Middle: provider/model/personality info.
	mid := ""
	if m.statusMsg != "" {
		mid = barStyle.Render(" " + m.statusMsg + " ")
	}

	// Right: key hints on darker background.
	var right string
	if m.busy {
		right = statusBarHintStyle.Render("  ") +
			statusBarHintKeyStyle.Render("ctrl+c") +
			statusBarHintStyle.Render(" cancel  ")
	} else {
		right = statusBarHintStyle.Render("  ") +
			statusBarHintKeyStyle.Render("^c^c") +
			statusBarHintStyle.Render(" quit") +
			statusBarHintStyle.Render("   ") +
			statusBarHintKeyStyle.Render("/help") +
			statusBarHintStyle.Render(" commands  ")
	}

	leftW := lipgloss.Width(left)
	midW := lipgloss.Width(mid)
	rightW := lipgloss.Width(right)
	padW := w - leftW - midW - rightW
	if padW < 0 {
		padW = 0
	}

	return left + mid + barStyle.Render(strings.Repeat(" ", padW)) + right
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// ShellLabel returns a styled "shell3:" prefix for assistant responses.
func ShellLabel() string {
	return "\n" + assistantLabelStyle.Render("shell3:") + "\n"
}

func renderMarkdown(text string, width int) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if width <= 0 {
		width = 100
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
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
