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

// Heredocs are how kits carry prose, and prose contains unbalanced braces — a
// skill quoting JSON, a prompt with a stray `}`. Naive brace counting ends the
// function early and reports the next prose line as a top-level statement.
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
