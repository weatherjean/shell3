package config

import (
	"fmt"
	"sort"
	"sync"

	"github.com/weatherjean/shell3/internal/obfile"
	"github.com/weatherjean/shell3/internal/paths"
)

type instanceRecord struct {
	Adapter string            `yaml:"adapter"`
	Fields  map[string]string `yaml:"fields"`
}

// credsFile is the on-disk root object.
type credsFile struct {
	Instances map[string]instanceRecord `yaml:"instances"`
}

// InstanceMeta is the public summary of one configured instance.
type InstanceMeta struct {
	Instance string
	Adapter  string
}

// CredStore is the unified credential store backed by ~/.shell3/credentials.shell3.
// Instances are keyed by user-chosen name; each record carries its adapter name
// and a flat string-keyed bag of fields. The on-disk file is XOR-obfuscated
// and never written in plaintext.
type CredStore struct {
	path string

	mu   sync.Mutex
	data credsFile
}

// LoadCredStore reads ~/.shell3/credentials.shell3 if present, otherwise
// returns an empty store ready for Set/Save.
func LoadCredStore(homeDir string) (*CredStore, error) {
	c := &CredStore{
		path: paths.NewGlobal(homeDir).Auth,
		data: credsFile{Instances: map[string]instanceRecord{}},
	}
	if err := obfile.Read(c.path, &c.data); err != nil {
		return nil, fmt.Errorf("config: load credentials: %w", err)
	}
	if c.data.Instances == nil {
		c.data.Instances = map[string]instanceRecord{}
	}
	return c, nil
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

// Update applies fn to a snapshot of the instance's fields and persists.
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

func (c *CredStore) saveLocked() error {
	return obfile.Write(c.path, c.data)
}
