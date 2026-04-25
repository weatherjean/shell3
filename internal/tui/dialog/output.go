package dialog

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// OutputID is the stable identifier for the command-output dialog.
const OutputID = "command-output"

// CommandOutput displays the output of a slash command in a scrollable modal.
type CommandOutput struct {
	title  string
	lines  []string
	offset int
}

// NewCommandOutput creates a dialog showing title + content.
func NewCommandOutput(title, content string) *CommandOutput {
	return &CommandOutput{
		title: title,
		lines: strings.Split(strings.TrimRight(content, "\n"), "\n"),
	}
}

func (*CommandOutput) ID() string { return OutputID }

var (
	outputCloseKey = key.NewBinding(key.WithKeys("esc", "q"))
	outputUpKey    = key.NewBinding(key.WithKeys("up", "k"))
	outputDownKey  = key.NewBinding(key.WithKeys("down", "j"))
	outputPgUpKey  = key.NewBinding(key.WithKeys("pgup", "ctrl+b"))
	outputPgDnKey  = key.NewBinding(key.WithKeys("pgdn", "ctrl+f"))
)

func (c *CommandOutput) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, outputCloseKey):
			return ActionClose{}
		case key.Matches(msg, outputUpKey):
			if c.offset > 0 {
				c.offset--
			}
		case key.Matches(msg, outputDownKey):
			c.offset++
		case key.Matches(msg, outputPgUpKey):
			c.offset -= 10
			if c.offset < 0 {
				c.offset = 0
			}
		case key.Matches(msg, outputPgDnKey):
			c.offset += 10
		}
	}
	return nil
}

func (c *CommandOutput) View(width, height int) string {
	dialogW := width * 4 / 5
	if dialogW < 40 {
		dialogW = 40
	}
	if dialogW > width {
		dialogW = width
	}

	dialogH := height * 2 / 3
	if dialogH < 8 {
		dialogH = 8
	}

	// inner content width: dialog width minus frame (border=2) and padding (2*1=2)
	innerW := dialogW - 4
	if innerW < 1 {
		innerW = 1
	}

	// vertical space for body: dialog height minus frame (2) - title (1+margin=2) - hint (1+margin=2)
	bodyH := dialogH - 6
	if bodyH < 1 {
		bodyH = 1
	}

	// clamp scroll offset
	maxOffset := len(c.lines) - bodyH
	if maxOffset < 0 {
		maxOffset = 0
	}
	if c.offset > maxOffset {
		c.offset = maxOffset
	}
	if c.offset < 0 {
		c.offset = 0
	}

	end := c.offset + bodyH
	if end > len(c.lines) {
		end = len(c.lines)
	}
	visible := c.lines[c.offset:end]

	bodyLines := make([]string, bodyH)
	for i := range bodyLines {
		if i < len(visible) {
			bodyLines[i] = visible[i]
		}
	}

	scrollIndicator := ""
	if len(c.lines) > bodyH {
		scrollIndicator = lipgloss.NewStyle().Foreground(colorMuted).Render(
			" · " + strings.Repeat("─", 0) +
				lipgloss.NewStyle().Foreground(colorAccent).Render("↑/↓"),
		)
	}

	title := dialogTitleStyle.Width(innerW).Render(c.title + scrollIndicator)
	body := dialogBodyStyle.Width(innerW).Render(strings.Join(bodyLines, "\n"))
	hint := dialogHintStyle.Width(innerW).Render("esc · q  close     ↑/↓  k/j  scroll     pgup · pgdn  fast scroll")

	inner := lipgloss.JoinVertical(lipgloss.Left, title, body, hint)
	box := dialogFrameStyle.Width(dialogW).Render(inner)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
