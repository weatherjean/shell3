package luacfg

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestCheckKeys(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	tbl := L.NewTable()
	tbl.RawSetString("name", lua.LString("x"))
	tbl.RawSetString("bogus", lua.LString("y"))
	err := checkKeys(tbl, "model", map[string]bool{"name": true})
	if err == nil || err.Error() != `model: unknown key "bogus"` {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}
