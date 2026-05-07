// Package secrets manages global user secrets stored at ~/.shell3/ai-do-not-read.secrets.yaml.
// Secrets are exposed to user tools that declare the matching key in their
// tool YAML's "secrets:" field.
package secrets

import (
	"errors"
	"os"
	"sync"

	"gopkg.in/yaml.v3"

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

// Load reads ~/.shell3/ai-do-not-read.secrets.yaml. Returns an empty store if
// the file does not exist — first Set auto-creates it.
func Load(homeDir string) (*Store, error) {
	path := paths.NewGlobal(homeDir).Secrets
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
