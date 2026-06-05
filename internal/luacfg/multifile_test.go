package luacfg

import (
	"os"
	"path/filepath"
	"testing"
)

// shell3.lua should be able to require() a sibling module by name, with the
// path resolved relative to the config dir rather than the process CWD.
func TestRequireSiblingModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "models.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
return { ok = true }
`)
	writeFile(t, dir, "shell3.lua", `
local m = require("models")
assert(m.ok, "module return value lost")
shell3.agent({ name="a", model="m", prompt="p", tools={} })
`)

	// Run from an unrelated CWD to prove resolution is config-relative.
	other := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)

	c, err := Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.Model("m"); !ok {
		t.Fatal("model from required module not registered")
	}
}

// A package laid out as a subdir with init.lua should resolve too (the
// dir/?/init.lua entry we prepend to package.path).
func TestRequirePackageInit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "providers"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "providers"), "init.lua",
		`shell3.model("m", { base_url="u", api_key="k", model="x" })`)
	writeFile(t, dir, "shell3.lua", `
require("providers")
shell3.agent({ name="a", model="m", prompt="p", tools={} })
`)
	c, err := Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.Model("m"); !ok {
		t.Fatal("model from package init not registered")
	}
}
