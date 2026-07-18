package chat

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file just over the cap must report a size strictly above the stated max —
// integer-truncating >>20 on both sides printed a self-contradictory
// "1 MB, max 1 MB" for a 1MB+5B file against a 1MB cap.
func TestReadMediaFileTooLargeCeilsSize(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(p, make([]byte, 1<<20+5), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := readMediaFile(p, dir, "video", 1<<20)
	if err == nil {
		t.Fatal("expected too-large error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "video too large (2 MB, max 1 MB)") {
		t.Fatalf("error = %q, want ceiled size above the stated max", got)
	}
}

// The bytes-based helpers must derive the stated max from the cap constant via
// the shared mediaTooLarge, not a hardcoded literal that lies after a cap bump.
func TestPDFPartFromBytesTooLargeDerivesMax(t *testing.T) {
	data := bytes.Repeat([]byte{0}, maxPDFBytes+1)
	_, _, err := pdfPartFromBytes(data, "big.pdf")
	if err == nil {
		t.Fatal("expected too-large error, got nil")
	}
	want := mediaTooLarge("pdf", int64(len(data)), maxPDFBytes).Error()
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
