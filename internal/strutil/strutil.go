// Package strutil holds the rune-safe string truncation helpers shared by the
// runtime and front-ends. Byte-cap helpers (Truncate, Tail) bound storage and
// context budgets; the rune-count helper (CutRunes) bounds display columns and
// injected-summary lengths.
package strutil

import "unicode/utf8"

// ellipsis marks a trimmed string; its 3 UTF-8 bytes count against the
// caller's byte budget.
const ellipsis = "…"

// Truncate clamps s to at most max bytes — including the appended ellipsis —
// cutting on a rune boundary (never mid-UTF-8-sequence). max <= 0 returns "".
// When max is too small to fit the ellipsis, the string is cut without one.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := max - len(ellipsis)
	if cut < 0 {
		cut = max
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if max < len(ellipsis) {
		return s[:cut]
	}
	return s[:cut] + ellipsis
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

// CutRunes returns s truncated to at most n runes and whether it was cut. No
// ellipsis is added — for callers that append their own truncation marker.
func CutRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]), true
}
