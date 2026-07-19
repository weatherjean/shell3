package cli

import (
	"fmt"
	"io"

	"charm.land/lipgloss/v2"
)

// Brand banner colors, taken from the historical dark palette. The banner is
// printed by cobra's pre-run (before any terminal-background sensing), so it
// always rendered with the dark palette — these values keep that
// look identical.
var (
	bannerPrimary  = lipgloss.Color("#EAB308") // brand yellow
	bannerMuted    = lipgloss.Color("#6B7280")
	bannerFgDim    = lipgloss.Color("#9CA3AF")
	bannerContrast = lipgloss.Color("#1F2937") // dark text on a bannerPrimary background
)

// brandLine renders the one-line snail wordmark shared by the full banner and
// the slim help logo.
func brandLine() string {
	brand := lipgloss.NewStyle().Foreground(bannerPrimary).Bold(true)
	dim := lipgloss.NewStyle().Foreground(bannerMuted)
	return brand.Render("๑ï shell3") + "  " + dim.Render("/ˈʃɛli/")
}

// PrintHeader writes the two-line shell3 brand banner to w, used as a uniform
// top banner for the non-interactive commands.
func PrintHeader(w io.Writer) {
	sub := lipgloss.NewStyle().Foreground(bannerFgDim)
	fmt.Fprintln(w, brandLine())
	fmt.Fprintln(w, sub.Render("minimal Unix-composable personal agent"))
	fmt.Fprintln(w)
}

// PrintLogo writes just the one-line snail brand, indented to sit flush above
// fang's help output (which prints the command description itself — the full
// banner's tagline would double it).
func PrintLogo(w io.Writer) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  "+brandLine())
}
