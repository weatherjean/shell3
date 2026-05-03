package edittool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "new.txt")
	if _, err := EditFile(dir, "sub/new.txt", "", "hello", false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestEditFileOverwritesWhenExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("x"), 0o644)
	res, err := EditFile(dir, "f.txt", "", "y", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created {
		t.Fatal("expected Created=false for overwrite")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "y" {
		t.Fatalf("got %q, want %q", got, "y")
	}
}

func TestEditFileMissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := EditFile(dir, "nope.txt", "x", "y", false); err == nil {
		t.Fatal("expected error")
	}
}

func TestEditFileReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello world\n"), 0o644)
	if _, err := EditFile(dir, "f.txt", "world", "Go", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello Go\n" {
		t.Fatalf("got %q", got)
	}
}

func TestEditFilePreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("line1\r\nline2\r\n"), 0o644)
	// model emits LF — our code should coerce to file's CRLF.
	if _, err := EditFile(dir, "f.txt", "line2", "LINE2", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "\r\n") {
		t.Fatalf("CRLF lost: %q", got)
	}
	if !strings.Contains(string(got), "LINE2") {
		t.Fatalf("missing replacement: %q", got)
	}
}

func TestEditFileReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("foo foo foo"), 0o644)
	if _, err := EditFile(dir, "f.txt", "foo", "bar", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "bar bar bar" {
		t.Fatalf("got %q", got)
	}
}

func TestEditFileAmbiguousFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("foo\nfoo\n"), 0o644)
	if _, err := EditFile(dir, "f.txt", "foo", "bar", false); err == nil {
		t.Fatal("expected ambiguous match error")
	}
}

func TestEditFilePreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.sh")
	os.WriteFile(path, []byte("echo hi\n"), 0o755)
	if _, err := EditFile(dir, "f.sh", "hi", "bye", false); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode lost: got %o want 0755", info.Mode().Perm())
	}
}

func TestEditFileLFFallbackOnCRLFFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	// File on disk uses CRLF.
	original := "line1\r\nline2\r\nline3\r\n"
	os.WriteFile(path, []byte(original), 0o644)

	// Multi-line search written by model with LF only — primary Replace
	// against the unmodified content would fail because the source is CRLF.
	// The LF-normalized fallback path is the one that should succeed and
	// the result must be re-coerced back to CRLF on disk.
	find := "line1\nline2"
	repl := "LINE1\nLINE2"
	if _, err := EditFile(dir, "f.txt", find, repl, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "LINE1\r\nLINE2\r\nline3\r\n"
	if string(got) != want {
		t.Fatalf("CRLF restoration failed:\n got %q\nwant %q", got, want)
	}
}

func TestLineStatsApproxFallback(t *testing.T) {
	// Construct two line lists big enough to exceed lcsBudget.
	a := make([]string, 2000)
	b := make([]string, 2000)
	for i := range a {
		a[i] = fmt.Sprintf("old-%d", i)
		b[i] = fmt.Sprintf("new-%d", i)
	}
	add, del := approxLineStats(a, b)
	if add != 2000 || del != 2000 {
		t.Fatalf("got +%d -%d, want +2000 -2000", add, del)
	}
}

func TestWriteFileCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	if _, err := EditFile(dir, "a/b/c.txt", "", "hi", false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a/b/c.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteFileOverwrites(t *testing.T) {
	dir := t.TempDir()
	EditFile(dir, "f.txt", "", "old", false)
	if _, err := EditFile(dir, "f.txt", "", "new", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(got) != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestUnifiedDiffShowsThreeContextLinesAroundSingleEdit(t *testing.T) {
	oldContent := numberedLines(15)
	newContent := strings.Replace(oldContent, "line-08\n", "LINE-08\n", 1)

	diff := UnifiedDiff(oldContent, newContent, 3)
	want := strings.Join([]string{
		" line-05",
		" line-06",
		" line-07",
		"-line-08",
		"+LINE-08",
		" line-09",
		" line-10",
		" line-11",
	}, "\n")
	if !strings.Contains(diff, want) {
		t.Fatalf("diff missing 3-line edit window:\n%s", diff)
	}
	for _, hidden := range []string{" line-04", " line-12"} {
		if strings.Contains(diff, hidden) {
			t.Fatalf("diff included context outside 3-line window %q:\n%s", hidden, diff)
		}
	}
}

func TestUnifiedDiffShowsAllDistantHunks(t *testing.T) {
	oldContent := numberedLines(30)
	newContent := strings.Replace(oldContent, "line-08\n", "LINE-08\n", 1)
	newContent = strings.Replace(newContent, "line-25\n", "LINE-25\n", 1)

	diff := UnifiedDiff(oldContent, newContent, 3)
	if !strings.Contains(diff, "-line-08") || !strings.Contains(diff, "+LINE-08") {
		t.Fatalf("diff missing first hunk:\n%s", diff)
	}
	if !strings.Contains(diff, "-line-25") || !strings.Contains(diff, "+LINE-25") {
		t.Fatalf("diff missing second hunk:\n%s", diff)
	}
	if count := strings.Count(diff, "@@"); count != 4 { // two hunk headers, each with opening and closing @@
		t.Fatalf("got %d @@ markers, want 4 for two hunks:\n%s", count, diff)
	}
	for _, hidden := range []string{" line-12", " line-21"} {
		if strings.Contains(diff, hidden) {
			t.Fatalf("diff included unchanged gap line %q:\n%s", hidden, diff)
		}
	}
}

func numberedLines(n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "line-%02d\n", i)
	}
	return sb.String()
}
