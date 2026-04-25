package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	welcomeTitleStyle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)

	welcomeSubStyle = lipgloss.NewStyle().
		Foreground(colorFgDim)

	welcomeDimStyle = lipgloss.NewStyle().
		Foreground(colorMuted)
)

// renderWelcome returns the landing screen shown before the first message.
func renderWelcome(width int) string {
	if width <= 0 {
		width = 80
	}

	title := welcomeTitleStyle.Render("◆ shell3")
	sub := welcomeSubStyle.Render("AI-powered shell assistant")

	hints := []string{
		"  " + welcomeDimStyle.Render("type a message") + "  start a conversation",
		"  " + welcomeDimStyle.Render("! <cmd>") + "  run a shell command with a real terminal",
		"  " + welcomeDimStyle.Render("/help") + "  list slash commands",
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Left).PaddingLeft(2).Render(title))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Width(width).PaddingLeft(2).Render(sub))
	b.WriteString("\n\n")
	for _, h := range hints {
		b.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(h))
		b.WriteString("\n")
	}
	return b.String()
}
