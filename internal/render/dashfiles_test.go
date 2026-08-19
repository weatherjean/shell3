//go:build unix

package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The listing shows dirs before files, credential files carry the lock mark,
// and every link threads the token.
func TestFilesListHTML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shell3.sh"), "# kit")
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=xyz")
	writeFile(t, filepath.Join(dir, "skills", "a.md"), "skill")

	frag, ok := FilesListHTML(dir, "", "TOK")
	if !ok {
		t.Fatal("listing not ok")
	}
	if !strings.Contains(frag, "shell3.sh") || !strings.Contains(frag, "skills/") {
		t.Errorf("listing missing entries:\n%s", frag)
	}
	if !strings.Contains(frag, ".env") || !strings.Contains(frag, "🔒") {
		t.Errorf("credential file not marked redacted:\n%s", frag)
	}
	if !strings.Contains(frag, "t=TOK") {
		t.Errorf("token not threaded into links:\n%s", frag)
	}
	// dirs first: skills/ appears before shell3.sh in the rendered order.
	if strings.Index(frag, "skills/") > strings.Index(frag, "shell3.sh") {
		t.Errorf("dirs should sort before files:\n%s", frag)
	}
}

// A normal file renders its content escaped.
func TestFileViewHTML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.md"), "hello <script>alert(1)</script>")
	frag, ok := FileViewHTML(dir, "notes.md", "TOK")
	if !ok {
		t.Fatal("view not ok")
	}
	if strings.Contains(frag, "<script>alert") {
		t.Errorf("content not escaped:\n%s", frag)
	}
	if !strings.Contains(frag, "&lt;script&gt;") {
		t.Errorf("expected escaped content:\n%s", frag)
	}
}

// SECURITY: a credential file is reported redacted and its contents are NEVER
// placed in the page.
func TestFileViewRedactsCredentials(t *testing.T) {
	dir := t.TempDir()
	secret := "SECRET=super-secret-value-42"
	writeFile(t, filepath.Join(dir, ".env"), secret)
	frag, ok := FileViewHTML(dir, ".env", "TOK")
	if !ok {
		t.Fatal("view not ok")
	}
	if strings.Contains(frag, "super-secret-value-42") {
		t.Fatalf("SECRET LEAKED into page:\n%s", frag)
	}
	if !strings.Contains(strings.ToLower(frag), "redacted") {
		t.Errorf("expected a redaction notice:\n%s", frag)
	}
	// Same for dotenv siblings.
	writeFile(t, filepath.Join(dir, ".env.local"), secret)
	frag2, _ := FileViewHTML(dir, ".env.local", "TOK")
	if strings.Contains(frag2, "super-secret-value-42") {
		t.Fatalf("SECRET LEAKED from .env.local:\n%s", frag2)
	}
}

// SECURITY: path traversal via ../ is clamped at the root — a file outside the
// config dir is unreachable.
func TestFilesTraversalBlocked(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside-secret.txt")
	writeFile(t, outside, "TOP SECRET OUTSIDE ROOT")
	t.Cleanup(func() { _ = os.Remove(outside) })

	for _, p := range []string{"../outside-secret.txt", "../../etc/passwd", "..%2f..", "/etc/passwd"} {
		if frag, ok := FileViewHTML(root, p, "TOK"); ok && strings.Contains(frag, "TOP SECRET") {
			t.Fatalf("traversal %q leaked outside content:\n%s", p, frag)
		}
	}
}

// SECURITY: a symlink pointing outside the root is refused.
func TestFilesSymlinkEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "sym-secret.txt")
	writeFile(t, outside, "SECRET VIA SYMLINK")
	t.Cleanup(func() { _ = os.Remove(outside) })
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if frag, ok := FileViewHTML(root, "escape", "TOK"); ok && strings.Contains(frag, "SECRET VIA SYMLINK") {
		t.Fatalf("symlink escaped the root:\n%s", frag)
	}
}

// A binary file is flagged, not dumped.
func TestFileViewBinaryFlagged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "blob.bin"), "abc\x00def")
	frag, ok := FileViewHTML(dir, "blob.bin", "TOK")
	if !ok {
		t.Fatal("view not ok")
	}
	if !strings.Contains(frag, "binary") {
		t.Errorf("binary not flagged:\n%s", frag)
	}
}

// An absent config dir yields a graceful page, not a failure.
func TestFilesNoConfigDir(t *testing.T) {
	frag, ok := FilesListHTML("", "", "TOK")
	if !ok {
		t.Fatal("expected a page even with no config dir")
	}
	if !strings.Contains(frag, "No config dir") {
		t.Errorf("expected empty-state:\n%s", frag)
	}
}
