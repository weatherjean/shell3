package cli

import (
	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
)

// FangColorScheme applies shell3's terminal palette to CLI help and errors.
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
