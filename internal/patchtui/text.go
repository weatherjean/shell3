package patchtui

import "strings"

// SplitLines splits text on '\n' and returns the resulting lines, dropping
// a trailing empty element if text ends with '\n'. Returns nil for the
// empty string. Useful for converting accumulated stream output into the
// []string slices that [Renderer.Print] expects.
func SplitLines(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// VisibleLen returns the number of visible columns occupied by s, skipping
// ANSI SGR escape sequences. East Asian Wide and emoji ranges count as 2
// columns; everything else counts as 1. Approximate — covers the common
// cases (CJK, emoji, Hangul) but is not a full Unicode width database.
//
// Use this when computing padding, truncation, or alignment for styled
// strings; raw len(s) over-counts by the byte length of every escape.
func VisibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		n += RuneWidth(r)
	}
	return n
}

// RuneWidth returns 2 for runes in common East Asian Wide and emoji
// ranges, 1 otherwise. Approximate — covers CJK/Hangul/emoji but not
// every Unicode width quirk. Exposed for callers that walk strings rune
// by rune (for example, when wrapping at column boundaries).
func RuneWidth(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return 2
	case r >= 0x2E80 && r <= 0x9FFF: // CJK
		return 2
	case r >= 0xA000 && r <= 0xA4CF: // Yi
		return 2
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul syllables
		return 2
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility
		return 2
	case r >= 0xFE30 && r <= 0xFE4F: // CJK Compatibility forms
		return 2
	case r >= 0xFF00 && r <= 0xFF60: // Fullwidth
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth signs
		return 2
	case r >= 0x1F000 && r <= 0x1FAFF: // Emoji + symbols
		return 2
	case r >= 0x20000 && r <= 0x3FFFD: // CJK Extension B-G
		return 2
	}
	return 1
}
