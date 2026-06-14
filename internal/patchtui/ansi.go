package patchtui

import (
	"fmt"
	"math"
	"strings"
)

// SGR (Select Graphic Rendition) primitives for styling text written to the
// terminal. These are the raw ANSI escape sequences; concatenate them with
// strings to apply, and always close with [Reset] so style does not bleed
// into adjacent output.
//
// patchtui consumes these internally only for cursor and frame control.
// Application code is free to use them directly when composing rendered
// frames or scrollback lines.
const (
	Reset     = "\033[0m"
	Bold      = "\033[1m"
	Dim       = "\033[2m"
	Italic    = "\033[3m"
	Underline = "\033[4m"

	// Standard 8-color foregrounds.
	Black   = "\033[30m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
)

// MutedGreen is a desaturated sage-ish green for tool-call headers. Less
// loud than the bright ANSI green so tool output blocks don't dominate.
var MutedGreen = FgRGB(120, 170, 120)

// Violet is used for user-defined tool-call headers.
var Violet = FgRGB(139, 92, 246)

// MutedThinking styles reasoning/thinking output. Named for its role rather
// than its hue so the color can evolve without churn at call sites. A
// desaturated sage that stays distinct from the cool-gray Dim style and the
// louder MutedGreen used for tool output.
var MutedThinking = FgRGB(135, 165, 140)

// FgRGB returns the SGR sequence to set the foreground to a 24-bit RGB color.
func FgRGB(r, g, b int) string { return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b) }

// BgRGB returns the SGR sequence to set the background to a 24-bit RGB color.
func BgRGB(r, g, b int) string { return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b) }

// Rainbow renders s with a left-to-right hue sweep: each rune gets its own
// 24-bit color stepping through the spectrum, closing with Reset. Used for the
// one-off auto-compaction banner. Whitespace runes are emitted uncolored to
// keep the escape stream small; the visible text is preserved exactly, so
// stripping the SGR codes returns s unchanged.
func Rainbow(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range runes {
		switch r {
		case ' ', '\t', '\n':
			b.WriteRune(r)
			continue
		}
		hue := float64(i) / float64(len(runes)) * 360
		rr, gg, bb := hsvToRGB(hue, 0.85, 1)
		b.WriteString(FgRGB(rr, gg, bb))
		b.WriteRune(r)
	}
	b.WriteString(Reset)
	return b.String()
}

// hsvToRGB converts an HSV color (h in degrees [0,360), s and v in [0,1]) to
// 8-bit RGB components. Used by Rainbow for an even perceptual hue sweep.
func hsvToRGB(h, s, v float64) (int, int, int) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return int((r + m) * 255), int((g + m) * 255), int((b + m) * 255)
}
