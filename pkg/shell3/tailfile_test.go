package shell3

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tailFile must never return more than `max` bytes — these reads run on the TUI
// goroutine, so an unbounded read of a large (or actively growing) job log would
// freeze the UI. It also reports truncation so the caller can drop the partial
// first line.
func TestTailFile_CapsAndReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")

	// Large file: read is capped and truncation is reported.
	big := strings.Repeat("a", 10_000)
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	data, truncated, err := tailFile(path, 1024)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(data) > 1024 {
		t.Fatalf("returned %d bytes, exceeds cap 1024", len(data))
	}
	if !truncated {
		t.Fatal("a file larger than max must report truncated=true")
	}

	// Small file: full content, not flagged truncated.
	if err := os.WriteFile(path, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, truncated, err = tailFile(path, 1024)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if string(data) != "short" || truncated {
		t.Fatalf("small file: got %q truncated=%v, want %q false", data, truncated, "short")
	}
}
