// Package secrets manages project-scoped tool secrets stored under
// <projectDir>/.shell3/secrets.shell3. The on-disk file is wrapped with
// the same XOR obfuscation as the credential store; this defends
// against accidental disclosure (e.g. an LLM tool reading the file
// verbatim), not against a determined attacker.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/config"
)

type secretsFile struct {
	Version int               `yaml:"version"`
	Secrets map[string]string `yaml:"secrets"`
}

// Store is the project secrets store. Keys are environment-variable
// style names; values are raw secret strings.
type Store struct {
	projectDir string

	mu   sync.Mutex
	data secretsFile
}

// Load reads <projectDir>/.shell3/secrets.shell3 if present. The
// .shell3/ directory must exist (project must be inited); otherwise
// Load returns an error directing the user to run `shell3 init`.
func Load(projectDir string) (*Store, error) {
	shell3Dir := filepath.Join(projectDir, ".shell3")
	if _, err := os.Stat(shell3Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("secrets: no .shell3/ in %s — run `shell3 init`", projectDir)
		}
		return nil, fmt.Errorf("secrets: stat %s: %w", shell3Dir, err)
	}

	s := &Store{
		projectDir: projectDir,
		data:       secretsFile{Version: 1, Secrets: map[string]string{}},
	}
	blob, err := os.ReadFile(secretsPath(projectDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("secrets: read: %w", err)
	}
	plain, err := config.Unwrap(blob)
	if err != nil {
		return nil, fmt.Errorf("secrets: unwrap: %w", err)
	}
	if err := yaml.Unmarshal(plain, &s.data); err != nil {
		return nil, fmt.Errorf("secrets: parse: %w", err)
	}
	if s.data.Secrets == nil {
		s.data.Secrets = map[string]string{}
	}
	return s, nil
}

func secretsPath(projectDir string) string {
	return filepath.Join(projectDir, ".shell3", "secrets.shell3")
}

// List returns secret names sorted alphabetically. Values are never
// returned by this method.
func (s *Store) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data.Secrets))
	for k := range s.data.Secrets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// All returns a copy of every key/value pair. Used at runtime to seed
// tool secret availability.
func (s *Store) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.data.Secrets))
	for k, v := range s.data.Secrets {
		out[k] = v
	}
	return out
}

// Get returns the raw value for a key, if present.
func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Secrets[key]
	return v, ok
}

// Set writes (or overwrites) one secret and persists.
func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Secrets[key] = value
	return s.saveLocked()
}

// Remove deletes a secret. No-op if absent.
func (s *Store) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Secrets[key]; !ok {
		return nil
	}
	delete(s.data.Secrets, key)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	dir := filepath.Join(s.projectDir, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("secrets: mkdir: %w", err)
	}
	plain, err := yaml.Marshal(s.data)
	if err != nil {
		return fmt.Errorf("secrets: marshal: %w", err)
	}
	wrapped := config.Wrap(plain)
	path := secretsPath(s.projectDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, wrapped, 0600); err != nil {
		return fmt.Errorf("secrets: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("secrets: rename: %w", err)
	}
	return nil
}
