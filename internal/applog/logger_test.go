package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLoggerWritesLevels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	lg, closer, err := Open(path, 2*1024*1024, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()

	lg.Debug("debug msg", "k", "v")
	lg.Warn("warn msg", "k", "v")
	lg.Error("error msg", os.ErrNotExist, "k", "v")

	_ = closer.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "[DEBUG] debug msg") {
		t.Errorf("missing debug line:\n%s", s)
	}
	if !strings.Contains(s, "[WARN] warn msg") {
		t.Errorf("missing warn line:\n%s", s)
	}
	if !strings.Contains(s, "[ERROR] error msg") {
		t.Errorf("missing error line:\n%s", s)
	}
	if !strings.Contains(s, "k=v") {
		t.Errorf("missing field:\n%s", s)
	}
	if !strings.Contains(s, "error=file does not exist") {
		t.Errorf("missing error field:\n%s", s)
	}
}

func TestRotateOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.log")

	// Write a file larger than 10 bytes to trigger rotation.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 20)), 0644); err != nil {
		t.Fatal(err)
	}

	lg, closer, err := Open(path, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()

	lg.Debug("after rotate")
	_ = closer.Close()

	// Original must have been rotated to .1
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated archive at %s.1: %v", path, err)
	}
	// New log file must exist with the new entry.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "after rotate") {
		t.Errorf("new log file missing expected content:\n%s", string(data))
	}
}

func TestRotateKeepsMaxArchives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.log")

	// Pre-populate archives .1 .2 .3 — opening with maxArchives=3 should
	// delete .3 and shift .1→.2, .2→.3.
	for i := 1; i <= 3; i++ {
		content := strings.Repeat("x", 5)
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(path)+"."+string(rune('0'+i))), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Main log: large enough to trigger rotation.
	if err := os.WriteFile(path, []byte(strings.Repeat("y", 20)), 0644); err != nil {
		t.Fatal(err)
	}

	lg, closer, _ := Open(path, 10, 3)
	defer closer.Close()
	lg.Debug("new")
	_ = closer.Close()

	// .3 should now be former .2 (content "xxxxx"), not the original .3.
	// Former .3 was deleted; former .2 shifted to .3.
	if _, err := os.Stat(path + ".3"); err != nil {
		t.Fatalf("expected archive .3 to exist: %v", err)
	}
	// .4 must not exist.
	if _, err := os.Stat(path + ".4"); err == nil {
		t.Errorf("unexpected archive .4")
	}
}

func TestNoopLogger(t *testing.T) {
	var lg Logger = Noop{}
	lg.Debug("d")
	lg.Warn("w")
	lg.Error("e", os.ErrNotExist)
}
