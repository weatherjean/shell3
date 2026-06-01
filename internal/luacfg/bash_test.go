package luacfg

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestLuaBashBinding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
r = shell3.bash("echo hello", { timeout=5 })
ok = (r.exit == 0) and (r.stdout == "hello\n")
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.L.GetGlobal("ok") != lua.LTrue {
		t.Fatalf("bash binding failed: exit/stdout mismatch")
	}
}
