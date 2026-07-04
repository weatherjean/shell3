package tui

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

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
		} else if out, err := m.cmds.Prune(arg); err != nil {
			m.cmdInfo("error: " + err.Error())
		} else {
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
		switch arg {
		case "":
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
	{name: "disable_safety", aliases: []string{"safety"}, desc: "toggle auto-allow for command gate (!)", run: func(m *model, _ string) tea.Cmd {
		m.safetyOff = !m.safetyOff
		if m.cmds != nil {
			m.cmds.SetSafetyOff(m.safetyOff)
		}
		if m.safetyOff {
			m.cmdInfo("command gate asks auto-allowed (!) — run :disable_safety again to re-enable")
		} else {
			m.cmdInfo("command gate prompts re-enabled")
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

// cmdInfo prints a one-line result into the transcript and refreshes. Shared by
// every ":" command handler.
func (m *model) cmdInfo(s string) { m.tr.AddInfo(s); m.refresh(true) }

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
