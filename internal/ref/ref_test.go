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
	_ = os.MkdirAll(filepath.Join(cwd, ".shell3"), 0755)
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
	_ = os.MkdirAll(g.Projects, 0700)
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

// F-JJ: a project dir with a corrupt meta.json must surface an error rather
// than being silently treated as a non-match.
func TestFindByCWD_CorruptMeta(t *testing.T) {
	_, _, g, _ := setup(t)
	p := paths.NewProject(g, "broken-uuid")
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p.Meta, []byte("{bad json"), 0600); err != nil {
		t.Fatalf("write corrupt meta: %v", err)
	}
	found, err := ref.FindByCWD(g, "/some/cwd")
	if err == nil {
		t.Fatalf("expected error for corrupt meta, got nil (found=%q)", found)
	}
}

// F-JJ: a project dir with NO meta.json is skipped silently (not an error),
// and a valid matching meta is still found alongside the meta-less dir.
func TestFindByCWD_MissingMetaSkipped(t *testing.T) {
	_, cwd, g, l := setup(t)
	if err := os.MkdirAll(g.Projects, 0700); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	// A project dir with no meta.json at all.
	empty := paths.NewProject(g, "no-meta-uuid")
	if err := os.MkdirAll(empty.Dir, 0700); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	// A real, matching project.
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

// F-KK: if .ref is lost but meta.json still records this cwd, Init must
// recover the SAME UUID and not mint a second project dir.
func TestInitRecoversLostRef(t *testing.T) {
	_, cwd, g, l := setup(t)
	uuid1, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init 1: %v", err)
	}
	// Simulate .ref loss (keep meta.json).
	if err := os.Remove(l.Ref); err != nil {
		t.Fatalf("remove .ref: %v", err)
	}
	uuid2, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init 2: %v", err)
	}
	if uuid2 != uuid1 {
		t.Fatalf("Init did not recover: got %q want %q", uuid2, uuid1)
	}
	// Only one project dir should exist under projects/.
	entries, err := os.ReadDir(g.Projects)
	if err != nil {
		t.Fatalf("ReadDir projects: %v", err)
	}
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != 1 {
		t.Fatalf("expected 1 project dir, got %d", dirs)
	}
}

// F-II: when .ref already exists (the O_EXCL serialization point), a recovery
// Init must not mint a second project dir; it returns the existing id.
func TestInitRecoverDoesNotMintWhenRefExists(t *testing.T) {
	_, cwd, g, l := setup(t)
	uuid1, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init 1: %v", err)
	}
	// .ref still present: idempotent fast path returns it, no new dir.
	uuid2, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init 2: %v", err)
	}
	if uuid2 != uuid1 {
		t.Fatalf("Init not idempotent: %q vs %q", uuid1, uuid2)
	}
	entries, err := os.ReadDir(g.Projects)
	if err != nil {
		t.Fatalf("ReadDir projects: %v", err)
	}
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != 1 {
		t.Fatalf("expected 1 project dir, got %d", dirs)
	}
}
