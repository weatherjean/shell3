package kit

import (
	"strings"
	"testing"
)

func TestScanFuncs(t *testing.T) {
	src := []byte("#---\n# agent: main\n#---\n" +
		"main_prompt() {\n  cat <<'EOF'\nhi\nEOF\n}\n" +
		"\n" +
		"do_search () {\n  if true; then echo x; fi\n}\n")

	got, err := scanFuncs(src)
	if err != nil {
		t.Fatalf("scanFuncs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("funcs = %+v, want 2", got)
	}
	if got[0].name != "main_prompt" || got[0].line != 4 {
		t.Fatalf("func0 = %+v", got[0])
	}
	if got[1].name != "do_search" || got[1].line != 10 {
		t.Fatalf("func1 = %+v", got[1])
	}
}

func TestScanFuncsRejectsTopLevelStatement(t *testing.T) {
	src := []byte("f() {\n  :\n}\n" + "echo hello\n")
	_, err := scanFuncs(src)
	if err == nil {
		t.Fatal("want error for a top-level statement")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("err = %q, want it to name line 4", err)
	}
}

func TestScanFuncsQuotedBracesCannotHideTopLevelStatement(t *testing.T) {
	src := []byte("f() { printf '%s\\n' \"{\"; }\n" +
		"touch /tmp/must-not-run\n" +
		"g() { printf '%s\\n' \"}\"; }\n")
	_, err := scanFuncs(src)
	if err == nil {
		t.Fatal("quoted braces hid a top-level statement")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %q, want it to name line 2", err)
	}
}

func TestScanFuncsRejectsStatementAfterOneLineFunction(t *testing.T) {
	for _, src := range []string{
		"f() { :; }; touch /tmp/must-not-run\n",
		"f() { :; } && touch /tmp/must-not-run\n",
	} {
		if _, err := scanFuncs([]byte(src)); err == nil || !strings.Contains(err.Error(), "top-level statement") {
			t.Fatalf("%q: want a top-level statement error, got %v", src, err)
		}
	}
	if _, err := scanFuncs([]byte("f() { :; }; # definition terminator\n")); err != nil {
		t.Fatalf("harmless trailing semicolon/comment rejected: %v", err)
	}
}

func TestScanFuncsQuotedFakeHeredocCannotHideTopLevelStatement(t *testing.T) {
	src := []byte("f() {\n" +
		"  printf '%s\\n' \"<<EOF\"\n" +
		"}\n" +
		"touch /tmp/must-not-run\n" +
		"EOF\n")
	_, err := scanFuncs(src)
	if err == nil {
		t.Fatal("a quoted fake heredoc hid a top-level statement")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("err = %q, want it to name line 4", err)
	}
}

func TestHeredocDelimiterIgnoresQuotesCommentsAndHereStrings(t *testing.T) {
	var braces braceScanner
	for _, line := range []string{`echo "<<EOF"`, `echo x # <<EOF`, `cat <<<EOF`} {
		if got, ok := braces.heredocDelimiter(line); ok {
			t.Errorf("heredocDelimiter(%q) = %q, true; want no delimiter", line, got)
		}
	}
	for _, line := range []string{`cat <<EOF`, `cat <<'EOF'`, `cat <<- "EOF"`} {
		if got, ok := braces.heredocDelimiter(line); !ok || got != "EOF" {
			t.Errorf("heredocDelimiter(%q) = %q, %v; want EOF, true", line, got, ok)
		}
	}
	depth, _ := braces.advance(`f() { printf '%s' "open`, 0)
	if depth != 1 {
		t.Fatalf("multiline quote setup depth = %d, want 1", depth)
	}
	if got, ok := braces.heredocDelimiter(`<<EOF`); ok {
		t.Errorf("heredoc inside a multiline quote = %q, true; want no delimiter", got)
	}
}

func TestScanFuncsIgnoresBracesInQuotesAndComments(t *testing.T) {
	src := []byte("f() {\n" +
		"  printf '%s\\n' '{' \"}\" `printf '{'` # }}}\n" +
		"}\n" +
		"g() { :; }\n")
	got, err := scanFuncs(src)
	if err != nil {
		t.Fatalf("scanFuncs: %v", err)
	}
	if len(got) != 2 || got[0].name != "f" || got[1].name != "g" {
		t.Fatalf("funcs = %+v, want f and g", got)
	}
}

func TestScanFuncsAllowsShebangAndBlankLines(t *testing.T) {
	src := []byte("#!/usr/bin/env shell3\n\n# a comment\n\nf() {\n  :\n}\n")
	got, err := scanFuncs(src)
	if err != nil {
		t.Fatalf("scanFuncs: %v", err)
	}
	if len(got) != 1 || got[0].name != "f" {
		t.Fatalf("funcs = %+v", got)
	}
}

func TestScanFuncsHeredocWithBraces(t *testing.T) {
	src := []byte("skill_x() { cat <<'EOF'\n" +
		"use {\"a\": 1} in your reply\n" +
		"}\n" +
		"a closing brace on its own line, in prose\n" +
		"EOF\n" +
		"}\n" +
		"after() { :; }\n")

	got, err := scanFuncs(src)
	if err != nil {
		t.Fatalf("scanFuncs: %v", err)
	}
	if len(got) != 2 || got[0].name != "skill_x" || got[1].name != "after" {
		t.Fatalf("funcs = %+v, want skill_x and after", got)
	}
}

func TestScanFuncsOneLiner(t *testing.T) {
	src := []byte("f() { :; }\ng() { :; }\n")
	got, err := scanFuncs(src)
	if err != nil {
		t.Fatalf("scanFuncs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("funcs = %+v, want 2", got)
	}
}

func TestScanFuncsUnclosedHeredocFails(t *testing.T) {
	src := []byte("f() { cat <<'EOF'\nsome prose\n}\n")
	if _, err := scanFuncs(src); err == nil {
		t.Fatal("want error for an unclosed heredoc")
	}
}

func TestScanFuncsUnclosedFuncFails(t *testing.T) {
	src := []byte("f() {\n  echo x\n")
	if _, err := scanFuncs(src); err == nil {
		t.Fatal("want error for an unclosed function")
	}
}
