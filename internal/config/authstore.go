package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ModelDef is one model entry in the auth YAML.
type ModelDef struct {
	ID            string `yaml:"id"`
	ContextWindow int    `yaml:"context_window"`
}

// Instance is one configured provider in the auth YAML.
type Instance struct {
	Name    string     `yaml:"name"`
	BaseURL string     `yaml:"base_url"`
	APIKey  string     `yaml:"api_key,omitempty"`
	Models  []ModelDef `yaml:"models"`
}

type authFile struct {
	Instances []Instance `yaml:"instances"`
}

// AuthStore reads ~/.shell3/ai-do-not-read.auth.yaml.
// It is read-only; the user edits the file directly.
type AuthStore struct {
	data authFile
}

// LoadAuthStore reads the auth YAML from homeDir. Returns an empty store if
// the file does not exist.
func LoadAuthStore(homeDir string) (*AuthStore, error) {
	path := filepath.Join(homeDir, ".shell3", "ai-do-not-read.auth.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &AuthStore{}, nil
		}
		return nil, err
	}
	var af authFile
	if err := yaml.Unmarshal(data, &af); err != nil {
		return nil, err
	}
	return &AuthStore{data: af}, nil
}

// Get returns the Instance with the given name.
func (s *AuthStore) Get(name string) (Instance, bool) {
	for _, inst := range s.data.Instances {
		if inst.Name == name {
			return inst, true
		}
	}
	return Instance{}, false
}

// List returns all configured instances in file order.
func (s *AuthStore) List() []Instance {
	out := make([]Instance, len(s.data.Instances))
	copy(out, s.data.Instances)
	return out
}
