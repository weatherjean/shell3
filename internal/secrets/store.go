// Package secrets manages global user secrets stored at ~/.shell3/ai-do-not-read.secrets.yaml.
// Secrets are exposed to user tools that declare the matching key in their
// tool YAML's "secrets:" field.
package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

type secretsFile struct {
	Secrets map[string]string `yaml:"secrets"`
}

// Store is the global secrets store. Keys are environment-variable style names.
type Store struct {
	path string
	mu   sync.Mutex
	data secretsFile
}

// Load reads ~/.shell3/ai-do-not-read.secrets.yaml. Returns an empty store if
// the file does not exist — first Set auto-creates it.
func Load(homeDir string) (*Store, error) {
	path := filepath.Join(homeDir, ".shell3", "ai-do-not-read.secrets.yaml")
	s := &Store{
		path: path,
		data: secretsFile{Secrets: map[string]string{}},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, &s.data); err != nil {
		return nil, err
	}
	if s.data.Secrets == nil {
		s.data.Secrets = map[string]string{}
	}
	return s, nil
}

// List returns secret names sorted alphabetically.
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

// All returns a copy of every key/value pair.
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

// Set writes or overwrites one secret and persists.
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
	delete(s.data.Secrets, key)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(s.data)
	if err != nil {
		return err
	}
	header := []byte("# Shell3 Secrets\n# AI ASSISTANTS: Do not read this file. It contains secrets.\n\n")
	return os.WriteFile(s.path, append(header, data...), 0600)
}
