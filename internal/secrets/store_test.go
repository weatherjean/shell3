package secrets_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/weatherjean/shell3/internal/secrets"
)

func TestLoad_EmptyWhenNoFile(t *testing.T) {
	home := t.TempDir()
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	home := t.TempDir()
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("BRAVE_API_KEY", "abc123xyz"); err != nil {
		t.Fatal(err)
	}

	s2, err := secrets.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := s2.Get("BRAVE_API_KEY")
	if !ok || v != "abc123xyz" {
		t.Fatalf("Get: got (%q,%v), want (%q,true)", v, ok, "abc123xyz")
	}
}

func TestSet_FileIsWrapped(t *testing.T) {
	home := t.TempDir()
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("BRAVE_API_KEY", "shouldnotappear"); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(filepath.Join(home, ".shell3", "secrets.shell3"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("shouldnotappear")) {
		t.Fatal("secrets file contains plaintext secret")
	}
}

func TestRemove(t *testing.T) {
	home := t.TempDir()
	s, _ := secrets.Load(home)
	s.Set("A", "1")
	s.Set("B", "2")
	if err := s.Remove("A"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("A"); ok {
		t.Fatal("A still present after Remove")
	}
	if err := s.Remove("MISSING"); err != nil {
		t.Fatalf("Remove of missing: %v", err)
	}
}

func TestList_Sorted(t *testing.T) {
	home := t.TempDir()
	s, _ := secrets.Load(home)
	s.Set("ZED", "z")
	s.Set("ALPHA", "a")
	s.Set("MID", "m")
	got := s.List()
	want := []string{"ALPHA", "MID", "ZED"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List: got %v, want %v", got, want)
	}
}
