package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The ctrl+p command palette is the single entry point to every exCommand (see
// excommands.go, the source of truth for name/aliases/args/desc/dispatch). It
// replaces the old vim-style ":" command line: an always-live input line on top
// of a filtered, selectable list of matching commands. It renders through the
// shared modal component (modal.go) like help/background/confirm.

// paletteModal is the ctrl+p palette's state.
type paletteModal struct {
	open  bool
	query string // typed filter text (and, for commands with args, "name arg...")
	sel   int    // selected row within the current filtered match list
}

// openPalette resets and opens the palette.
func (m *model) openPalette() {
	m.palette = paletteModal{open: true}
}

// closePalette dismisses the palette, discarding its typed query.
func (m *model) closePalette() {
	m.palette = paletteModal{}
}

// paletteMatches filters exCommands against the palette's query: a simple
// case-insensitive substring match against name+aliases+desc, falling back to a
// subsequence match (so "ntf" would still find "notify") when no entry contains
// the query as a straight substring. Order is exCommands' declaration order.
func paletteMatches(query string) []*exCommand {
	q := strings.ToLower(strings.TrimSpace(query))
	// A typed argument (a space) narrows to the command name itself, so the list
	// still shows the one row the user is filling in an argument for.
	if i := strings.IndexByte(q, ' '); i >= 0 {
		q = q[:i]
	}
	if q == "" {
		out := make([]*exCommand, len(exCommands))
		for i := range exCommands {
			out[i] = &exCommands[i]
		}
		return out
	}
	var out []*exCommand
	for i := range exCommands {
		c := &exCommands[i]
		hay := strings.ToLower(c.name + " " + strings.Join(c.aliases, " ") + " " + c.desc)
		// A straight substring match covers "ag" → agent/agents, "comp" →
		// compact, etc. The subsequence fallback is scoped to just the name (not
		// desc, which is prose long enough that almost any short query would
		// subsequence-match it) so a typo'd/abbreviated name like "bg" still
		// finds "background".
		if strings.Contains(hay, q) || isSubsequence(q, strings.ToLower(c.name)) {
			out = append(out, c)
		}
	}
	return out
}

// isSubsequence reports whether every rune of q appears in hay in order
// (not necessarily contiguous) — a loose fallback filter for the palette.
func isSubsequence(q, hay string) bool {
	hr := []rune(hay)
	qi := 0
	qr := []rune(q)
	for _, c := range hr {
		if qi < len(qr) && c == qr[qi] {
			qi++
		}
	}
	return qi == len(qr)
}

// paletteMatches is the model's current filtered list, clamped-safe to index
// with palette.sel.
func (m *model) paletteMatches() []*exCommand { return paletteMatches(m.palette.query) }

// clampPaletteSel keeps the selection within the current match list.
func (m *model) clampPaletteSel() {
	n := len(m.paletteMatches())
	if n == 0 {
		m.palette.sel = 0
		return
	}
	m.palette.sel = max(min(m.palette.sel, n-1), 0)
}

// handlePaletteKey drives the palette while it is open: it owns every key (like
// the other modals), so esc closes it and ctrl+c can't arm quit underneath.
func (m *model) handlePaletteKey(msg tea.KeyPressMsg, s string) (tea.Model, tea.Cmd) {
	switch s {
	case "esc":
		m.closePalette()
		return m, nil
	case "up":
		m.palette.sel--
		m.clampPaletteSel()
		return m, nil
	case "down":
		m.palette.sel++
		m.clampPaletteSel()
		return m, nil
	case "tab":
		if matches := m.paletteMatches(); len(matches) > 0 {
			c := matches[m.palette.sel]
			m.palette.query = c.name
			m.palette.sel = 0
		}
		return m, nil
	case "enter":
		return m, m.paletteEnter()
	case "backspace":
		if m.palette.query != "" {
			r := []rune(m.palette.query)
			m.palette.query = string(r[:len(r)-1])
			m.palette.sel = 0
		}
		return m, nil
	case "ctrl+u":
		m.palette.query = ""
		m.palette.sel = 0
		return m, nil
	default:
		// A single printable rune (multi-byte included); named keys like
		// "tab"/"enter" are multi-rune strings and fall through untouched.
		if r := msg.Text; r != "" {
			m.palette.query += r
			m.palette.sel = 0
		}
		return m, nil
	}
}

// paletteEnter resolves what Enter does in the palette: if the typed input is a
// full command name plus trailing argument text, it dispatches immediately
// (reusing runCommand, same parsing the old ":" line used). Otherwise it acts on
// the highlighted row: a command that takes no args runs immediately; one that
// does gets its name (plus a trailing space) inserted into the input instead, so
// the user can type the argument.
func (m *model) paletteEnter() tea.Cmd {
	input := strings.TrimSpace(m.palette.query)
	parts := strings.SplitN(input, " ", 2)
	name := parts[0]
	hasArgText := len(parts) > 1 && strings.TrimSpace(parts[1]) != ""
	if hasArgText {
		if c := findCommand(name); c != nil {
			m.closePalette()
			return m.runCommand(input)
		}
	}
	matches := m.paletteMatches()
	if len(matches) == 0 {
		return nil
	}
	m.clampPaletteSel()
	c := matches[m.palette.sel]
	if c.args != "" {
		m.palette.query = c.name + " "
		m.palette.sel = 0
		return nil
	}
	m.closePalette()
	return m.runCommand(c.name)
}

// paletteRowLimit caps how many list rows the palette shows before scrolling the
// window around the selection — mirrors the background modal's approach.
func paletteRowLimit(height int) int { return max(height-10, 3) }

// paletteBox renders the palette: the input line on top, the filtered command
// list below (desc + args hint per row, selection highlighted).
func (m *model) paletteBox() string {
	w := m.modalWidth(m.width*2/3, 84)
	keyStyle := lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	desc := lipgloss.NewStyle().Foreground(cFgDim)
	selStyle := lipgloss.NewStyle().Foreground(cBlack).Background(cPrimary).Bold(true)

	rows := []string{
		stBrand.Render("commands"), "",
		stPaletteInput.Render("› "+m.palette.query) + "█",
		"",
	}
	matches := m.paletteMatches()
	if len(matches) == 0 {
		rows = append(rows, desc.Render(" (no match)"))
	} else {
		maxRows := paletteRowLimit(m.height)
		start := 0
		if len(matches) > maxRows {
			start = min(max(m.palette.sel-maxRows/2, 0), len(matches)-maxRows)
		}
		end := min(start+maxRows, len(matches))
		for i := start; i < end; i++ {
			c := matches[i]
			label := c.name
			if c.args != "" {
				label += " " + c.args
			}
			row := fmt.Sprintf(" %-22s", label)
			if i == m.palette.sel {
				rows = append(rows, selStyle.Render(row+c.desc))
			} else {
				rows = append(rows, keyStyle.Render(row)+desc.Render(c.desc))
			}
		}
		if len(matches) > maxRows {
			rows = append(rows, stDim.Render(fmt.Sprintf("  %d of %d", m.palette.sel+1, len(matches))))
		}
	}
	rows = append(rows, "", desc.Render("↑↓ select · enter run · tab complete · esc close"))
	return modalBox(rows, 0, 1, w)
}
