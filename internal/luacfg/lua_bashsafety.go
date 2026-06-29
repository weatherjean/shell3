package luacfg

import (
	"regexp"
	"time"

	"github.com/weatherjean/shell3/internal/bashsafety"
	lua "github.com/yuin/gopher-lua"
)

var bashSafetyKeys = map[string]bool{
	"enabled": true, "deny": true, "hard_deny": true, "ask_timeout": true,
	// Deprecated keys from the old allow/baseline model. Accepted but ignored so
	// existing configs still load — the gate is now deny/hard_deny regex only.
	"allow": true, "read_baseline": true,
}

// luaBashSafety binds shell3.bash_safety{enabled=, deny={}, hard_deny={},
// ask_timeout=}: a regex command gate applied to the bash/bash_bg tools BEFORE
// execution. A command matching a hard_deny regex is hard-blocked (never
// prompted); one matching a deny regex prompts a human (allow/deny — denied when
// headless); anything matching neither runs. Patterns are full-command regexes,
// compiled here so a bad pattern is a load error. ask_timeout (seconds) bounds
// the wait for a human; omitted ⇒ DefaultAskTimeout, 0 ⇒ wait forever. The legacy
// allow/read_baseline keys are accepted but ignored. Config-global; a second call
// replaces the first.
func (c *LoadedConfig) luaBashSafety(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "bash_safety", bashSafetyKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	p := bashsafety.Policy{Enabled: optBool(opts, "enabled"), AskTimeout: bashsafety.DefaultAskTimeout}
	p.Deny = compileDenyList(L, opts, "deny")
	p.HardDeny = compileDenyList(L, opts, "hard_deny")
	// ask_timeout is in seconds; an explicit value (including 0 = wait forever)
	// overrides the default.
	if opts.RawGetString("ask_timeout") != lua.LNil {
		p.AskTimeout = time.Duration(optInt(opts, "ask_timeout")) * time.Second
	}
	// Surface the silent footguns of the allowlist→denylist migration: a config
	// that still sets the removed allow/read_baseline keys, or an enabled gate
	// with no patterns at all, gates nothing. These are warnings, not errors, so
	// existing configs still load — but a user who relied on the old allowlist
	// must not believe they are still protected.
	if opts.RawGetString("allow") != lua.LNil || opts.RawGetString("read_baseline") != lua.LNil {
		c.warn("bash_safety: the 'allow'/'read_baseline' keys were removed (the gate is now a deny/hard_deny denylist) and are ignored — move any patterns into 'deny'/'hard_deny', or the shell runs unguarded")
	}
	if p.Enabled && len(p.Deny) == 0 && len(p.HardDeny) == 0 {
		c.warn("bash_safety is enabled but both 'deny' and 'hard_deny' are empty — the gate matches nothing, so every command runs")
	}
	c.bashSafety = &p
	return 0
}

// compileDenyList reads a regex string list under key and compiles each pattern,
// raising a clear Lua load error on an invalid regex — so a typo surfaces at
// config load, not at the first command that would have matched.
func compileDenyList(L *lua.LState, opts *lua.LTable, key string) []*regexp.Regexp {
	pats := bashSafetyList(L, opts, key)
	if len(pats) == 0 {
		return nil
	}
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, pat := range pats {
		re, err := bashsafety.CompileRule(pat)
		if err != nil {
			L.RaiseError("bash_safety.%s: invalid regex %q: %v", key, pat, err)
		}
		out = append(out, re)
	}
	return out
}

// bashSafetyList reads a string list, raising a clear Lua error when the key is
// present but not a table — a wrong-typed deny (e.g. deny="rm") would otherwise
// silently parse to an empty list and disable the gate.
func bashSafetyList(L *lua.LState, opts *lua.LTable, key string) []string {
	v := opts.RawGetString(key)
	if v == lua.LNil {
		return nil
	}
	t, ok := v.(*lua.LTable)
	if !ok {
		L.RaiseError("bash_safety.%s must be a list of regex strings, got %s", key, v.Type())
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
