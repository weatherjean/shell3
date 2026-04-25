package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".shell3"), 0755)
	yaml := "providers:\n  openai:\n    api_key: sk-test123\n    base_url: https://api.openai.com/v1\n"
	os.WriteFile(filepath.Join(dir, ".shell3", "credentials.yaml"), []byte(yaml), 0644)

	creds, err := config.LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := creds.Providers["openai"]
	if !ok {
		t.Fatal("expected openai provider")
	}
	if p.APIKey != "sk-test123" {
		t.Errorf("got api_key %q", p.APIKey)
	}
}

func TestLoadCredentials_Missing(t *testing.T) {
	if _, err := config.LoadCredentials(t.TempDir()); err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestCredentials_First_AlphabeticalOrder(t *testing.T) {
	creds := &config.Credentials{
		Providers: map[string]config.ProviderCredentials{
			"z-provider": {BaseURL: "http://z"},
			"a-provider": {BaseURL: "http://a"},
		},
	}
	name, p, ok := creds.First()
	if !ok {
		t.Fatal("expected a provider")
	}
	if name != "a-provider" {
		t.Errorf("got %q, want a-provider", name)
	}
	if p.BaseURL != "http://a" {
		t.Errorf("got base_url %q", p.BaseURL)
	}
}

func TestCredentials_Get(t *testing.T) {
	creds := &config.Credentials{Providers: map[string]config.ProviderCredentials{"openai": {APIKey: "sk-abc"}}}
	p, err := creds.Get("openai")
	if err != nil {
		t.Fatal(err)
	}
	if p.APIKey != "sk-abc" {
		t.Errorf("got api_key %q", p.APIKey)
	}
}

func TestCredentials_Get_Missing(t *testing.T) {
	creds := &config.Credentials{Providers: map[string]config.ProviderCredentials{}}
	if _, err := creds.Get("ghost"); err == nil {
		t.Error("expected error for missing provider")
	}
}
