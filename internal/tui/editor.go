package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// openEditorMsg carries the result of composing a prompt in an external editor.
type openEditorMsg struct {
	text string
	err  error
}

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
