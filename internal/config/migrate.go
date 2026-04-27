package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Migrate imports legacy ~/.shell3/credentials.yaml and
// ~/.shell3/codex_tokens.json into the unified CredStore at
// ~/.shell3/credentials.shell3. Idempotent.
func Migrate(homeDir string) error {
	store, err := LoadCredStore(homeDir)
	if err != nil {
		return err
	}
	if err := migrateLegacyYAML(homeDir, store); err != nil {
		return err
	}
	if err := migrateCodexTokens(homeDir, store); err != nil {
		return err
	}
	return nil
}

func migrateLegacyYAML(homeDir string, store *CredStore) error {
	path := filepath.Join(homeDir, ".shell3", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: read legacy credentials: %w", err)
	}
	var legacy struct {
		Providers map[string]struct {
			APIKey       string `yaml:"api_key"`
			BaseURL      string `yaml:"base_url"`
			DefaultModel string `yaml:"default_model,omitempty"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("config: parse legacy credentials: %w", err)
	}
	for instance, p := range legacy.Providers {
		fields := map[string]string{
			"api_key":       p.APIKey,
			"base_url":      p.BaseURL,
			"default_model": p.DefaultModel,
		}
		if err := store.Set(instance, "openai", fields); err != nil {
			return err
		}
	}
	if err := os.Rename(path, path+".bak"); err != nil {
		return fmt.Errorf("config: backup legacy credentials: %w", err)
	}
	return nil
}

func migrateCodexTokens(homeDir string, store *CredStore) error {
	path := filepath.Join(homeDir, ".shell3", "codex_tokens.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: read legacy codex tokens: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("config: parse legacy codex tokens: %w", err)
	}
	fields := map[string]string{}
	for _, k := range []string{"access_token", "refresh_token", "id_token", "account_id", "plan_type", "expires_at"} {
		if v, ok := raw[k]; ok {
			fields[k] = fmt.Sprint(v)
		}
	}
	if err := store.Set("codex", "codex", fields); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("config: remove legacy codex tokens: %w", err)
	}
	return nil
}
