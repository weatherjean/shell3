package tui

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/weatherjean/shell3/internal/llm"
)

func nanoNow() int64 { return time.Now().UnixNano() }

// Model is the root BubbleTea model for shell3.
//
// Inline rendering — no alt screen, no viewport.
// Completed content is printed to terminal scrollback via tea.Println.
// View() renders only the sticky bottom: streaming preview + thinking + input + status.
type Model struct {
	input      textarea.Model
	inputLines int
	appName    string
	statusMsg  string
	modeLabel  string
	tokenCount int
	llmBuf     *strings.Builder // in-flight LLM text shown in View() while streaming
	ready      bool
	width      int
	height     int // kept for dialog height calculation
	busy       bool
	streaming  bool
	cancelFn   func()
	submitFn   func(string) tea.Cmd

	lastEscTime   int64
	lastCtrlCTime int64

	pendingStreamNext tea.Cmd

	spinner spinner.Model
}

func New(appName, statusMsg, modeLabel string, submitFn func(string) tea.Cmd) Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 12
	ta.SetWidth(80)

	s := ta.Styles()
	plain := lipgloss.NewStyle()
	s.Focused.CursorLine = plain
	s.Blurred.CursorLine = plain
	s.Blurred.Text = plain
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
		llmBuf:     new(strings.Builder),
		spinner:    sp,
	}
}

// ── turn lifecycle ────────────────────────────────────────────────────────────

func (m *Model) finalizeTurn(usage llm.Usage) tea.Cmd {
	var cmds []tea.Cmd
	if m.llmBuf.Len() > 0 {
		cmds = append(cmds, tea.Println(indentContent(renderMarkdown(m.llmBuf.String(), m.width-2))))
		m.llmBuf.Reset()
	}
	m.streaming = false
	m.busy = false
	m.cancelFn = nil
	if usage.TotalTokens > 0 {
		m.tokenCount = usage.TotalTokens
	}
	return tea.Batch(cmds...)
}

