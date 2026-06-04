package ref

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
)

// reloadWinner is only reached on the ErrExist create-race path, which requires
// .ref to already exist. If the winning writer created .ref but has not yet
// written its UUID, an observer must surface an error rather than return ""
// (which Load would otherwise do for empty/whitespace content).
func TestReloadWinnerEmptyRefErrors(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".shell3"), 0700); err != nil {
		t.Fatalf("mkdir .shell3: %v", err)
	}
	l := paths.NewLocal(cwd)
	// Simulate a winner that has O_CREATE'd .ref but not yet written the UUID.
	if err := os.WriteFile(l.Ref, []byte(""), 0600); err != nil {
		t.Fatalf("write empty .ref: %v", err)
	}
	id, err := reloadWinner(l)
	if err == nil {
		t.Fatalf("expected error for empty .ref, got nil (id=%q)", id)
	}
	if id != "" {
		t.Fatalf("expected empty id on error, got %q", id)
	}
}
