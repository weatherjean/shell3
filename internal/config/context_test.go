package config

import (
	"strings"
	"testing"
)

func TestResolveContextFiles_LiteralAndGlob(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "memory.md", "MEM")
	writeFile(t, dir, "notes/b.md", "BEE")
	writeFile(t, dir, "notes/a.md", "AYE")

	files, err := ResolveContextFiles(dir, []string{"memory.md", "notes/*.md"})
	if err != nil {
		t.Fatalf("ResolveContextFiles: %v", err)
	}
	// list order, glob expansion sorted lexically within its entry.
	want := []ContextFile{
		{Path: "memory.md", Body: "MEM"},
		{Path: "notes/a.md", Body: "AYE"},
		{Path: "notes/b.md", Body: "BEE"},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %+v", len(files), len(want), files)
	}
	for i, w := range want {
		if files[i].Path != w.Path || files[i].Body != w.Body {
			t.Errorf("files[%d] = %+v, want %+v", i, files[i], w)
		}
	}
}

func TestResolveContextFiles_MissingLiteralStub(t *testing.T) {
	dir := t.TempDir()
	files, err := ResolveContextFiles(dir, []string{"gone.md"})
	if err != nil {
		t.Fatalf("ResolveContextFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if files[0].Path != "gone.md" || files[0].Body != "(context file missing: gone.md)" {
		t.Errorf("missing-file stub = %+v", files[0])
	}
}

func TestBuildPersonaFor_ContextSection(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"agent.md":  "---\nmodel: m1\ncontext: [memory.md]\n---\nBASE PROMPT\n",
		"memory.md": "REMEMBER THIS",
	})
	prompt := c.BuildPersonaFor(c.FirstAgent())
	for _, want := range []string{"BASE PROMPT", "## Context", "### memory.md", "REMEMBER THIS"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// A context file under the cap is passed through byte-for-byte: the cap must
// be invisible in the common case, since every agent brain file lives here.
func TestReadContextFile_UnderCapIsVerbatim(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("x\n", MaxContextBytes/4)
	writeFile(t, dir, "memory.md", body)

	files, err := ResolveContextFiles(dir, []string{"memory.md"})
	if err != nil {
		t.Fatalf("ResolveContextFiles: %v", err)
	}
	if files[0].Body != body {
		t.Errorf("body was modified under the cap: got %d bytes, want %d", len(files[0].Body), len(body))
	}
	if files[0].Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d", files[0].Size, len(body))
	}
}

// The cap keeps BOTH ends. A tail-only window would drop a brain file's
// standing header — the traps and instructions it exists to carry — which is
// the failure this cap is meant to prevent, not cause.
func TestReadContextFile_OverCapKeepsHeadAndTail(t *testing.T) {
	dir := t.TempDir()
	head := "HEAD-SENTINEL: standing traps live up here\n"
	tail := "TAIL-SENTINEL: the newest appended note\n"
	body := head + strings.Repeat("filler filler filler\n", MaxContextBytes/8) + tail
	writeFile(t, dir, "memory.md", body)

	files, err := ResolveContextFiles(dir, []string{"memory.md"})
	if err != nil {
		t.Fatalf("ResolveContextFiles: %v", err)
	}
	got := files[0].Body

	if len(got) > MaxContextBytes {
		t.Errorf("body is %d bytes, over the %d cap", len(got), MaxContextBytes)
	}
	if !strings.Contains(got, "HEAD-SENTINEL") {
		t.Error("head was dropped — a tail-only window loses the file's standing instructions")
	}
	if !strings.Contains(got, "TAIL-SENTINEL") {
		t.Error("tail was dropped — the newest appended note must survive")
	}
	// The marker is what stops the agent reading a window as if it were whole.
	if !strings.Contains(got, "elided") || !strings.Contains(got, "memory.md") {
		t.Errorf("truncation marker missing or unnamed; body middle = %.200q", got[len(got)/3:])
	}
	// Size reports the file on disk, not the elided body.
	if files[0].Size != int64(len(body)) {
		t.Errorf("Size = %d, want true file size %d", files[0].Size, len(body))
	}
}

// The warning fires between WarnContextBytes and the cap — the whole point is
// to reach the operator BEFORE anything is silently elided.
func TestContextSizeWarnings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "small.md", "tiny")
	writeFile(t, dir, "warn.md", strings.Repeat("y\n", (WarnContextBytes+2048)/2))
	writeFile(t, dir, "huge.md", strings.Repeat("z\n", MaxContextBytes))

	got := ContextSizeWarnings(dir, []string{"small.md", "warn.md", "huge.md"})
	if len(got) != 2 {
		t.Fatalf("got %d warnings, want 2 (warn.md, huge.md): %q", len(got), got)
	}
	if got[0].Path != "warn.md" || got[0].OverCap {
		t.Errorf("warn.md should warn but not be over cap, got %+v", got[0])
	}
	if got[1].Path != "huge.md" || !got[1].OverCap {
		t.Errorf("huge.md should be flagged over cap, got %+v", got[1])
	}
	// Callers branch on OverCap; the message is for humans.
	if !strings.Contains(got[1].String(), "elided") {
		t.Errorf("over-cap message should say the middle is elided, got %q", got[1].String())
	}
}
