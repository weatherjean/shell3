package patchapp

import (
	"strings"

	"github.com/weatherjean/shell3/internal/patchtui"
)

const asciiLogo = "       /\\\n      {.-}\n     ;_.-'\\\n    {    _.}_\n     \\.-' /  `,\n      \\  |    /\n       \\ |  ,/\n        \\|_/"

// renderWelcome returns the welcome lines printed once on session start.
// Pass the lines to Renderer.Print so they live in scrollback, never
// re-rendered.
func renderWelcome(width int) []string {
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
	out = append(out, "  "+styled("type a message", dim, "", false)+"  start a conversation")
	out = append(out, "  "+styled("! <cmd>", dim, "", false)+"  run a shell command with a real terminal")
	out = append(out, "  "+styled("/help", dim, "", false)+"  list slash commands")
	out = append(out, "", "", "", "", "")
	return out
}

