package luacfg

import "testing"

func TestToolDefs_Gates(t *testing.T) {
	defs := ToolDefs(ToolGates{Bash: true, Edit: true}, nil)
	want := map[string]bool{"bash": false, "edit_file": false}
	for _, d := range defs {
		if _, ok := want[d.Name]; ok {
			want[d.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("%s def missing when gate enabled", name)
		}
	}
	if defs2 := ToolDefs(ToolGates{}, nil); len(defs2) != 0 {
		t.Fatalf("no gates should yield no defs, got %d", len(defs2))
	}
}
