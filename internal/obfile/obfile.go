package obfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/obfuscate"
)

// Read decrypts and unmarshals the obfuscated YAML file at path into v.
// Returns nil (leaving v unchanged) if the file does not exist.
func Read(path string, v any) error {
	blob, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("obfile: read %s: %w", path, err)
	}
	plain, err := obfuscate.Unwrap(blob)
	if err != nil {
		return fmt.Errorf("obfile: unwrap %s: %w", path, err)
	}
	if err := yaml.Unmarshal(plain, v); err != nil {
		return fmt.Errorf("obfile: parse %s: %w", path, err)
	}
	return nil
}

// Write marshals v to YAML, obfuscates it, and writes atomically to path.
// Creates parent directories as needed (mode 0700).
func Write(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("obfile: mkdir: %w", err)
	}
	plain, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("obfile: marshal: %w", err)
	}
	wrapped := obfuscate.Wrap(plain)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, wrapped, 0600); err != nil {
		return fmt.Errorf("obfile: write tmp: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }() // no-op if rename succeeded
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("obfile: rename: %w", err)
	}
	return nil
}
