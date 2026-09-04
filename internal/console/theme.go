package console

import (
	"fmt"
	"image/color"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"
	"golang.org/x/term"
)

type consoleTheme struct {
	tty       bool
	dark      bool
	prompt    lipgloss.Style
	dim       lipgloss.Style
	info      lipgloss.Style
	assistant lipgloss.Style
	result    lipgloss.Style
	output    lipgloss.Style
	err       lipgloss.Style
	toolBash  lipgloss.Style
	toolBG    lipgloss.Style
	toolOther lipgloss.Style
}

func newTheme(in io.Reader, out io.Writer) consoleTheme {
	fd, ok := out.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(int(fd.Fd())) {
		return consoleTheme{}
	}

	dark := true
	input, inputOK := in.(terminalFile)
	output, outputOK := out.(terminalFile)
	if inputOK && outputOK {
		dark = lipgloss.HasDarkBackground(input, output)
	}
	colors := struct {
		primary, green, red, cyan, pink, dim, muted color.Color
	}{
		primary: lipgloss.Color("#EAB308"), green: lipgloss.Color("#78AA78"),
		red: lipgloss.Color("#DC2626"), cyan: lipgloss.Color("#5BB6C9"),
		pink: lipgloss.Color("#D98FB8"), dim: lipgloss.Color("#9CA3AF"),
		muted: lipgloss.Color("#6B7280"),
	}
	if !dark {
		colors.primary, colors.green, colors.red = lipgloss.Color("#9A6700"), lipgloss.Color("#1A7F37"), lipgloss.Color("#CF222E")
		colors.cyan, colors.pink = lipgloss.Color("#0969DA"), lipgloss.Color("#BF3989")
		colors.dim, colors.muted = lipgloss.Color("#57606A"), lipgloss.Color("#6E7781")
	}

	return consoleTheme{
		tty:       true,
		dark:      dark,
		prompt:    lipgloss.NewStyle().Foreground(colors.primary).Bold(true),
		dim:       lipgloss.NewStyle().Foreground(colors.muted),
		info:      lipgloss.NewStyle().Foreground(colors.cyan),
		assistant: lipgloss.NewStyle().Foreground(colors.green).Bold(true),
		result:    lipgloss.NewStyle().Foreground(colors.green).Bold(true),
		output:    lipgloss.NewStyle().Foreground(colors.dim),
		err:       lipgloss.NewStyle().Foreground(colors.red).Bold(true),
		toolBash:  lipgloss.NewStyle().Foreground(colors.cyan).Bold(true),
		toolBG:    lipgloss.NewStyle().Foreground(colors.red).Bold(true),
		toolOther: lipgloss.NewStyle().Foreground(colors.pink).Bold(true),
	}
}

type terminalFile interface {
	io.ReadWriteCloser
	Fd() uintptr
}

func (t consoleTheme) toolFor(name string) lipgloss.Style {
	switch name {
	case "bash":
		return t.toolBash
	case "bash_bg":
		return t.toolBG
	default:
		return t.toolOther
	}
}

func rainbow(s string, shift int) string {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		hue := math.Mod(float64(i)*360/float64(n)+float64(shift)*15, 360)
		red, green, blue := colorful.Hsv(hue, 0.75, 0.88).RGB255()
		fmt.Fprintf(&b, "\x1b[1;38;2;%d;%d;%dm%c\x1b[0m", red, green, blue, r)
	}
	return b.String()
}
