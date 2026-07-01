package luacfg

import (
	"regexp"

	lua "github.com/yuin/gopher-lua"
)

const regexTypeName = "shell3.regex"

// registerRegex installs the metatable backing shell3.regex values. A regex
// value is a userdata wrapping *regexp.Regexp with a single method, :match(s).
func registerRegex(L *lua.LState) {
	mt := L.NewTypeMetatable(regexTypeName)
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"match": regexMatch,
	}))
}

// luaRegex binds shell3.regex(pattern): compile a Go RE2 pattern at config load
// (a bad pattern is a load error) and return a reusable matcher. No implicit
// flags — the user writes (?s) etc. explicitly.
func (c *LoadedConfig) luaRegex(L *lua.LState) int {
	pat := L.CheckString(1)
	re, err := regexp.Compile(pat)
	if err != nil {
		L.RaiseError("shell3.regex: invalid pattern %q: %v", pat, err)
	}
	ud := L.NewUserData()
	ud.Value = re
	L.SetMetatable(ud, L.GetTypeMetatable(regexTypeName))
	L.Push(ud)
	return 1
}

// regexMatch is the :match(s) method — reports whether the pattern is found
// anywhere in s (unanchored, matching the whole-command convention).
func regexMatch(L *lua.LState) int {
	ud := L.CheckUserData(1)
	re, ok := ud.Value.(*regexp.Regexp)
	if !ok {
		L.ArgError(1, regexTypeName+" expected")
	}
	L.Push(lua.LBool(re.MatchString(L.CheckString(2))))
	return 1
}
