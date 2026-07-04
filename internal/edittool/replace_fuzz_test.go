package edittool

import (
	"strings"
	"testing"
)

// FuzzReplace exercises the 9-replacer cascade for panics and pins the
// exact-match contract: when oldString occurs exactly once verbatim in content,
// the non-replaceAll path (simpleReplacer wins, being first) must apply exactly
// like a single strings.Replace. Fuzzy replacers only ever come into play when
// the verbatim match is absent, so this round-trip stays sound across arbitrary
// input.
func FuzzReplace(f *testing.F) {
	f.Add("hello world", "world", "go", false)
	f.Add("a\nb\nc\n", "b", "B", true)
	f.Add("  indented line\n", "indented line", "x", false)
	f.Add("dup\ndup\n", "dup", "y", false) // ambiguous → errMultipleMatch, must not panic
	f.Add("", "", "", false)

	f.Fuzz(func(t *testing.T, content, oldString, newString string, all bool) {
		got, err := replace(content, oldString, newString, all) // must never panic

		// "Exactly one occurrence" must mirror simpleReplacer's own test
		// (Index == LastIndex), not strings.Count: overlapping matches like
		// "00" in "000" are non-overlapping-count 1 but the replacer correctly
		// rejects them as ambiguous.
		idx := strings.Index(content, oldString)
		if !all && oldString != "" && oldString != newString && idx != -1 && idx == strings.LastIndex(content, oldString) {
			if err != nil {
				t.Fatalf("single verbatim occurrence should replace cleanly: err=%v (content=%q old=%q)", err, content, oldString)
			}
			if want := content[:idx] + newString + content[idx+len(oldString):]; got != want {
				t.Fatalf("exact replace mismatch:\n got=%q\nwant=%q", got, want)
			}
		}
	})
}
