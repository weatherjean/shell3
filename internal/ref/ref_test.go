package ref_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
)

func setup(t *testing.T) (homeDir, cwd string, g paths.Global, l paths.Local) {
	t.Helper()
	tmp := t.TempDir()
	homeDir = filepath.Join(tmp, "home")
	cwd = filepath.Join(tmp, "project")
	os.MkdirAll(filepath.Join(cwd, ".shell3"), 0755)
	g = paths.NewGlobal(homeDir)
	l = paths.NewLocal(cwd)
	return
}

func TestInitCreatesRefAndMeta(t *testing.T) {
	_, cwd, g, l := setup(t)
	uuid, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if uuid == "" {
		t.Fatal("empty uuid")
	}

	loaded, err := ref.Load(l)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != uuid {
		t.Fatalf("Load: got %q want %q", loaded, uuid)
	}

	p := paths.NewProject(g, uuid)
	if _, err := os.Stat(p.Meta); err != nil {
		t.Fatalf("meta.json missing: %v", err)
	}

	meta, err := ref.ReadMeta(p)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.CWD != cwd {
		t.Fatalf("meta.CWD: got %q want %q", meta.CWD, cwd)
	}
	if meta.UUID != uuid {
		t.Fatalf("meta.UUID: got %q want %q", meta.UUID, uuid)
	}
}

func TestLoadMissing(t *testing.T) {
	_, _, _, l := setup(t)
	uuid, err := ref.Load(l)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if uuid != "" {
		t.Fatalf("expected empty, got %q", uuid)
	}
}

func TestInitIdempotent(t *testing.T) {
	_, cwd, g, l := setup(t)
	uuid1, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init 1: %v", err)
	}
	uuid2, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init 2: %v", err)
	}
	if uuid1 != uuid2 {
		t.Fatalf("Init not idempotent: %q vs %q", uuid1, uuid2)
	}
}

func TestFindByCWD(t *testing.T) {
	_, cwd, g, l := setup(t)
	// Need projects dir to exist for Init to work
	os.MkdirAll(g.Projects, 0700)
	uuid, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	found, err := ref.FindByCWD(g, cwd)
	if err != nil {
		t.Fatalf("FindByCWD: %v", err)
	}
	if found != uuid {
		t.Fatalf("FindByCWD: got %q want %q", found, uuid)
	}
}

func TestFindByCWD_NotFound(t *testing.T) {
	_, _, g, _ := setup(t)
	found, err := ref.FindByCWD(g, "/nonexistent/cwd")
	if err != nil {
		t.Fatalf("FindByCWD error: %v", err)
	}
	if found != "" {
		t.Fatalf("expected empty, got %q", found)
	}
}
