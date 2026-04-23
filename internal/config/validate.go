package config

import "fmt"

// Validate checks that cfg has required fields and creds has the configured provider.
func Validate(cfg *ProjectConfig, creds *Credentials) error {
	if cfg.Model == "" {
		return fmt.Errorf("config: model is required")
	}
	if cfg.Provider == "" {
		return fmt.Errorf("config: provider is required")
	}
	if _, err := creds.Get(cfg.Provider); err != nil {
		return err
	}
	return nil
}
