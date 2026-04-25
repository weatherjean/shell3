package dialog

import "charm.land/lipgloss/v2"

var (
	colorBorder = lipgloss.Color("#374151") // gray-700
	colorMuted  = lipgloss.Color("#6B7280") // gray-500
	colorFg     = lipgloss.Color("#D1D5DB") // gray-300
	colorAccent = lipgloss.Color("#EAB308") // shell3 yellow

	dialogFrameStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorder).
				Padding(0, 1)

	dialogTitleStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true).
				MarginBottom(1)

	dialogHintStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			MarginTop(1)

	dialogBodyStyle = lipgloss.NewStyle().
			Foreground(colorFg)
)
