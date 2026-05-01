package patchapp

import (
	"strings"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// labelBlock renders a label line followed by a word-wrapped value line,
// indented by indent spaces. Returns 2+ lines.
func labelBlock(styledLabel, value string, indent, width int) []string {
	out := []string{"  " + styledLabel}
	if value == "" {
		return out
	}
	avail := width - indent
	if avail < 10 {
		return append(out, strings.Repeat(" ", indent)+value)
	}
	words := strings.Fields(value)
	cont := strings.Repeat(" ", indent)
	line := cont + words[0]
	lineLen := indent + len(words[0])
	for _, w := range words[1:] {
		if lineLen+1+len(w) <= width {
			line += " " + w
			lineLen += 1 + len(w)
		} else {
			out = append(out, line)
			line = cont + w
			lineLen = indent + len(w)
		}
	}
	return append(out, line)
}

const asciiLogo = "       /\\\n      {.-}\n     ;_.-'\\\n    {    _.}_\n     \\.-' /  `,\n      \\  |    /\n       \\ |  ,/\n        \\|_/"

// WelcomeInfo holds session metadata rendered in the welcome card.
type WelcomeInfo struct {
	Persona      string   // persona name
	ProjectRef   string   // project UUID
	ActiveSkills []string // skill names active for this persona
	ActiveTools  []string // user tool names active for this persona
}

// renderWelcome returns the welcome lines printed once on session start.
// Pass the lines to Renderer.Print so they live in scrollback, never
// re-rendered.
func renderWelcome(width int, info WelcomeInfo) []string {
	if width < 40 {
		width = 40
	}
	yellow := patchtui.FgRGB(rPrimary, gPrimary, bPrimary)
	dim := patchtui.FgRGB(rMuted, gMuted, bMuted)
	sub := patchtui.FgRGB(rFgDim, gFgDim, bFgDim)

	// Center the whole logo as a block (preserve internal alignment).
	logoLines := strings.Split(asciiLogo, "\n")
	maxW := 0
	for _, l := range logoLines {
		if patchtui.VisibleLen(l) > maxW {
			maxW = patchtui.VisibleLen(l)
		}
	}
	leftPad := (width - maxW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	pad := strings.Repeat(" ", leftPad)

	var out []string
	out = append(out, "")
	for _, l := range logoLines {
		out = append(out, pad+styled(l, yellow, "", true))
	}
	out = append(out, "")
	out = append(out, "  "+styled("◆ shell3", yellow, "", true)+"  "+styled("/'ʃɛli/", dim, "", false))
	out = append(out, "  "+styled("AI-powered shell assistant", sub, "", false))
	out = append(out, "")

	const infoIndent = 4
	if info.Persona != "" {
		out = append(out, labelBlock(styled("persona", yellow, "", false), info.Persona, infoIndent, width)...)
	}
	if info.ProjectRef != "" {
		out = append(out, labelBlock(styled("project", yellow, "", false), info.ProjectRef, infoIndent, width)...)
	}
	out = append(out, "")
	out = append(out, labelBlock(styled("/help", yellow, "", false), "list slash commands  ·  /info for session details", infoIndent, width)...)
	out = append(out, "", "", "", "", "")
	return out
}

