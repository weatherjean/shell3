//go:build unix

// quietmode.go implements QuietStore, which persists the /quiet toggle to
// disk. Quiet on = agent-initiated posts (⏰/🔔/✉️) send without a
// notification ping; replies to the user and ⚠️ failures always ring.
package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// QuietStore persists the /quiet toggle. Zero value or nil = not quiet.
type QuietStore struct {
	Path string
}

// Get reports the persisted toggle, treating every failure — nil store,
// missing file, corrupt JSON — as off. Quiet is a preference, never a
// reason to fail a send.
func (s *QuietStore) Get() bool {
	if s == nil || s.Path == "" {
		return false
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return false
	}
	var m struct {
		Quiet bool `json:"quiet"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	return m.Quiet
}

// Set writes {"quiet":…} to s.Path, creating parent directories.
func (s *QuietStore) Set(on bool) error {
	if s == nil || s.Path == "" {
		return fmt.Errorf("QuietStore.Set: empty Path")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, _ := json.Marshal(struct {
		Quiet bool `json:"quiet"`
	}{on})
	return os.WriteFile(s.Path, data, 0o600)
}
