package shell3_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestConfigPath_ExplicitSpec(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	got, err := rt.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("ConfigPath = %q, want %q", got, path)
	}
}

// The real bot starts with no -c flag (ConfigPath ""); ConfigPath must resolve
// it to the global ~/.shell3/shell3.lua the runtime loaded. The cwd is NOT
// consulted — a project-local ./shell3.lua must be ignored.
func TestConfigPath_ResolvesEmptySpec(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shell3Dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(shell3Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCfg(t, shell3Dir, baseCfg) // creates ~/.shell3/shell3.lua

	dir := t.TempDir()
	writeCfg(t, dir, baseCfg) // a cwd ./shell3.lua that must be ignored

	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: "", WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	got, err := rt.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(shell3Dir, "shell3.lua"); got != want {
		t.Fatalf("ConfigPath = %q, want %q", got, want)
	}
}
