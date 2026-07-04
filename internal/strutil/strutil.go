// Package strutil holds the rune-safe string truncation helpers shared by the
// runtime and front-ends. Byte-cap helpers (Truncate, Tail) bound storage and
// context budgets; rune-count helpers (ClipRunes, CutRunes) bound display
// columns and injected-summary lengths.
package strutil

import "unicode/utf8"

// Truncate clamps s to at most max bytes, cutting on a rune boundary (never
// mid-UTF-8-sequence) and appending an ellipsis when trimmed.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Tail returns the last max bytes of s (rune-safe: the cut advances past any
// partial UTF-8 sequence), prefixed with an ellipsis when trimmed. max <= 0
// returns "".
func Tail(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := len(s) - max
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return "…" + s[cut:]
}

// ClipRunes truncates s to at most n runes total, the last of which becomes an
// ellipsis when s overflows. n < 1 is treated as 1.
func ClipRunes(s string, n int) string {
	if n < 1 {
		n = 1
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// CutRunes returns s truncated to at most n runes and whether it was cut. No
// ellipsis is added — for callers that append their own truncation marker.
func CutRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]), true
}
