package tui

import "charm.land/lipgloss/v2"

// Color palette — true-color hex values used throughout the TUI.
var (
	colorPrimary  = lipgloss.Color("#EAB308") // shell3 yellow (brand)
	colorAccent   = lipgloss.Color("#06B6D4") // cyan
	colorGreen    = lipgloss.Color("#22C55E")
	colorGreenDim = lipgloss.Color("#166534")
	colorRed      = lipgloss.Color("#EF4444")
	colorMuted    = lipgloss.Color("#6B7280") // gray-500
	colorSubtle   = lipgloss.Color("#374151") // gray-700
	colorDark     = lipgloss.Color("#1F2937") // gray-800
	colorFg       = lipgloss.Color("#D1D5DB") // gray-300
	colorFgDim    = lipgloss.Color("#9CA3AF") // gray-400
	colorBorder   = lipgloss.Color("#374151") // gray-700
)

var (
	// Input box border: idle vs streaming
	inputBorderIdle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	inputBorderStreaming = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorSubtle)

	// nvim/tmux style status bar (full-width, coloured background)
	statusBarNormal = lipgloss.NewStyle().
			Background(colorSubtle).
			Foreground(colorFgDim)

	statusBarStreaming = lipgloss.NewStyle().
				Background(colorGreenDim).
				Foreground(colorFg)

	statusBarAppName = lipgloss.NewStyle().
				Background(colorPrimary).
				Foreground(lipgloss.Color("#000000")).
				Bold(true).
				Padding(0, 1)

	statusBarAppNameStreaming = lipgloss.NewStyle().
					Background(colorGreenDim).
					Foreground(colorFg).
					Bold(true).
					Padding(0, 1)

	// Status bar right-side hints: single darker background.
	statusBarHintStyle = lipgloss.NewStyle().
				Background(colorDark).
				Foreground(colorFgDim)

	statusBarHintKeyStyle = lipgloss.NewStyle().
				Background(colorDark).
				Foreground(colorPrimary).
				Bold(true)

	// Message labels
	userLabelStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	assistantLabelStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	// Inline content
	errorStyle = lipgloss.NewStyle().
			Foreground(colorRed)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Prompt symbol in text input
	promptStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)
)
