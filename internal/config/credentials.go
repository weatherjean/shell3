package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProviderCredentials holds API key and base URL for one LLM provider.
type ProviderCredentials struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
}

// Credentials holds provider credentials loaded from ~/.shell3/credentials.yaml.
type Credentials struct {
	Providers map[string]ProviderCredentials `yaml:"providers"`
}

// LoadCredentials reads ~/.shell3/credentials.yaml. Pass os.UserHomeDir() result as homeDir.
func LoadCredentials(homeDir string) (*Credentials, error) {
	path := filepath.Join(homeDir, ".shell3", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config: no credentials found — run: shell3 auth")
		}
		return nil, fmt.Errorf("config: read credentials: %w", err)
	}
	var creds Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("config: invalid credentials.yaml: %w", err)
	}
	return &creds, nil
}

// WriteCredentials upserts provider credentials into homeDir/.shell3/credentials.yaml.
func WriteCredentials(homeDir, provider, apiKey, baseURL string) error {
	shell3Dir := filepath.Join(homeDir, ".shell3")
	if err := os.MkdirAll(shell3Dir, 0700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", shell3Dir, err)
	}
	path := filepath.Join(shell3Dir, "credentials.yaml")

	creds := &Credentials{Providers: map[string]ProviderCredentials{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(data, creds)
	}
	if creds.Providers == nil {
		creds.Providers = map[string]ProviderCredentials{}
	}

	creds.Providers[provider] = ProviderCredentials{APIKey: apiKey, BaseURL: baseURL}

	data, err := yaml.Marshal(creds)
	if err != nil {
		return fmt.Errorf("config: marshal credentials: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("config: write credentials: %w", err)
	}
	return nil
}

// Get returns credentials for the named provider.
func (c *Credentials) Get(provider string) (ProviderCredentials, error) {
	p, ok := c.Providers[provider]
	if !ok {
		return ProviderCredentials{}, fmt.Errorf("config: no credentials for provider %q — run: shell3 auth", provider)
	}
	return p, nil
}
