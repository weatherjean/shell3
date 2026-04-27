package store

import "strings"

// sanitizeFTSQuery converts free-form user/model input into a safe FTS5
// MATCH expression. Tokens (whitespace-separated) are wrapped in double
// quotes so any FTS5-reserved characters inside (`?`, `:`, `-`, parens,
// `*`, etc.) are treated as part of the token rather than as DSL syntax.
// Embedded `"` is escaped as `""` per FTS5 string-literal rules.
// Tokens consisting entirely of non-word characters are dropped.
//
// Examples:
//
//	"hello world"         → `"hello" "world"`
//	"cobra colorful cli ?" → `"cobra" "colorful" "cli"`
//	"a:b c-d"             → `"a:b" "c-d"`
func sanitizeFTSQuery(q string) string {
	var out []string
	for _, tok := range strings.Fields(q) {
		if !hasWordChar(tok) {
			continue
		}
		// Escape internal double quotes per FTS5 grammar.
		tok = strings.ReplaceAll(tok, `"`, `""`)
		out = append(out, `"`+tok+`"`)
	}
	return strings.Join(out, " ")
}

// BuildFTSExpr converts a list of free-form search terms into a safe
// FTS5 MATCH expression. Each term becomes a quoted phrase (so internal
// punctuation, spaces, and FTS5-reserved chars are taken literally).
// Terms with no word characters are dropped. Empty result = "".
//
// matchAll false → join with " OR " (broad recall, default for agents).
// matchAll true  → join with " AND " (narrow precision).
func BuildFTSExpr(terms []string, matchAll bool) string {
	var phrases []string
	for _, t := range terms {
		t = strings.TrimSpace(t)
		if !hasWordChar(t) {
			continue
		}
		t = strings.ReplaceAll(t, `"`, `""`)
		phrases = append(phrases, `"`+t+`"`)
	}
	if len(phrases) == 0 {
		return ""
	}
	op := " OR "
	if matchAll {
		op = " AND "
	}
	return strings.Join(phrases, op)
}

// hasWordChar reports whether s contains at least one alphanumeric byte.
// Used to drop pure-punctuation tokens like "?" or "!.,".
func hasWordChar(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c >= 0x80 {
			return true
		}
	}
	return false
}
