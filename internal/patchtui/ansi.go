package patchtui

import "fmt"

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

// FgRGB returns the SGR sequence to set the foreground to a 24-bit RGB color.
func FgRGB(r, g, b int) string { return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b) }

// BgRGB returns the SGR sequence to set the background to a 24-bit RGB color.
func BgRGB(r, g, b int) string { return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b) }
