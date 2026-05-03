package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredStore_SetGetList(t *testing.T) {
	home := t.TempDir()
	store, err := LoadCredStore(home)
	if err != nil {
		t.Fatalf("LoadCredStore on empty home: %v", err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("want empty list, got %v", got)
	}

	if err := store.Set("openai-prod", "openai", map[string]string{
		"base_url":      "https://api.openai.com/v1",
		"api_key":       "sk-test",
		"default_model": "gpt-4o",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	store2, err := LoadCredStore(home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	adapter, fields, ok := store2.Get("openai-prod")
	if !ok {
		t.Fatal("Get: not found")
	}
	if adapter != "openai" || fields["api_key"] != "sk-test" {
		t.Fatalf("got adapter=%q api_key=%q", adapter, fields["api_key"])
	}
	if list := store2.List(); len(list) != 1 || list[0].Instance != "openai-prod" {
		t.Fatalf("List: %+v", list)
	}
}

func TestCredStore_Update(t *testing.T) {
	home := t.TempDir()
	store, _ := LoadCredStore(home)
	_ = store.Set("codex", "codex", map[string]string{
		"access_token":  "old",
		"refresh_token": "rt",
	})
	if err := store.Update("codex", func(f map[string]string) error {
		f["access_token"] = "new"
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	store2, _ := LoadCredStore(home)
	_, fields, _ := store2.Get("codex")
	if fields["access_token"] != "new" || fields["refresh_token"] != "rt" {
		t.Fatalf("Update did not persist correctly: %+v", fields)
	}
}

func TestCredStore_Delete(t *testing.T) {
	home := t.TempDir()
	store, _ := LoadCredStore(home)
	_ = store.Set("a", "openai", map[string]string{"api_key": "x"})
	_ = store.Set("b", "openai", map[string]string{"api_key": "y"})
	if err := store.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, ok := store.Get("a"); ok {
		t.Fatal("Delete left record")
	}
	if _, _, ok := store.Get("b"); !ok {
		t.Fatal("Delete clobbered other record")
	}
}

func TestCredStore_FilePathAndPerm(t *testing.T) {
	home := t.TempDir()
	store, _ := LoadCredStore(home)
	_ = store.Set("openai", "openai", map[string]string{"api_key": "k"})
	path := filepath.Join(home, ".shell3", "credentials.shell3")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("perm = %o, want 0600", mode)
	}
}
