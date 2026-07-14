package cli

import (
	"fmt"
	"io"

	"charm.land/lipgloss/v2"
)

// Brand banner colors, taken from the historical dark palette. The banner is
// printed by cobra's pre-run (before any terminal-background sensing), so it
// always rendered with the dark palette in the old TUI — these values keep that
// look identical.
var (
	bannerPrimary = lipgloss.Color("#EAB308") // brand yellow
	bannerMuted   = lipgloss.Color("#6B7280")
	bannerFgDim   = lipgloss.Color("#9CA3AF")
)

// PrintHeader writes the two-line shell3 brand banner to w, used as a uniform
// top banner for the non-interactive commands.
func PrintHeader(w io.Writer) {
	brand := lipgloss.NewStyle().Foreground(bannerPrimary).Bold(true)
	dim := lipgloss.NewStyle().Foreground(bannerMuted)
	sub := lipgloss.NewStyle().Foreground(bannerFgDim)
	fmt.Fprintln(w, brand.Render("๑ï shell3")+"  "+dim.Render("/ˈʃɛli/"))
	fmt.Fprintln(w, sub.Render("minimal Unix-composable coding agent"))
	fmt.Fprintln(w)
}
