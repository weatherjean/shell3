package luacfg

import (
	"time"

	"github.com/weatherjean/shell3/internal/bashsafety"
	lua "github.com/yuin/gopher-lua"
)

var bashSafetyKeys = map[string]bool{
	"enabled": true, "allow": true, "deny": true, "ask_timeout": true,
}

// luaBashSafety binds shell3.bash_safety{enabled=, allow={}, deny={},
// ask_timeout=}: an opt-in command-approval policy applied to the bash/bash_bg
// tools BEFORE execution. Allowlisted commands run; denylisted ones hard-block;
// anything else asks a human (or is denied when no asker is attached, e.g. a
// headless subagent). ask_timeout (seconds) bounds the wait for a human;
// omitted ⇒ DefaultAskTimeout, 0 ⇒ wait forever. Config-global; a second call
// replaces the first.
func (c *LoadedConfig) luaBashSafety(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "bash_safety", bashSafetyKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	p := bashsafety.Policy{Enabled: optBool(opts, "enabled"), AskTimeout: bashsafety.DefaultAskTimeout}
	p.Allow = bashSafetyList(L, opts, "allow")
	p.Deny = bashSafetyList(L, opts, "deny")
	// ask_timeout is in seconds; an explicit value (including 0 = wait forever)
	// overrides the default.
	if opts.RawGetString("ask_timeout") != lua.LNil {
		p.AskTimeout = time.Duration(optInt(opts, "ask_timeout")) * time.Second
	}
	c.bashSafety = &p
	return 0
}

// bashSafetyList reads a glob list, raising a clear Lua error when the key is
// present but not a table — a wrong-typed allow (e.g. allow="ls") would
// otherwise silently parse to an empty list and brick a headless agent.
func bashSafetyList(L *lua.LState, opts *lua.LTable, key string) []string {
	v := opts.RawGetString(key)
	if v == lua.LNil {
		return nil
	}
	t, ok := v.(*lua.LTable)
	if !ok {
		L.RaiseError("bash_safety.%s must be a list of glob strings, got %s", key, v.Type())
	}
	return stringList(t)
}

// HasBashSafety reports whether shell3.bash_safety was declared.
func (c *LoadedConfig) HasBashSafety() bool { return c.bashSafety != nil }

// BashSafety returns the parsed policy. Zero value (disabled) when none was
// declared, so callers may use it unconditionally.
func (c *LoadedConfig) BashSafety() bashsafety.Policy {
	if c.bashSafety == nil {
		return bashsafety.Policy{}
	}
	return *c.bashSafety
}
