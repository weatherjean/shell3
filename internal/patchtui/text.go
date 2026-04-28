package patchtui

// Unicode-aware text measurement for terminal rendering.
//
// Grapheme cluster segmentation is provided by rivo/uniseg
// (Rivo Huber, MIT License, https://github.com/rivo/uniseg),
// which implements Unicode Annex #29 and UAX #11.
//
// The ANSI-strip + grapheme-walk approach is inspired by
// charmbracelet/x/ansi (Charmbracelet Inc., MIT License).

import (
	"strings"

	"github.com/rivo/uniseg"
)

// SplitLines splits text on '\n' and returns the resulting lines, dropping
// at most one trailing '\n' so a final newline acts as a line terminator
// rather than producing a spurious empty line. Multiple trailing newlines
// are preserved as blank lines so callers can intentionally emit spacing.
// Returns nil for the empty string.
func SplitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// VisibleLen returns the number of visible terminal columns occupied by s.
//
// ANSI SGR escape sequences (ESC [ … m) are skipped. The remainder is
// measured as grapheme clusters per Unicode Annex #29, so ZWJ sequences
// (e.g. 👩‍💻), emoji with variation selectors (e.g. 🖥️), and East Asian
// wide characters are all counted correctly.
//
// Use VisibleLen whenever you compute padding, truncation, or column
// alignment for strings that may contain ANSI colour codes or non-ASCII
// text.
func VisibleLen(s string) int {
	return uniseg.StringWidth(StripANSI(s))
}

// StripANSI returns s with all ANSI SGR escape sequences removed.
// Only ESC [ … m sequences are stripped; other escape types pass through.
func StripANSI(s string) string {
	if !strings.ContainsRune(s, '\033') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
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
		b.WriteRune(r)
	}
	return b.String()
}

// RuneWidth returns the display width of a single rune in terminal columns.
//
// Returns 0 for zero-width characters (ZWJ U+200D, variation selectors
// U+FE00–FE0F, combining marks, ZWSP, BOM). Returns 2 for East Asian wide
// characters and most emoji. Returns 1 otherwise.
//
// Limitation: RuneWidth cannot correctly measure multi-rune grapheme
// clusters such as ZWJ sequences (e.g. 👩‍💻 spans three runes but occupies
// two columns). Use VisibleLen for whole strings.
func RuneWidth(r rune) int {
	switch {
	// Zero-width: joiners, variation selectors, combining marks.
	case r == 0x200D: // Zero Width Joiner
		return 0
	case r >= 0xFE00 && r <= 0xFE0F: // Variation Selectors 1–16
		return 0
	case r >= 0x0300 && r <= 0x036F: // Combining Diacritical Marks
		return 0
	case r >= 0x1DC0 && r <= 0x1DFF: // Combining Diacritical Marks Supplement
		return 0
	case r >= 0x20D0 && r <= 0x20FF: // Combining Diacritical Marks for Symbols
		return 0
	case r == 0x200B || r == 0x200C || r == 0xFEFF: // ZWSP, ZWNJ, BOM
		return 0

	// ASCII fast path.
	case r < 0x80:
		return 1

	// Wide: East Asian, emoji, symbols.
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return 2
	case r >= 0x2600 && r <= 0x27BF: // Misc Symbols + Dingbats (✨ ✅ etc.)
		return 2
	case r >= 0x2B00 && r <= 0x2BFF: // Misc Symbols and Arrows
		return 2
	case r >= 0x2E80 && r <= 0x9FFF: // CJK unified ideographs + radicals
		return 2
	case r >= 0xA000 && r <= 0xA4CF: // Yi
		return 2
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul syllables
		return 2
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return 2
	case r >= 0xFE30 && r <= 0xFE4F: // CJK Compatibility Forms
		return 2
	case r >= 0xFF00 && r <= 0xFF60: // Fullwidth ASCII variants
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth currency signs
		return 2
	case r >= 0x1F000 && r <= 0x1FAFF: // Emoji, Mahjong, playing cards, symbols
		return 2
	case r >= 0x20000 && r <= 0x3FFFD: // CJK Extension B–G
		return 2
	}
	return 1
}
