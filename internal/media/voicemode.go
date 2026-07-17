//go:build unix

// voicemode.go implements ModeStore, which persists the runtime /voice
// override to disk. Zero value (Path="") means no file, no override.
package media

import (
	"encoding/json"
	"fmt"
	"os"
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

	// Marshal to JSON
	data, err := json.Marshal(map[string]string{"mode": mode})
	if err != nil {
		return fmt.Errorf("ModeStore.Set: marshal failed: %w", err)
	}

	// Write file with 0o644 permissions
	if err := os.WriteFile(s.Path, data, 0o644); err != nil {
		return fmt.Errorf("ModeStore.Set: write failed: %w", err)
	}

	return nil
}
