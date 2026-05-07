package secrets_test

import (
	"os"
	"path/filepath"
	"reflect"
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
	if len(s.List()) != 0 {
		t.Error("expected empty store")
	}
}

func TestSecretsLoad_Valid(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.List()) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(s.List()))
	}
}

func TestSecretsGet(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, _ := secrets.Load(home)
	v, ok := s.Get("GITHUB_TOKEN")
	if !ok {
		t.Fatal("expected GITHUB_TOKEN to exist")
	}
	if v != "ghp_abc123" {
		t.Errorf("wrong value: %q", v)
	}
}

func TestSecretsSet(t *testing.T) {
	home := setupSecrets(t, "")
	s, _ := secrets.Load(home)
	if err := s.Set("MY_KEY", "my_val"); err != nil {
		t.Fatal(err)
	}
	v, ok := s.Get("MY_KEY")
	if !ok || v != "my_val" {
		t.Errorf("expected MY_KEY=my_val after Set, got ok=%v v=%q", ok, v)
	}
	s2, _ := secrets.Load(home)
	v2, ok2 := s2.Get("MY_KEY")
	if !ok2 || v2 != "my_val" {
		t.Error("Set did not persist to disk")
	}
}

func TestSecretsRemove(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, _ := secrets.Load(home)
	if err := s.Remove("GITHUB_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("GITHUB_TOKEN"); ok {
		t.Error("expected GITHUB_TOKEN gone after Remove")
	}
	s2, _ := secrets.Load(home)
	if _, ok := s2.Get("GITHUB_TOKEN"); ok {
		t.Error("Remove did not persist to disk")
	}
}

func TestSecretsList_Sorted(t *testing.T) {
	home := setupSecrets(t, "")
	s, _ := secrets.Load(home)
	_ = s.Set("ZED", "z")
	_ = s.Set("ALPHA", "a")
	_ = s.Set("MID", "m")
	got := s.List()
	want := []string{"ALPHA", "MID", "ZED"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List: got %v, want %v", got, want)
	}
}

func TestSecretsAll(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, _ := secrets.Load(home)
	all := s.All()
	if all["GITHUB_TOKEN"] != "ghp_abc123" {
		t.Errorf("All() wrong value: %v", all)
	}
}

func TestSecretsRemove_Missing_NoError(t *testing.T) {
	home := setupSecrets(t, "")
	s, _ := secrets.Load(home)
	if err := s.Remove("NONEXISTENT"); err != nil {
		t.Fatalf("Remove of missing key should be no-op, got: %v", err)
	}
}
