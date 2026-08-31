package config

import (
	"slices"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
)

func TestToolDefs_Gates(t *testing.T) {
	defs := ToolDefs([]string{"bash", "edit"})
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
	if defs2 := ToolDefs(nil); len(defs2) != 0 {
		t.Fatalf("no gates should yield no defs, got %d", len(defs2))
	}
}

func TestBuiltinsTableMatchesKitBuiltins(t *testing.T) {
	inTable := make([]string, 0, len(builtins))
	for _, b := range builtins {
		inTable = append(inTable, b.Name)
	}
	slices.Sort(inTable)

	fromKit := slices.Clone(kit.Builtins)
	slices.Sort(fromKit)

	if !slices.Equal(inTable, fromKit) {
		t.Fatalf("built-in names drifted:\n  kit.Builtins   = %v\n  config.builtins = %v", fromKit, inTable)
	}
}

func TestToolDefsIsOrderStable(t *testing.T) {
	all := slices.Clone(kit.Builtins)
	forward := ToolDefs(all)
	if len(forward) != len(builtins) {
		t.Fatalf("every built-in should render a def: got %d, want %d", len(forward), len(builtins))
	}

	slices.Reverse(all)
	reversed := ToolDefs(all)
	for i := range forward {
		if forward[i].Name != reversed[i].Name {
			t.Fatalf("def order follows the caller's slice, not the table: %v vs %v",
				forward[i].Name, reversed[i].Name)
		}
	}
}

func TestReadMediaIsNotAToolAnyMore(t *testing.T) {
	for _, b := range builtins {
		if b.Def.Name == "read_media" {
			t.Fatal("read_media was removed; perception is a declared tool now")
		}
	}
}
