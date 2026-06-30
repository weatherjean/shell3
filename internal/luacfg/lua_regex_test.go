// internal/luacfg/lua_regex_test.go
package luacfg

import (
	lua "github.com/yuin/gopher-lua"
	"testing"
)

func runRegexLua(t *testing.T, src string) lua.LValue {
	t.Helper()
	L := lua.NewState()
	defer L.Close()
	c := &LoadedConfig{L: L}
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	registerRegex(L)
	L.SetField(tbl, "regex", L.NewFunction(c.luaRegex))
	if err := L.DoString(src); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	return L.Get(-1)
}

func TestRegexMatchTrue(t *testing.T) {
	if v := runRegexLua(t, `return shell3.regex([[\d+]]):match("ab12")`); v != lua.LTrue {
		t.Fatalf("want true, got %v", v)
	}
}

func TestRegexMatchFalse(t *testing.T) {
	if v := runRegexLua(t, `return shell3.regex([[\d+]]):match("abc")`); v != lua.LFalse {
		t.Fatalf("want false, got %v", v)
	}
}

func TestRegexBadPatternIsLoadError(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	c := &LoadedConfig{L: L}
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	registerRegex(L)
	L.SetField(tbl, "regex", L.NewFunction(c.luaRegex))
	if err := L.DoString(`return shell3.regex("(")`); err == nil {
		t.Fatal("expected a load error for an invalid pattern")
	}
}

func TestRegexNoDotall(t *testing.T) {
	// "." must NOT match a newline — there is no implicit (?s) DOTALL flag.
	if v := runRegexLua(t, "return shell3.regex(\".\"):match(\"\\n\")"); v != lua.LFalse {
		t.Fatal("want false: dot must not match newline without explicit (?s)")
	}
}