func (m *Model) abortTurn(reason string) tea.Cmd {
	var cmds []tea.Cmd
	if content := strings.TrimRight(m.llmBuf.String(), "\n"); content != "" {
		cmds = append(cmds, tea.Println(content))
	}
	m.llmBuf.Reset()
	m.streaming = false
	m.busy = false
	m.cancelFn = nil
	cmds = append(cmds, tea.Println(dimStyle.Render("["+reason+"]")))
	return tea.Batch(cmds...)
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
		m.input.SetWidth(m.width - 2)
		if !m.ready {
			cmds = append(cmds, tea.Println(renderWelcome(m.width)))
			m.ready = true
		}
		m.updateInputSize()

	case SetCancelMsg:
		m.cancelFn = msg.Cancel

	case resumeStreamMsg:
		if m.pendingStreamNext != nil {
			cmds = append(cmds, m.pendingStreamNext)
			m.pendingStreamNext = nil
		}

	case shellDoneMsg:
		if msg.errMsg != "" {
			cmds = append(cmds, tea.Println(msg.errMsg))
		}
		m.busy = false
		m.streaming = false
		cmds = append(cmds, m.input.Focus())

	case RunCmd:
		if msg.Cmd != nil {
			cmds = append(cmds, msg.Cmd)
		}

	case AppendMsg:
		cmds = append(cmds, tea.Println(indentContent(wrapToWidth(string(msg), m.width-2))))

	case StatusMsg:
		m.statusMsg = string(msg)

	case streamMsg:
		inner := msg.msg
		turnDone := false
		switch v := inner.(type) {
		case ChunkMsg:
			m.llmBuf.WriteString(string(v))
			m.streaming = true

		case TurnDoneMsg:
			turnDone = true
			cmds = append(cmds, m.finalizeTurn(v.Usage))

		case TurnErrMsg:
			turnDone = true
			errMsg := v.Err.Error()
			if strings.Contains(errMsg, "context canceled") {
				cmds = append(cmds, m.abortTurn("cancelled"))
			} else {
				if content := strings.TrimRight(m.llmBuf.String(), "\n"); content != "" {
					cmds = append(cmds, tea.Println(content))
				}
				m.llmBuf.Reset()
				m.streaming = false
				m.busy = false
				m.cancelFn = nil
				cmds = append(cmds, tea.Println(errorStyle.Render("[error: "+errMsg+"]")))
			}

		case AppendMsg:
			// Commit in-flight LLM text before tool output so ordering is preserved.
			if m.llmBuf.Len() > 0 {
				cmds = append(cmds, tea.Println(indentContent(renderMarkdown(m.llmBuf.String(), m.width-2))))
				m.llmBuf.Reset()
			}
			cmds = append(cmds, tea.Println(indentContent(wrapToWidth(string(v), m.width-2))))

		case StatusMsg:
			m.statusMsg = string(v)

		case TTYExecMsg:
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
				cmds = append(cmds, m.abortTurn("cancelled"))
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

		case msg.Code == tea.KeyEnter && (msg.Mod&tea.ModShift != 0 || msg.Mod&tea.ModAlt != 0) && !m.busy:
			m.input.InsertString("\n")
			m.updateInputSize()

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
							return shellDoneMsg{errMsg: errorStyle.Render("[exit: "+err.Error()+"]")}
						}
						return shellDoneMsg{}
					}))
				} else {
					if !strings.HasPrefix(input, "/") {
						m.busy = true
						m.input.Blur()
					}
					userMsg := indentContent(renderMarkdown(input, m.width-2))
					sep := separatorStyle.Render(strings.Repeat("─", m.width))
					topSep := userLabelStyle.Render(">") + " " + separatorStyle.Render(strings.Repeat("─", m.width-2))
					cmds = append(cmds, tea.Println(topSep+"\n"+userMsg+"\n"+sep))
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

	if !m.busy && !m.input.Focused() {
		cmds = append(cmds, m.input.Focus())
	}

	var spCmd tea.Cmd
	m.spinner, spCmd = m.spinner.Update(msg)
	cmds = append(cmds, spCmd)

	return m, tea.Batch(cmds...)
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = false

	if !m.ready {
		v.Content = ""
		return v
	}

	w := m.width
	if w == 0 {
		w = 80
	}

	var parts []string
	streamLines := 0

	if m.llmBuf.Len() > 0 {
		streamContent := indentContent(renderMarkdown(m.llmBuf.String(), w-2))
		parts = append(parts, streamContent)
		streamLines = strings.Count(streamContent, "\n") + 1
	}

	thinkingLine := m.renderThinkingLine(w)
	if thinkingLine != "" {
		parts = append(parts, thinkingLine)
	}

	inputBox := m.renderInputBox(w)
	statusBar := m.renderStatusBar(w)
	parts = append(parts, inputBox, statusBar)

	v.Content = strings.Join(parts, "\n")

	if cur := m.input.Cursor(); cur != nil {
		cur.X += 1 // border left
		cur.Y += streamLines + 1 // stream lines + border-top
		if m.busy {
			cur.Y += 1 // thinking line
		}
		v.Cursor = cur
	}

	return v
}

// ── Sub-renders ───────────────────────────────────────────────────────────────

func (m *Model) updateInputSize() {
	newLines := m.input.Height()
	if newLines < 1 {
		newLines = 1
	}
	m.inputLines = newLines
}

func (m Model) renderThinkingLine(_ int) string {
	if m.busy {
		return " " + m.spinner.View() + dimStyle.Render(" thinking")
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

func (m Model) renderStatusBar(w int) string {
	barStyle := statusBarNormal
	appStyle := statusBarAppName
	if m.busy {
		barStyle = statusBarStreaming
		appStyle = statusBarAppNameStreaming
	}

	left := appStyle.Render(" shell3 ")
	modeBadge := modeBadgeCustom.Render(" " + m.modeLabel + " ")

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
	midBudget := w - leftW - rightW
	if midBudget < 0 {
		midBudget = 0
	}

	statusText := " " + m.statusMsg + " "
	if m.tokenCount > 0 {
		statusText += fmt.Sprintf("│ ctx: %d ", m.tokenCount)
	}
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

func indentContent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = " " + l
		}
	}
	return strings.Join(lines, "\n")
}

func wrapToWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		for lipgloss.Width(line) > width {
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
	style.Document.Color = nil
	zero := uint(0)
	style.Document.Margin = &zero
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
	return strings.Trim(out, "\n")
}

// execCmd wraps *exec.Cmd to implement tea.ExecCommand for TTY handoff.
type execCmd struct{ c *exec.Cmd }

func newExecCmd(c *exec.Cmd) *execCmd { return &execCmd{c} }

func (e *execCmd) SetStdin(r io.Reader)  { e.c.Stdin = r }
func (e *execCmd) SetStdout(w io.Writer) { e.c.Stdout = w }
func (e *execCmd) SetStderr(w io.Writer) { e.c.Stderr = w }
func (e *execCmd) Run() error            { return e.c.Run() }
