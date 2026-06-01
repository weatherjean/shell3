package luacfg

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

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
