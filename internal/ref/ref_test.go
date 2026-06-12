package ref_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
)

func setup(t *testing.T) (g paths.Global, l paths.Local) {
	t.Helper()
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	_ = os.MkdirAll(filepath.Join(cwd, ".shell3"), 0755)
	g = paths.NewGlobal(homeDir)
	l = paths.NewLocal(cwd)
	return
}

func TestInitCreatesRef(t *testing.T) {
	g, l := setup(t)
	id, err := ref.Init(l, g)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if id == "" {
		t.Fatal("empty uuid")
	}

	loaded, err := ref.Load(l)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != id {
		t.Fatalf("Load: got %q want %q", loaded, id)
	}
}

func TestLoadMissing(t *testing.T) {
	_, l := setup(t)
	id, err := ref.Load(l)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if id != "" {
		t.Fatalf("expected empty, got %q", id)
	}
}

func TestInitIdempotent(t *testing.T) {
	g, l := setup(t)
	id1, err := ref.Init(l, g)
	if err != nil {
		t.Fatalf("Init 1: %v", err)
	}
	id2, err := ref.Init(l, g)
	if err != nil {
		t.Fatalf("Init 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("Init not idempotent: %q vs %q", id1, id2)
	}
}

func TestInit_MintsRefWithoutProjectDir(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := ref.Init(l, g)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("empty uuid")
	}
	if _, err := os.Stat(filepath.Join(home, ".shell3", "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects/ dir should not exist, stat err=%v", err)
	}
	id2, _ := ref.Init(l, g)
	if id2 != id {
		t.Fatalf("non-idempotent: %q != %q", id2, id)
	}
}
