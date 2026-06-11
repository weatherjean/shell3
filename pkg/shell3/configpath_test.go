package shell3_test

import (
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
// it to the actual file the runtime loaded, the same way construction did.
func TestConfigPath_ResolvesEmptySpec(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, baseCfg) // creates dir/shell3.lua
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: "", WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	got, err := rt.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "shell3.lua"); got != want {
		t.Fatalf("ConfigPath = %q, want %q", got, want)
	}
}
