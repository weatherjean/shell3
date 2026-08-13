package kit

import (
	"strings"
	"testing"
)

func TestScanBlocks(t *testing.T) {
	src := []byte("#!/usr/bin/env shell3\n" +
		"#---\n" +
		"# agent: main\n" +
		"# model: opus\n" +
		"#---\n" +
		"main_prompt() { :; }\n" +
		"\n" +
		"#---\n" +
		"# tool: search\n" +
		"#---\n" +
		"do_search() { :; }\n")

	got, err := scanBlocks(src)
	if err != nil {
		t.Fatalf("scanBlocks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("blocks = %d, want 2", len(got))
	}
	if got[0].line != 2 || got[0].endLine != 5 {
		t.Fatalf("block0 lines = %d..%d, want 2..5", got[0].line, got[0].endLine)
	}
	if string(got[0].yaml) != "agent: main\nmodel: opus\n" {
		t.Fatalf("block0 yaml = %q", got[0].yaml)
	}
	if got[1].line != 8 {
		t.Fatalf("block1 line = %d, want 8", got[1].line)
	}
}

func TestScanBlocksUnterminated(t *testing.T) {
	src := []byte("#---\n# agent: main\n")
	_, err := scanBlocks(src)
	if err == nil {
		t.Fatal("want error for unterminated block")
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("err = %q, want it to name line 1", err)
	}
}

func TestScanBlocksRejectsNonCommentLine(t *testing.T) {
	src := []byte("#---\n# agent: main\necho oops\n#---\n")
	_, err := scanBlocks(src)
	if err == nil {
		t.Fatal("want error for a non-comment line inside a block")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("err = %q, want it to name line 3", err)
	}
}
