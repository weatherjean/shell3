package luacfg

import (
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
  enabled = true,
  allow = { "ls*", "git status*" },
  deny  = { "rm -rf /*" },
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
	if !p.Enabled || len(p.Allow) != 2 || len(p.Deny) != 1 {
		t.Fatalf("policy = %+v, want enabled with 2 allow / 1 deny", p)
	}
	// ask_timeout unset ⇒ the default.
	if p.AskTimeout != bashsafety.DefaultAskTimeout {
		t.Errorf("AskTimeout = %v, want default %v", p.AskTimeout, bashsafety.DefaultAskTimeout)
	}
}

func TestBashSafety_AskTimeout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`
shell3.bash_safety{ enabled = true, allow = { "ls*" }, ask_timeout = 30 }
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
shell3.bash_safety{ enabled = true, allow = { "ls*" }, ask_timeout = 0 }
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

func TestBashSafety_RejectsWrongTypedAllow(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minimalLua+`shell3.bash_safety{ enabled = true, allow = "ls" }`)
	if _, err := Load(dir+"/shell3.lua", dir); err == nil {
		t.Fatal("expected error when allow is a string, not a list")
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
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected error on unknown key")
	}
}
