package luacfg

import (
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// hasStringKey reports whether the table has any string (non-integer) key.
func hasStringKey(t *lua.LTable) bool {
	found := false
	t.ForEach(func(k, _ lua.LValue) {
		if _, ok := k.(lua.LString); ok {
			found = true
		}
	})
	return found
}

func optStr(t *lua.LTable, k string) string {
	if s, ok := t.RawGetString(k).(lua.LString); ok {
		return string(s)
	}
	return ""
}
func optInt(t *lua.LTable, k string) int {
	if n, ok := t.RawGetString(k).(lua.LNumber); ok {
		return int(n)
	}
	return 0
}
func optFloatPtr(t *lua.LTable, k string) *float64 {
	if n, ok := t.RawGetString(k).(lua.LNumber); ok {
		f := float64(n)
		return &f
	}
	return nil
}
func optBool(t *lua.LTable, k string) bool {
	return lua.LVAsBool(t.RawGetString(k))
}

// tableToMap converts a Lua table to a Go map (objects) or slice (arrays).
func tableToMap(t *lua.LTable) map[string]any {
	out := map[string]any{}
	t.ForEach(func(k, v lua.LValue) {
		if ks, ok := k.(lua.LString); ok {
			out[string(ks)] = luaToGo(v)
		}
	})
	return out
}

// stringList reads the array part of a Lua table as a []string.
func stringList(t *lua.LTable) []string {
	var out []string
	for i := 1; ; i++ {
		v := t.RawGetInt(i)
		if v == lua.LNil {
			break
		}
		// Only accept genuine strings; a non-string element (e.g. a stray number
		// in a secrets list) is ignored rather than coerced into a garbage value.
		if s, ok := v.(lua.LString); ok {
			out = append(out, string(s))
		}
	}
	return out
}

func handleNames(list *lua.LTable, sentinel string) []string {
	var out []string
	list.ForEach(func(_, v lua.LValue) {
		if ht, ok := v.(*lua.LTable); ok {
			if s, ok := ht.RawGetString(sentinel).(lua.LString); ok {
				out = append(out, string(s))
			}
		}
	})
	return out
}

func luaToGo(v lua.LValue) any {
	switch x := v.(type) {
	case lua.LString:
		return string(x)
	case lua.LNumber:
		return float64(x)
	case lua.LBool:
		return bool(x)
	case *lua.LTable:
		n := x.Len()
		// A pure sequence becomes []any; a table with any string key becomes a
		// map preserving BOTH integer- and string-keyed entries (nothing dropped).
		if n > 0 && !hasStringKey(x) {
			arr := make([]any, 0, n)
			for i := 1; i <= n; i++ {
				arr = append(arr, luaToGo(x.RawGetInt(i)))
			}
			return arr
		}
		m := tableToMap(x)
		if n > 0 {
			for i := 1; i <= n; i++ {
				m[strconv.Itoa(i)] = luaToGo(x.RawGetInt(i))
			}
		}
		return m
	default:
		return nil
	}
}
