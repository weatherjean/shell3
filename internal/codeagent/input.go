package codeagent

import (
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ReadInput shows a huh text area. Multiline via alt+enter, submit via enter (single-line) or ctrl+d.
// Returns io.EOF on ctrl+c.
func ReadInput() (string, error) {
	var value string

	theme := huh.ThemeCharm()
	cyan := lipgloss.Color("6")
	theme.Focused.Title = theme.Focused.Title.Foreground(cyan)
	theme.Blurred.Title = theme.Blurred.Title.Foreground(cyan)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Title("prompt").
				Value(&value).
				CharLimit(0),
		),
	).WithTheme(theme)

	err := form.Run()
	if err == huh.ErrUserAborted {
		return "", io.EOF
	}
	if err != nil {
		return "", err
	}
	if value == "" {
		return ReadInput()
	}
	fmt.Printf("\n\033[36mprompt:\033[0m\n%s\n", value)
	return value, nil
}
