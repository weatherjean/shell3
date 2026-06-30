package luacfg

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/bashsafety"
)

// minimalLua is a minimal shell3.lua preamble that satisfies Load's agent
// requirement; tests append their own shell3.bash_safety{} block after it.
const minimalLua = `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`

func TestBashSafety_Parsed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`
shell3.bash_safety{
  enabled   = true,
  deny      = { [[rm\s+-rf]], "git push" },
  hard_deny = { "mkfs" },
}
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer c.Close()
	if !c.HasBashSafety() {
		t.Fatal("HasBashSafety() = false, want true")
	}
	p := c.BashSafety()
	if !p.Enabled || len(p.Deny) != 2 || len(p.HardDeny) != 1 {
		t.Fatalf("policy = %+v, want enabled with 2 deny / 1 hard_deny", p)
	}
	// Patterns are compiled regexes.
	if !p.Deny[0].MatchString("rm   -rf x") {
		t.Error("deny regex should match `rm` with multiple spaces")
	}
	// ask_timeout unset ⇒ the default.
	if p.AskTimeout != bashsafety.DefaultAskTimeout {
		t.Errorf("AskTimeout = %v, want default %v", p.AskTimeout, bashsafety.DefaultAskTimeout)
	}
}

func TestBashSafety_BadRegexIsLoadError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`shell3.bash_safety{ enabled = true, deny = { "rm[" } }`)
	if _, err := Load(dir+"/shell3.lua", dir); err == nil {
		t.Fatal("expected a load error for an invalid deny regex")
	}
}

func TestBashSafety_DeprecatedKeysIgnored(t *testing.T) {
	dir := t.TempDir()
	// allow / read_baseline are from the old model — accepted but ignored so old
	// configs still load.
	writeFile(t, dir, "shell3.lua", minimalLua+`
shell3.bash_safety{ enabled = true, allow = { "ls*" }, read_baseline = false, deny = { "git push" } }
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("deprecated keys should be accepted (ignored), got: %v", err)
	}
	defer c.Close()
	if p := c.BashSafety(); len(p.Deny) != 1 {
		t.Fatalf("deny should still parse alongside deprecated keys: %+v", p)
	}
}

func TestBashSafety_WarnsOnSilentDowngrade(t *testing.T) {
	load := func(t *testing.T, body string) *LoadedConfig {
		t.Helper()
		dir := t.TempDir()
		writeFile(t, dir, "shell3.lua", minimalLua+body)
		c, err := Load(dir+"/shell3.lua", dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return c
	}
	has := func(ws []string, substr string) bool {
		for _, w := range ws {
			if strings.Contains(w, substr) {
				return true
			}
		}
		return false
	}

	// A removed allow/read_baseline key must warn: it is ignored, so a user who
	// relied on the old allowlist would otherwise silently run unguarded.
	c := load(t, `shell3.bash_safety{ enabled = true, allow = { "ls*" } }`)
	defer c.Close()
	if !has(c.Warnings(), "allow") {
		t.Errorf("expected an 'allow'-ignored warning, got %v", c.Warnings())
	}

	// An enabled gate with no patterns gates nothing — warn.
	c2 := load(t, `shell3.bash_safety{ enabled = true }`)
	defer c2.Close()
	if !has(c2.Warnings(), "matches nothing") {
		t.Errorf("expected an empty-gate warning, got %v", c2.Warnings())
	}

	// A normal denylist config is clean — no warnings.
	c3 := load(t, `shell3.bash_safety{ enabled = true, deny = { "rm" } }`)
	defer c3.Close()
	if len(c3.Warnings()) != 0 {
		t.Errorf("a valid deny config should produce no warnings, got %v", c3.Warnings())
	}
}

func TestBashSafety_AskTimeout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`
shell3.bash_safety{ enabled = true, deny = { "rm" }, ask_timeout = 30 }
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer c.Close()
	if got := c.BashSafety().AskTimeout; got != 30*time.Second {
		t.Errorf("AskTimeout = %v, want 30s", got)
	}
}

func TestBashSafety_AskTimeoutZeroWaitsForever(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`
shell3.bash_safety{ enabled = true, deny = { "rm" }, ask_timeout = 0 }
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer c.Close()
	if got := c.BashSafety().AskTimeout; got != 0 {
		t.Errorf("AskTimeout = %v, want 0 (an explicit 0 overrides the default)", got)
	}
}

func TestBashSafety_RejectsWrongTypedDeny(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`shell3.bash_safety{ enabled = true, deny = "rm" }`)
	if _, err := Load(dir+"/shell3.lua", dir); err == nil {
		t.Fatal("expected error when deny is a string, not a list")
	}
}

func TestBashSafety_Absent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`-- no bash_safety`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer c.Close()
	if c.HasBashSafety() {
		t.Fatal("HasBashSafety() = true with no declaration")
	}
}

func TestBashSafety_RejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`shell3.bash_safety{ bogus = 1 }`)
	if _, err := Load(dir+"/shell3.lua", dir); err == nil {
		t.Fatal("expected error on unknown key")
	}
}
