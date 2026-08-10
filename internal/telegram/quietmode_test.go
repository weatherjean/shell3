//go:build unix

package telegram

import (
	"os"
	"path/filepath"
	"testing"
)

// A missing file, a nil store, and a corrupt file all mean "not quiet" — the
// toggle is a preference, never a reason to fail.
func TestQuietStore_DefaultsOff(t *testing.T) {
	s := &QuietStore{Path: filepath.Join(t.TempDir(), "nonexistent.json")}
	if s.Get() {
		t.Error("missing file: Get() = true, want false")
	}
	var nilStore *QuietStore
	if nilStore.Get() {
		t.Error("nil store: Get() = true, want false")
	}
	p := filepath.Join(t.TempDir(), "quiet_mode.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if (&QuietStore{Path: p}).Get() {
		t.Error("corrupt file: Get() = true, want false")
	}
}

func TestQuietStore_SetRoundtrip(t *testing.T) {
	s := &QuietStore{Path: filepath.Join(t.TempDir(), "quiet_mode.json")}
	if err := s.Set(true); err != nil {
		t.Fatal(err)
	}
	if !s.Get() {
		t.Error("after Set(true): Get() = false, want true")
	}
	// A fresh store over the same path sees the persisted value (restart).
	if !(&QuietStore{Path: s.Path}).Get() {
		t.Error("fresh store over same path: Get() = false, want true")
	}
	if err := s.Set(false); err != nil {
		t.Fatal(err)
	}
	if s.Get() {
		t.Error("after Set(false): Get() = true, want false")
	}
}

func TestQuietStore_SetEmptyPath(t *testing.T) {
	if err := (&QuietStore{}).Set(true); err == nil {
		t.Error("Set on empty Path: want error, got nil")
	}
}
