package luacfg

import "testing"

// TestWelcome_Parse verifies shell3.welcome stores a custom card string verbatim
// (including embedded ANSI escapes), and that a later call replaces an earlier.
func TestWelcome_Parse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.welcome("first")
shell3.welcome("\27[31m✦ mine ✦\27[0m")
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if c.Welcome != "\x1b[31m✦ mine ✦\x1b[0m" {
		t.Fatalf("Welcome = %q, want the last call's value verbatim (with ANSI)", c.Welcome)
	}
}

// TestWelcome_FromCommandOutput documents (and guards) that the welcome body can
// be built from a shell command's output — the config Lua VM exposes io.popen
// (via /bin/sh -c), so a dynamic card (cwd, git branch, figlet art…) needs no
// dedicated welcome_cmd option. If the sandbox is ever tightened, this breaks.
func TestWelcome_FromCommandOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.welcome(io.popen("echo hello-from-cmd"):read("*a"))
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !contains(c.Welcome, "hello-from-cmd") {
		t.Fatalf("welcome should carry the command's stdout, got %q", c.Welcome)
	}
}

// TestWelcome_BadValue verifies a non-string argument is a hard error.
func TestWelcome_BadValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.welcome({ nope = true })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	if _, err := Load(dir+"/shell3.lua", dir); err == nil {
		t.Fatal("expected error for non-string welcome argument, got nil")
	}
}
