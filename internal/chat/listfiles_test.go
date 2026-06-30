package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkTree builds a small directory tree under dir for the listing tests.
func mkTree(t *testing.T, dir string) {
	t.Helper()
	for _, d := range []string{"src/chat", "src/tui", "node_modules/pkg"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"README.md", "go.mod", "src/chat/read.go", "src/chat/turn.go", "src/tui/model.go", "node_modules/pkg/index.js", ".hidden"} {
		writeFile(t, dir, f, "x")
	}
}

func TestListFiles_TreeFormat(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir)
	// depth high enough to reach the files.
	out := handleListFilesTool(`{"path":".","depth":5}`, dir)
	// Directories are suffixed "/" and nested entries indent two spaces per level.
	for _, want := range []string{
		"src/",
		"  chat/",
		"    read.go",
		"    turn.go",
		"  tui/",
		"    model.go",
		"README.md",
		"go.mod",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// Dirs come before files at each level: src/ (dir) before README.md (file).
	if strings.Index(out, "src/") > strings.Index(out, "README.md") {
		t.Fatalf("directories should be listed before files:\n%s", out)
	}
}

func TestListFiles_DepthCap(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir)
	out := handleListFilesTool(`{"path":".","depth":1}`, dir)
	if !strings.Contains(out, "src/") {
		t.Fatalf("depth 1 should show the top-level src/ dir:\n%s", out)
	}
	if strings.Contains(out, "read.go") || strings.Contains(out, "chat/") {
		t.Fatalf("depth 1 must NOT recurse into src/:\n%s", out)
	}
}

func TestListFiles_IgnoreGlobs(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir)
	out := handleListFilesTool(`{"path":".","depth":5,"ignore":["node_modules","*.mod"]}`, dir)
	if strings.Contains(out, "node_modules") {
		t.Fatalf("ignore should exclude node_modules:\n%s", out)
	}
	if strings.Contains(out, "go.mod") {
		t.Fatalf("ignore glob *.mod should exclude go.mod:\n%s", out)
	}
	if !strings.Contains(out, "README.md") {
		t.Fatalf("non-ignored files should remain:\n%s", out)
	}
}

func TestListFiles_NoAutoFiltering_ShowsHidden(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir)
	out := handleListFilesTool(`{"path":"."}`, dir)
	if !strings.Contains(out, ".hidden") {
		t.Fatalf("with no filtering, a hidden file must appear:\n%s", out)
	}
}

func TestListFiles_Truncation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, dir, "f"+string(rune('a'+i)), "x")
	}
	out, truncated := listTree(dir, 1, nil, 5)
	if !truncated {
		t.Fatal("listing more entries than the cap should report truncation")
	}
	if n := strings.Count(strings.TrimSpace(out), "\n") + 1; n > 5 {
		t.Fatalf("output should be capped at 5 entries, got %d:\n%s", n, out)
	}
}

func TestListFiles_Errors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "afile", "x")
	if out := handleListFilesTool(`{"path":"nope"}`, dir); !strings.HasPrefix(out, "error:") {
		t.Fatalf("missing path should error, got %q", out)
	}
	if out := handleListFilesTool(`{"path":"afile"}`, dir); !strings.HasPrefix(out, "error:") {
		t.Fatalf("listing a file (not a dir) should error, got %q", out)
	}
}

// An unreadable directory must surface a permission error, not masquerade as an
// empty directory (the read tool surfaces such errors; list_files should match).
func TestListFiles_UnreadableRootErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses directory permissions")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Skip("chmod unsupported")
	}
	defer os.Chmod(sub, 0o755)
	out := handleListFilesTool(`{"path":"locked"}`, dir)
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("an unreadable directory should error, got %q", out)
	}
}

func TestListFiles_DefaultsToWorkdir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "only.txt", "x")
	out := handleListFilesTool(`{}`, dir)
	if !strings.Contains(out, "only.txt") {
		t.Fatalf("empty args should list the workdir:\n%s", out)
	}
}
