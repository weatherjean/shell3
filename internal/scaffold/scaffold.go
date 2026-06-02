// Package scaffold writes the starter shell3 configuration for new installs.
package scaffold

import (
	_ "embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed defaults/shell3.lua
var defaultConfig []byte

//go:embed defaults/env.example
var defaultEnvExample []byte

// WriteStarterConfig writes the embedded starter shell3.lua and .env template to
// the given destinations, but only if each file is absent. Safe to call on every
// run — existing files are never overwritten.
func WriteStarterConfig(configPath, envExamplePath string) error {
	if err := writeIfAbsent(configPath, defaultConfig, 0644); err != nil {
		return err
	}
	if err := writeIfAbsent(envExamplePath, defaultEnvExample, 0644); err != nil {
		return err
	}
	return nil
}

func writeIfAbsent(path string, content []byte, mode fs.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, content, mode)
}
