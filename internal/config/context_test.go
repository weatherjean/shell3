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
