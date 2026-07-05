package luacfg

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// mustKeys is checkKeys raising the failure as a Lua error — the form every
// table-parsing register.go call site wants.
func mustKeys(L *lua.LState, tbl *lua.LTable, ctx string, allowed map[string]bool) {
	if err := checkKeys(tbl, ctx, allowed); err != nil {
		L.RaiseError("%s", err.Error())
	}
}

// checkKeys fails if tbl has any string key not in allowed.
func checkKeys(tbl *lua.LTable, ctx string, allowed map[string]bool) error {
	var bad string
	tbl.ForEach(func(k, _ lua.LValue) {
		if bad != "" {
			return
		}
		if s, ok := k.(lua.LString); ok && !allowed[string(s)] {
			bad = string(s)
		}
	})
	if bad != "" {
		return fmt.Errorf("%s: unknown key %q", ctx, bad)
	}
	return nil
}
