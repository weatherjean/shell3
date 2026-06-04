package luacfg

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestLuaToGoPureArray(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	tbl := L.NewTable()
	tbl.Append(lua.LNumber(1))
	tbl.Append(lua.LNumber(2))
	got := luaToGo(tbl)
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("pure array: want []any, got %T (%v)", got, got)
	}
	if len(arr) != 2 || arr[0] != float64(1) || arr[1] != float64(2) {
		t.Fatalf("pure array values: %+v", arr)
	}
}

func TestLuaToGoPureMap(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	tbl := L.NewTable()
	tbl.RawSetString("foo", lua.LString("x"))
	got := luaToGo(tbl)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("pure map: want map, got %T (%v)", got, got)
	}
	if m["foo"] != "x" {
		t.Fatalf("pure map values: %+v", m)
	}
}

func TestLuaToGoMixedTableKeepsStringKeys(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	tbl := L.NewTable()
	tbl.Append(lua.LNumber(1))
	tbl.Append(lua.LNumber(2))
	tbl.RawSetString("foo", lua.LString("x"))
	got := luaToGo(tbl)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("mixed table: want map preserving all entries, got %T (%v)", got, got)
	}
	if m["foo"] != "x" {
		t.Fatalf("mixed table dropped string key: %+v", m)
	}
	if m["1"] != float64(1) || m["2"] != float64(2) {
		t.Fatalf("mixed table dropped sequence entries: %+v", m)
	}
}
