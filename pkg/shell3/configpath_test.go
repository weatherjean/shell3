package shell3_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// writeCfg writes body as dir/shell3.lua and returns its path.
func writeCfg(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const baseCfg = `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
local explorer = shell3.subagent({ name="explorer", model="main", description="d", prompt="p", tools={} })
shell3.agent({ name="code", model="main", prompt="hi", tools={ subagents={explorer} } })
`

func TestConfigPath_ExplicitSpec(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg)
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
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

	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigPath: "", WorkDir: dir})
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
