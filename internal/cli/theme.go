package cli

import (
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
)

// HuhTheme is the shell3 brand theme for huh forms (boot): the Charm base
// with the banner's yellow as the focus accent and its grays for secondary
// text, so the form and the banner above it read as one surface.
func HuhTheme() huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeCharm(isDark)

		s.Group.Title = s.Group.Title.Foreground(bannerPrimary).Bold(true)
		s.Group.Description = s.Group.Description.Foreground(bannerFgDim)

		f := &s.Focused
		f.Base = f.Base.BorderForeground(bannerPrimary)
		f.Title = f.Title.Foreground(bannerPrimary).Bold(true)
		f.Description = f.Description.Foreground(bannerFgDim)
		f.TextInput.Prompt = f.TextInput.Prompt.Foreground(bannerPrimary)
		f.TextInput.Cursor = f.TextInput.Cursor.Foreground(bannerPrimary)
		f.SelectSelector = f.SelectSelector.Foreground(bannerPrimary)
		f.FocusedButton = f.FocusedButton.Background(bannerPrimary).Foreground(bannerContrast)

		b := &s.Blurred
		b.Description = b.Description.Foreground(bannerMuted)

		return s
	})
}

// FangColorScheme is the shell3 brand color scheme for fang help/error
// output: the default scheme with titles, commands, and flags in the banner
// yellow and secondary text in the banner grays.
func FangColorScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	s := fang.DefaultColorScheme(c)
	s.Title = bannerPrimary
	s.Command = bannerPrimary
	s.Program = bannerPrimary
	s.Flag = bannerFgDim
	s.FlagDefault = bannerMuted
	s.Comment = bannerMuted
	s.DimmedArgument = bannerMuted
	return s
}
