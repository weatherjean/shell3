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

// Get returns the persisted override if valid, otherwise configDefault.
// If the file doesn't exist or is corrupted, configDefault is returned
// without error.
func (s *ModeStore) Get(configDefault string) string {
	if s.Path == "" {
		return configDefault
	}

	data, err := os.ReadFile(s.Path)
	if err != nil {
		// File doesn't exist or is unreadable; return default.
		return configDefault
	}

	var m struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		// Corrupted/unparseable file; return default, never error.
		return configDefault
	}

	// Validate the mode field is one of the allowed values
	if m.Mode == "off" || m.Mode == "inbound" || m.Mode == "always" {
		return m.Mode
	}

	// Invalid mode stored in file; return default.
	return configDefault
}

// Set validates mode (must be "off", "inbound", or "always") and writes
// a JSON file {"mode":"…"} to s.Path. Returns error if Path is empty
// or mode is invalid.
func (s *ModeStore) Set(mode string) error {
	if s.Path == "" {
		return fmt.Errorf("ModeStore.Set: empty Path")
	}

	// Validate mode is one of the allowed values
	if mode != "off" && mode != "inbound" && mode != "always" {
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
