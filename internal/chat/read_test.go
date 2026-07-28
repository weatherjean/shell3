package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadTool_WholeSmallFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "line1\nline2\nline3\n")
	out := handleReadTool(context.Background(), `{"path":"a.txt"}`, dir)
	if !strings.Contains(out, "line1") || !strings.Contains(out, "line3") {
		t.Fatalf("missing content: %q", out)
	}
	if strings.Contains(out, "Use offset=") {
		t.Fatalf("small file should have no continuation footer: %q", out)
	}
}

func TestReadTool_LineLimitFooter(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString("x\n")
	}
	writeFile(t, dir, "big.txt", sb.String())
	out := handleReadTool(context.Background(), `{"path":"big.txt"}`, dir)
	if !strings.Contains(out, "Use offset=2001 to continue") {
		t.Fatalf("expected line-limit footer with next offset, got tail: %q", out[max(0, len(out)-120):])
	}
}

func TestReadTool_Offset(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "n.txt", "a\nb\nc\nd\n")
	out := handleReadTool(context.Background(), `{"path":"n.txt","offset":3,"limit":1}`, dir)
	if !strings.Contains(out, "c") || strings.Contains(out, "a\n") {
		t.Fatalf("offset/limit wrong: %q", out)
	}
}

func TestReadTool_BinaryRedirect(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bin", "ab\x00cd\x00ef")
	out := handleReadTool(context.Background(), `{"path":"bin"}`, dir)
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "read_media") {
		t.Fatalf("binary should redirect to read_media: %q", out)
	}
}

// A tiny binary file (under the old 32-byte sniff floor) with no NUL but mostly
// non-printable bytes must still be flagged binary, not rendered as mojibake.
func TestReadTool_TinyBinaryFlagged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tiny.bin", "\x01\x02\x03\x04\x05\x06\x07\x08\x0e\x0f\x10\x11")
	out := handleReadTool(context.Background(), `{"path":"tiny.bin"}`, dir)
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "read_media") {
		t.Fatalf("a tiny mostly-non-printable file should be flagged binary, got %q", out)
	}
}

// A tiny text file with a single stray control byte must NOT be flagged binary.
func TestReadTool_TinyTextWithStrayByteNotFlagged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tiny.txt", "hi\x07there")
	out := handleReadTool(context.Background(), `{"path":"tiny.txt"}`, dir)
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("a tiny text file with one stray control byte should not be flagged binary, got %q", out)
	}
}

func TestReadTool_Errors(t *testing.T) {
	dir := t.TempDir()
	if out := handleReadTool(context.Background(), `{"path":"nope.txt"}`, dir); !strings.HasPrefix(out, "error:") || !strings.Contains(out, "not found") {
		t.Fatalf("missing-file error wrong: %q", out)
	}
	if out := handleReadTool(context.Background(), `{"path":"."}`, dir); !strings.HasPrefix(out, "error:") || !strings.Contains(out, "directory") {
		t.Fatalf("directory error wrong: %q", out)
	}
}

func TestReadTool_ByteCapTruncatesBeforeLineCap(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	// 100 lines of ~1 KB each = ~100 KB, well under the 2000-line cap but over the
	// 50 KB byte cap — so the byte cap must trigger the continuation footer.
	for i := 0; i < 100; i++ {
		sb.WriteString(strings.Repeat("x", 1000))
		sb.WriteByte('\n')
	}
	writeFile(t, dir, "wide.txt", sb.String())
	out := handleReadTool(context.Background(), `{"path":"wide.txt"}`, dir)
	if !strings.Contains(out, "Use offset=") {
		t.Fatalf("byte cap should emit a continuation footer: %q", out[max(0, len(out)-120):])
	}
	if len(out) > maxReadBytes+1024 {
		t.Fatalf("output should respect the byte cap, got %d bytes", len(out))
	}
}

func TestReadTool_GiantSingleLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.txt", strings.Repeat("z", maxReadBytes+10))
	out := handleReadTool(context.Background(), `{"path":"one.txt"}`, dir)
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "exceeds") {
		t.Fatalf("oversize single line should be a clean error: %q", out)
	}
}

func TestReadTool_OffsetBeyondEOF(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "s.txt", "a\nb\nc\n")
	out := handleReadTool(context.Background(), `{"path":"s.txt","offset":99}`, dir)
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "beyond end of file") {
		t.Fatalf("offset past EOF should error: %q", out)
	}
}

func TestReadTool_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	if out := handleReadTool(context.Background(), `{"path":"empty.txt"}`, dir); out != "" {
		t.Fatalf("empty file should read as empty string, got %q", out)
	}
}

func TestReadTool_NoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "nn.txt", "alpha\nbeta\ngamma") // no final newline
	out := handleReadTool(context.Background(), `{"path":"nn.txt"}`, dir)
	// Byte fidelity: a file with no final newline must read back verbatim, with no
	// fabricated trailing "\n" — otherwise a whole-tail copy fails to exact-match
	// in edit_file. (Presence-only checks would miss this.)
	if out != "alpha\nbeta\ngamma" {
		t.Fatalf("non-verbatim read of a no-final-newline file: %q", out)
	}
	if strings.Contains(out, "Use offset=") {
		t.Fatalf("a 3-line file should have no continuation footer: %q", out)
	}
	// The mirror case: a file that DOES end in "\n" reads back with that newline.
	writeFile(t, dir, "tn.txt", "alpha\nbeta\n")
	if out := handleReadTool(context.Background(), `{"path":"tn.txt"}`, dir); out != "alpha\nbeta\n" {
		t.Fatalf("trailing-newline file should keep its final newline: %q", out)
	}
}

// A directory read must surface as the directory message.
func TestReadTool_DirError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := handleReadTool(context.Background(), `{"path":"sub"}`, dir)
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "directory") {
		t.Fatalf("directory read should map to directory message, got %q", out)
	}
}

func TestReadTool_FileSizeCeiling(t *testing.T) {
	dir := t.TempDir()
	// A sparse file just over the in-memory ceiling: the backend reads it, then
	// the len(content) cap refuses to render it.
	p := filepath.Join(dir, "huge.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxReadFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out := handleReadTool(context.Background(), `{"path":"huge.bin"}`, dir)
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "read limit") {
		t.Fatalf("oversize file should be refused with a redirect: %q", out)
	}
}
