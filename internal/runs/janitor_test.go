package runs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// touchFile creates a file with the given content and sets its mtime.
func touchFile(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestSweepRemovesOldDir(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	touchFile(t, filepath.Join(root, "runs", "old-sess", "meta.json"), old)

	removed, err := Sweep(root, 30*24*time.Hour, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(removed) != 1 || removed[0] != "old-sess" {
		t.Fatalf("removed = %v, want [old-sess]", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "old-sess")); !os.IsNotExist(err) {
		t.Fatalf("old-sess dir still exists: %v", err)
	}
}

func TestSweepKeepsFreshDir(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-1 * time.Hour)
	touchFile(t, filepath.Join(root, "runs", "fresh-sess", "meta.json"), fresh)

	removed, err := Sweep(root, 30*24*time.Hour, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "fresh-sess")); err != nil {
		t.Fatalf("fresh-sess dir should still exist: %v", err)
	}
}

func TestSweepKeepZeroIsNoOp(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	old := now.Add(-365 * 24 * time.Hour)
	touchFile(t, filepath.Join(root, "runs", "ancient-sess", "meta.json"), old)

	removed, err := Sweep(root, 0, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none (keep=0 disables sweep)", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "ancient-sess")); err != nil {
		t.Fatalf("ancient-sess dir should still exist with keep=0: %v", err)
	}
}

// TestSweepAgeIsNewestFileMtime verifies age is computed from the NEWEST file
// inside the run dir, not the dir's own mtime or an arbitrarily-picked file:
// an old meta.json alongside a just-appended messages.jsonl must count as
// fresh (a live session mid-turn must never be swept out from under it).
func TestSweepAgeIsNewestFileMtime(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	sessDir := filepath.Join(root, "runs", "mixed-sess")
	touchFile(t, filepath.Join(sessDir, "meta.json"), now.Add(-90*24*time.Hour))
	touchFile(t, filepath.Join(sessDir, "messages.jsonl"), now.Add(-1*time.Hour))

	removed, err := Sweep(root, 30*24*time.Hour, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none (newest file in dir is fresh)", removed)
	}
	if _, err := os.Stat(sessDir); err != nil {
		t.Fatalf("mixed-sess dir should still exist: %v", err)
	}
}

// TestSweepFailOpenOneBadDir proves an unremovable run dir doesn't abort the
// sweep: an old dir made undeletable (its own permissions locked down so
// os.RemoveAll fails) is skipped, but another old dir alongside it still gets
// swept, and Sweep reports an error for the caller to log without losing the
// ids it DID remove.
func TestSweepFailOpenOneBadDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions; can't simulate an undeletable dir")
	}
	root := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)

	badDir := filepath.Join(root, "runs", "locked-sess")
	touchFile(t, filepath.Join(badDir, "meta.json"), old)
	goodDir := filepath.Join(root, "runs", "old-sess")
	touchFile(t, filepath.Join(goodDir, "meta.json"), old)

	// Lock the parent dir so RemoveAll can't unlink its child entry, without
	// preventing os.ReadDir(runs/) itself from listing it.
	if err := os.Chmod(badDir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(badDir, 0o755) })

	removed, err := Sweep(root, 30*24*time.Hour, now)
	if err == nil {
		t.Fatal("expected a reported error for the unremovable dir")
	}
	if len(removed) != 1 || removed[0] != "old-sess" {
		t.Fatalf("removed = %v, want [old-sess] (locked-sess must not abort the sweep)", removed)
	}
	if _, err := os.Stat(goodDir); !os.IsNotExist(err) {
		t.Fatalf("old-sess dir should have been removed: %v", err)
	}
	if _, err := os.Stat(badDir); err != nil {
		t.Fatalf("locked-sess dir should still exist (removal failed, skipped): %v", err)
	}
}

func TestSweepEmptyRunsDirNoOp(t *testing.T) {
	root := t.TempDir()
	removed, err := Sweep(root, 30*24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Sweep on missing runs dir: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none", removed)
	}
}
