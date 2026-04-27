package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// instanceRecord is the on-disk shape of a single credential instance.
type instanceRecord struct {
	Adapter string            `yaml:"adapter"`
	Fields  map[string]string `yaml:"fields"`
}

// credsFile is the on-disk root object inside the obfuscated body.
type credsFile struct {
	Version   int                       `yaml:"version"`
	Instances map[string]instanceRecord `yaml:"instances"`
}

// InstanceMeta is the public summary of one configured instance.
type InstanceMeta struct {
	Instance string
	Adapter  string
}

// CredStore is the unified credential store backed by
// ~/.shell3/credentials.shell3. Instances are keyed by user-chosen name
// (e.g. "ollama-local", "codex"); each record carries its adapter name
// and a flat string-keyed bag of fields. The on-disk file is XOR-
// obfuscated (see obfuscate.go) and never written in plaintext.
type CredStore struct {
	homeDir string

	mu   sync.Mutex
	data credsFile
}

// LoadCredStore reads ~/.shell3/credentials.shell3 if present, otherwise
// returns an empty store ready for Set/Save.
func LoadCredStore(homeDir string) (*CredStore, error) {
	c := &CredStore{
		homeDir: homeDir,
		data:    credsFile{Version: 1, Instances: map[string]instanceRecord{}},
	}
	path := credsPath(homeDir)
	blob, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	plaintext, err := Unwrap(blob)
	if err != nil {
		return nil, fmt.Errorf("config: unwrap %s: %w", path, err)
	}
	if err := yaml.Unmarshal(plaintext, &c.data); err != nil {
		return nil, fmt.Errorf("config: parse credentials: %w", err)
	}
	if c.data.Instances == nil {
		c.data.Instances = map[string]instanceRecord{}
	}
	return c, nil
}

// credsPath returns the canonical on-disk path.
func credsPath(homeDir string) string {
	return filepath.Join(homeDir, ".shell3", "credentials.shell3")
}

// Set writes (or overwrites) one instance and persists immediately.
func (c *CredStore) Set(instance, adapter string, fields map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(map[string]string, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	c.data.Instances[instance] = instanceRecord{Adapter: adapter, Fields: cp}
	return c.saveLocked()
}

// Get returns the adapter name and a copy of the field bag.
func (c *CredStore) Get(instance string) (adapter string, fields map[string]string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.data.Instances[instance]
	if !ok {
		return "", nil, false
	}
	out := make(map[string]string, len(rec.Fields))
	for k, v := range rec.Fields {
		out[k] = v
	}
	return rec.Adapter, out, true
}

// Update applies fn to a snapshot of the instance's fields and persists
// the result.
func (c *CredStore) Update(instance string, fn func(fields map[string]string) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.data.Instances[instance]
	if !ok {
		return fmt.Errorf("config: no instance %q", instance)
	}
	cp := make(map[string]string, len(rec.Fields))
	for k, v := range rec.Fields {
		cp[k] = v
	}
	if err := fn(cp); err != nil {
		return err
	}
	rec.Fields = cp
	c.data.Instances[instance] = rec
	return c.saveLocked()
}

// Delete removes an instance.
func (c *CredStore) Delete(instance string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.data.Instances[instance]; !ok {
		return nil
	}
	delete(c.data.Instances, instance)
	return c.saveLocked()
}

// List returns the configured instances sorted by name.
func (c *CredStore) List() []InstanceMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]InstanceMeta, 0, len(c.data.Instances))
	for name, rec := range c.data.Instances {
		out = append(out, InstanceMeta{Instance: name, Adapter: rec.Adapter})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

// HomeDir returns the home directory the store was loaded against.
func (c *CredStore) HomeDir() string { return c.homeDir }

// saveLocked marshals data, wraps with the obfuscation layer, and writes
// atomically. Caller must hold c.mu.
func (c *CredStore) saveLocked() error {
	dir := filepath.Join(c.homeDir, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	plaintext, err := yaml.Marshal(c.data)
	if err != nil {
		return fmt.Errorf("config: marshal credentials: %w", err)
	}
	wrapped := Wrap(plaintext)
	path := credsPath(c.homeDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, wrapped, 0600); err != nil {
		return fmt.Errorf("config: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: rename tmp: %w", err)
	}
	return nil
}
