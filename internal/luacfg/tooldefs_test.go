package luacfg

import "testing"

func TestToolDefs_ReadGate(t *testing.T) {
	defs := ToolDefs(ToolGates{Read: true}, nil)
	found := false
	for _, d := range defs {
		if d.Name == "read" {
			found = true
		}
	}
	if !found {
		t.Fatal("read tool missing when gate enabled")
	}
	if defs2 := ToolDefs(ToolGates{}, nil); len(defs2) != 0 {
		t.Fatalf("no gates should yield no defs, got %d", len(defs2))
	}
}

func TestToolDefs_ListFilesGate(t *testing.T) {
	defs := ToolDefs(ToolGates{ListFiles: true}, nil)
	if len(defs) != 1 || defs[0].Name != "list_files" {
		t.Fatalf("list_files gate should yield exactly the list_files def, got %+v", defs)
	}
}
