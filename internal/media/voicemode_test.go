//go:build unix

package media

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestModeStoreGetNoFile(t *testing.T) {
	s := &ModeStore{Path: filepath.Join(t.TempDir(), "nonexistent.json")}
	got := s.Get("inbound")
	if got != "inbound" {
		t.Errorf("Get with no file: got %q, want %q", got, "inbound")
	}
}

func TestModeStoreSetAndGet(t *testing.T) {
	s := &ModeStore{Path: filepath.Join(t.TempDir(), "mode.json")}
	if err := s.Set("always"); err != nil {
		t.Fatalf("Set(always) failed: %v", err)
	}
	got := s.Get("inbound")
	if got != "always" {
		t.Errorf("Get after Set(always): got %q, want %q", got, "always")
	}
}

func TestModeStoreSetInvalidMode(t *testing.T) {
	dir := t.TempDir()
	s := &ModeStore{Path: filepath.Join(dir, "mode.json")}

	// Set a valid mode first
	if err := s.Set("off"); err != nil {
		t.Fatalf("Set(off) failed: %v", err)
	}

	// Try to set an invalid mode
	err := s.Set("loud")
	if err == nil {
		t.Fatal("Set(loud) should error, but got nil")
	}

	// File should be unchanged
	got := s.Get("inbound")
	if got != "off" {
		t.Errorf("Get after invalid Set: got %q, want %q", got, "off")
	}
}

func TestModeStoreCorruptedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode.json")

	// Write corrupted JSON
	if err := os.WriteFile(path, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	s := &ModeStore{Path: path}
	got := s.Get("inbound")
	if got != "inbound" {
		t.Errorf("Get with corrupted file: got %q, want %q", got, "inbound")
	}
}

func TestModeStoreSetEmptyPath(t *testing.T) {
	s := &ModeStore{Path: ""}
	err := s.Set("always")
	if err == nil {
		t.Fatal("Set with empty Path should error, but got nil")
	}
}

func TestModeStoreValidModes(t *testing.T) {
	tests := []string{"off", "inbound", "always"}
	for _, mode := range tests {
		t.Run(mode, func(t *testing.T) {
			s := &ModeStore{Path: filepath.Join(t.TempDir(), "mode.json")}
			if err := s.Set(mode); err != nil {
				t.Fatalf("Set(%q) failed: %v", mode, err)
			}
			got := s.Get("default")
			if got != mode {
				t.Errorf("Get after Set(%q): got %q", mode, got)
			}
		})
	}
}

func TestModeStoreFileFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode.json")
	s := &ModeStore{Path: path}

	if err := s.Set("inbound"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify file content is valid JSON with "mode" field
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if m["mode"] != "inbound" {
		t.Errorf("JSON mode field: got %q, want %q", m["mode"], "inbound")
	}
}

func TestModeStoreFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode.json")
	s := &ModeStore{Path: path}

	if err := s.Set("always"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	// Check that file permissions are 0o644 (rw-r--r--)
	if info.Mode().Perm() != 0o644 {
		t.Errorf("File permissions: got %o, want %o", info.Mode().Perm(), 0o644)
	}
}
