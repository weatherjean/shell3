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
