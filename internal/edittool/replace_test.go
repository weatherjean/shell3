package edittool

import (
	"errors"
	"strings"
	"testing"
)

func TestReplaceSimpleExact(t *testing.T) {
	got, err := Replace("hello world", "world", "Go", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "hello Go" {
		t.Fatalf("got %q", got)
	}
}

func TestReplaceIdenticalErrors(t *testing.T) {
	_, err := Replace("hello", "x", "x", false)
	if !errors.Is(err, ErrNoChange) {
		t.Fatalf("want ErrNoChange, got %v", err)
	}
}

func TestReplaceNotFound(t *testing.T) {
	_, err := Replace("hello", "missing", "x", false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReplaceMultipleMatchExact(t *testing.T) {
	_, err := Replace("foo foo", "foo", "bar", false)
	if !errors.Is(err, ErrMultipleMatch) {
		t.Fatalf("want ErrMultipleMatch, got %v", err)
	}
}

func TestReplaceAllMultiple(t *testing.T) {
	got, err := Replace("foo foo foo", "foo", "bar", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "bar bar bar" {
		t.Fatalf("got %q", got)
	}
}

// TestReplaceAllExactOverlappingSelfMatch pins that the exact-match replaceAll
// path (simpleReplacer → single candidate) matches strings.ReplaceAll's
// non-overlapping left-to-right semantics, including the odd-leftover case.
func TestReplaceAllExactOverlappingSelfMatch(t *testing.T) {
	cases := []struct{ content, old, new, want string }{
		{"aaaa", "aa", "X", "XX"},  // two non-overlapping matches
		{"aaa", "aa", "X", "Xa"},   // one match, trailing leftover
		{"abab", "ab", "X", "XX"},  // adjacent matches
	}
	for _, c := range cases {
		got, err := Replace(c.content, c.old, c.new, true)
		if err != nil {
			t.Fatalf("Replace(%q,%q): %v", c.content, c.old, err)
		}
		if got != c.want {
			t.Errorf("Replace(%q,%q,%q,true) = %q, want %q (must match strings.ReplaceAll)", c.content, c.old, c.new, got, c.want)
		}
	}
}

func TestLineTrimmedReplacerHandlesTrailingWhitespace(t *testing.T) {
	content := "func main() {\n\treturn nil  \n}\n"
	// model emits the line without the trailing spaces — exact-match would fail.
	got, err := Replace(content, "func main() {\n\treturn nil\n}", "func main() { return ok }", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "func main() { return ok }") {
		t.Fatalf("got %q", got)
	}
}

func TestLineTrimmedReplacerLeadingWhitespace(t *testing.T) {
	content := "  alpha\n  beta\n  gamma\n"
	got, err := Replace(content, "alpha\nbeta\ngamma", "X\nY\nZ", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "X\nY\nZ\n" {
		t.Fatalf("got %q", got)
	}
}

func TestBlockAnchorReplacer(t *testing.T) {
	content := "package x\n\nfunc Foo() {\n\tfmt.Println(\"hi\")\n\tfmt.Println(\"bye\")\n}\n\nfunc Bar() {}\n"
	// middle line slightly different (extra arg) — block anchor should still match.
	find := "func Foo() {\n\tfmt.Println(\"different\")\n}"
	got, err := Replace(content, find, "func Foo() {}", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "func Foo() {}") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "fmt.Println") {
		t.Fatalf("middle lines should be replaced, got %q", got)
	}
}

func TestWhitespaceNormalizedReplacer(t *testing.T) {
	content := "if   foo  ==  bar  {"
	got, err := Replace(content, "if foo == bar {", "if x == y {", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "if x == y {" {
		t.Fatalf("got %q", got)
	}
}

func TestIndentationFlexibleReplacer(t *testing.T) {
	content := "func f() {\n\t\tif x {\n\t\t\treturn 1\n\t\t}\n}\n"
	// Search uses no indent, original is double-tab indented.
	find := "if x {\n\treturn 1\n}"
	got, err := Replace(content, find, "if y { return 2 }", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "if y { return 2 }") {
		t.Fatalf("got %q", got)
	}
}

func TestEscapeNormalizedReplacer(t *testing.T) {
	content := "line1\nline2\nline3"
	// model double-escapes newlines.
	got, err := Replace(content, `line1\nline2`, "X", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "X") || strings.Contains(got, "line1") {
		t.Fatalf("got %q", got)
	}
}

func TestTrimmedBoundaryReplacer(t *testing.T) {
	content := "package x\n\nvar y = 1\n"
	got, err := Replace(content, "  var y = 1  ", "var z = 2", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "var z = 2") {
		t.Fatalf("got %q", got)
	}
}

func TestContextAwareReplacer(t *testing.T) {
	content := "func F() {\n\ta := 1\n\tb := 2\n\tc := 3\n}\n"
	// Middle lines drift but >= 50% match.
	find := "func F() {\n\ta := 1\n\tDIFFERENT := 99\n\tc := 3\n}"
	got, err := Replace(content, find, "func F() {}", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "func F() {}") {
		t.Fatalf("got %q", got)
	}
}

func TestSimpleReplacerWinsOverMultiOccurrenceForUnique(t *testing.T) {
	got, err := Replace("alpha\nbeta\ngamma\n", "beta", "BETA", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "alpha\nBETA\ngamma\n" {
		t.Fatalf("got %q", got)
	}
}

func TestReplaceAllFuzzyReplacesEveryDistinctMatch(t *testing.T) {
	// "foo   bar" (3 spaces) matches neither line literally, but
	// whitespaceNormalizedReplacer matches both "foo bar" and "foo  bar" as two
	// DISTINCT candidates. replaceAll must replace BOTH.
	content := "foo bar\nfoo  bar\n"
	got, err := Replace(content, "foo   bar", "X", true)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	want := "X\nX\n"
	if got != want {
		t.Fatalf("replaceAll fuzzy: got %q, want %q (some matches left unreplaced)", got, want)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
