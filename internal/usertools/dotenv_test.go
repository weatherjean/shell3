package usertools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	body := `# comment line
FOO=bar
EMPTY=
QUOTED="hello world"
WITH_EQ=a=b=c

# trailing blank lines below

`
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	want := map[string]string{
		"FOO":     "bar",
		"EMPTY":   "",
		"QUOTED":  "hello world",
		"WITH_EQ": "a=b=c",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra keys: %v", got)
	}
}

func TestLoadDotEnv_Missing(t *testing.T) {
	got, err := LoadDotEnv("/no/such/path/.env")
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestLoadDotEnv_PermissionWarn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FOO=bar\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDotEnv(path)
	if err == nil {
		t.Fatal("expected permission warning error")
	}
}

func TestLoadDotEnv_MissingEquals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("NOT_KEYVALUE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDotEnv(path)
	if err == nil {
		t.Fatal("expected parse error for line missing '='")
	}
}

func TestLoadDotEnv_EmptyKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("=value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDotEnv(path)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}
