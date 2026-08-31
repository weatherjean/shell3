package shell3_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestConfigDir_ExplicitSpec(t *testing.T) {
	dir := t.TempDir()
	writeBaseTree(t, dir, map[string]string{
		"agents/explorer.md": "---\ndescription: d\n---\np\n",
	})
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	got, err := rt.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("ConfigDir = %q, want %q", got, dir)
	}
}

func TestConfigDir_ResolvesEmptySpec(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shell3Dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(shell3Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBaseTree(t, shell3Dir, nil)

	dir := t.TempDir()
	writeBaseTree(t, dir, nil)

	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: "", WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	got, err := rt.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != shell3Dir {
		t.Fatalf("ConfigDir = %q, want %q", got, shell3Dir)
	}
}
