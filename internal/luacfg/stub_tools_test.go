package luacfg

import "testing"

// TestStubTools_Parse verifies shell3.stub_tools loads a string→string map onto
// StubTools, and that multiple calls merge with later keys overwriting earlier.
func TestStubTools_Parse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.stub_tools({
  read_file = "Use bash: cat <path>",
  grep      = "Use bash: rg <pattern>",
})
shell3.stub_tools({
  read_file = "OVERWRITTEN",
  write_file = "Use edit_file",
})
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	stubs := c.StubNames()
	if len(stubs) != 3 {
		t.Fatalf("StubNames len = %d, want 3: %v", len(stubs), stubs)
	}
	// Later call overwrites the earlier read_file value.
	if stubs["read_file"] != "OVERWRITTEN" {
		t.Errorf("read_file = %q, want %q (later key overwrites)", stubs["read_file"], "OVERWRITTEN")
	}
	if stubs["grep"] != "Use bash: rg <pattern>" {
		t.Errorf("grep = %q", stubs["grep"])
	}
	if stubs["write_file"] != "Use edit_file" {
		t.Errorf("write_file = %q", stubs["write_file"])
	}
}

// TestStubTools_BadValue verifies a non-string value raises a Lua error.
func TestStubTools_BadValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.stub_tools({ read_file = 42 })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected error for non-string stub value, got nil")
	}
	if !contains(err.Error(), "must be a string") {
		t.Fatalf("expected 'must be a string' in error, got: %v", err)
	}
}
