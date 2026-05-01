// Package secrets manages global user secrets stored at ~/.shell3/secrets.shell3.
// Secrets are exposed to user tools that declare the matching key in their
// tool YAML's "secrets:" field. The on-disk file is XOR-obfuscated (see
// internal/obfuscate) — this defends against accidental disclosure (e.g. an
// LLM tool reading the file verbatim), not against a determined attacker.
package secrets

import (
	"fmt"
	"sort"
	"sync"

	"github.com/weatherjean/shell3/internal/obfile"
	"github.com/weatherjean/shell3/internal/paths"
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

// Load reads ~/.shell3/secrets.shell3. Returns an empty store if the file does
// not exist — first-use auto-creates on next Set.
func Load(homeDir string) (*Store, error) {
	g := paths.NewGlobal(homeDir)
	s := &Store{
		path: g.Secrets,
		data: secretsFile{Secrets: map[string]string{}},
	}
	if err := obfile.Read(s.path, &s.data); err != nil {
		return nil, fmt.Errorf("secrets: load: %w", err)
	}
	if s.data.Secrets == nil {
		s.data.Secrets = map[string]string{}
	}
	return s, nil
}

// List returns secret names sorted alphabetically. Values are never returned.
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

// All returns a copy of every key/value pair. Used at runtime to seed tool
// secret availability.
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
	return obfile.Write(s.path, s.data)
}

// Remove deletes a secret. No-op if absent.
func (s *Store) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Secrets, key)
	return obfile.Write(s.path, s.data)
}
