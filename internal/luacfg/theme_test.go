package luacfg

import "testing"

// TestTheme_Parse verifies shell3.theme validates the hex FORMAT and stores every
// well-formed token→hex pair. A malformed hex value is skipped with a non-fatal
// load warning (a typo shouldn't fail the whole config). Token-NAME validation is
// intentionally not done here — the TUI owns the palette vocabulary and filters
// unknown names when it applies them — so a well-formed but unrecognized token
// passes straight through.
func TestTheme_Parse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.theme({
  primary  = "#EAB308",
  fg       = "#E5E7EB",
  bogus    = "#123456",
  green    = "not-a-hex",
})
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if c.Theme["primary"] != "#EAB308" {
		t.Errorf("primary = %q, want #EAB308", c.Theme["primary"])
	}
	if c.Theme["fg"] != "#E5E7EB" {
		t.Errorf("fg = %q, want #E5E7EB", c.Theme["fg"])
	}
	// A well-formed but unknown token passes through unchanged — the front-end,
	// not luacfg, decides which names are meaningful.
	if c.Theme["bogus"] != "#123456" {
		t.Errorf("well-formed unknown token 'bogus' should pass through, got %q", c.Theme["bogus"])
	}
	if _, ok := c.Theme["green"]; ok {
		t.Error("malformed hex for 'green' should be skipped, not stored")
	}

	warns := c.Warnings()
	if !anyContains(warns, "green") || !anyContains(warns, "hex color") {
		t.Errorf("expected a malformed-hex warning, got: %v", warns)
	}
}

// TestTheme_BadValue verifies a non-string value raises a Lua error (a type
// mistake, not a typo).
func TestTheme_BadValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.theme({ primary = 42 })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	if _, err := Load(dir + "/shell3.lua"); err == nil {
		t.Fatal("expected error for non-string theme value, got nil")
	}
}

func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if contains(s, sub) {
			return true
		}
	}
	return false
}
