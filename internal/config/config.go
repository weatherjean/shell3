// Package config loads per-project and global credential configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Hooks holds shell command paths for each lifecycle hook.
type Hooks struct {
	OnSessionStart string `yaml:"on_session_start"`
	OnSessionEnd   string `yaml:"on_session_end"`
	OnTurnStart    string `yaml:"on_turn_start"`
	OnTurnEnd      string `yaml:"on_turn_end"`
	OnToolCall     string `yaml:"on_tool_call"`
	OnToolResult   string `yaml:"on_tool_result"`
	OnContextBuild string `yaml:"on_context_build"`
	OnError        string `yaml:"on_error"`
}

// ProjectConfig is loaded from .shell3/config.yaml in the project directory.
type ProjectConfig struct {
	Model    string `yaml:"model"`
	Provider string `yaml:"provider"`
	MemoryDB string `yaml:"memory_db"`
	HistoryMD          string `yaml:"history_md"`
	Hooks              Hooks  `yaml:"hooks"`
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
