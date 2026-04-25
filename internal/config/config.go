// Package config loads per-project and global credential configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/hooks"
	"gopkg.in/yaml.v3"
)

// Hooks is an alias for hooks.Config so the two packages share one definition.
type Hooks = hooks.Config

// ProjectConfig is loaded from .shell3/config.yaml in the project directory.
type ProjectConfig struct {
	Model       string `yaml:"model"`
	Provider    string `yaml:"provider"`
	StoreDB     string `yaml:"store_db"`
	MemoryDB    string `yaml:"memory_db"`
	HistoryMD   string `yaml:"history_md"`
	Personality string `yaml:"personality"`
	Hooks       Hooks  `yaml:"hooks"`
}

// LoadProject reads .shell3/config.yaml from projectDir.
func LoadProject(projectDir string) (*ProjectConfig, error) {
	path := filepath.Join(projectDir, ".shell3", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config: no .shell3/config.yaml found — run: shell3 init")
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: invalid config.yaml: %w", err)
	}
	return &cfg, nil
}
