package secrets_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/secrets"
)

func setupSecrets(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		p := filepath.Join(dir, "ai-do-not-read.secrets.yaml")
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

const testSecretsYAML = `
secrets:
  GITHUB_TOKEN: ghp_abc123
  BRAVE_API: brave_xyz789
`

func TestSecretsLoad_Missing(t *testing.T) {
	home := setupSecrets(t, "")
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(s.All()) != 0 {
		t.Error("expected empty store")
	}
}

func TestSecretsLoad_Valid(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(all))
	}
	if all["GITHUB_TOKEN"] != "ghp_abc123" {
		t.Errorf("wrong value for GITHUB_TOKEN: %q", all["GITHUB_TOKEN"])
	}
	if all["BRAVE_API"] != "brave_xyz789" {
		t.Errorf("wrong value for BRAVE_API: %q", all["BRAVE_API"])
	}
}

func TestSecretsLoad_Malformed(t *testing.T) {
	home := setupSecrets(t, "this is: not: valid: yaml: at all\n  - bad\n")
	if _, err := secrets.Load(home); err == nil {
		t.Fatal("expected error parsing malformed yaml")
	}
}
