//go:build unix

// voicemode.go implements ModeStore, which persists the runtime /voice
// override to disk. Zero value (Path="") means no file, no override.
//
// This used to live in internal/media as media.ModeStore. It is a Telegram
// front-end concern, so it lives here rather than in the shared media
// package.
package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ModeStore persists the runtime /voice override. Zero value = no file, no override.
type ModeStore struct {
	Path string
}

// Get returns the persisted override, falling back to configDefault for every
// failure — missing file, unreadable, corrupt, or a mode this build does not
// know. A /voice override is a preference, never a reason to fail a turn.
func (s *ModeStore) Get(configDefault string) string {
	if s.Path == "" {
		return configDefault
	}

	data, err := os.ReadFile(s.Path)
	if err != nil {
		return configDefault
	}

	var m struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return configDefault
	}
	if !validMode(m.Mode) {
		return configDefault
	}
	return m.Mode
}

// validMode reports whether mode is one the front-end understands. Get treats
// anything else as no override; Set refuses to persist it.
func validMode(mode string) bool {
	return mode == "off" || mode == "inbound" || mode == "always"
}

// Set validates mode (must be "off", "inbound", or "always") and writes
// a JSON file {"mode":"…"} to s.Path. Returns error if Path is empty
// or mode is invalid.
func (s *ModeStore) Set(mode string) error {
	if s.Path == "" {
		return fmt.Errorf("ModeStore.Set: empty Path")
	}
	if !validMode(mode) {
		return fmt.Errorf("ModeStore.Set: invalid mode %q (must be off, inbound, or always)", mode)
	}

	data, err := json.Marshal(map[string]string{"mode": mode})
	if err != nil {
		return fmt.Errorf("ModeStore.Set: marshal failed: %w", err)
	}

	if err := atomicWrite(s.Path, data, 0o644); err != nil {
		return fmt.Errorf("ModeStore.Set: write failed: %w", err)
	}

	return nil
}

// atomicWrite writes data to path via a temp file in the same directory
// followed by a rename, so a crash mid-write leaves either the old contents or
// the new ones — never a truncated file Get would have to fall back over.
// Mirrors cmd/shell3's atomicWriteFile, which is in package main.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	// Sync before rename, or a power loss can leave the renamed file empty on
	// some filesystems — exactly the corruption this is here to prevent.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
