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

// The built-in names live in two packages that cannot reference each other:
// kit.Builtins is what an agent may write in `use:` (kit imports nothing of
// ours), and the builtins table here maps those names to schemas. If they
// drift, a name accepted by `use:` silently yields no tool — the agent is
// told it has a capability the model never receives. Nothing else catches
// that, so this test is the seam.
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

// Every name in the table must render a def, and the order must be the
// table's regardless of the order the caller passes — a tool list that
// reshuffles between turns invalidates the prompt cache.
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

// read_media was a built-in reaching a hard-coded loader; perception is now
// a tool an agent declares in its own kit, not something shipped in the
// binary's tool table.
func TestReadMediaIsNotAToolAnyMore(t *testing.T) {
	for _, b := range builtins {
		if b.Def.Name == "read_media" {
			t.Fatal("read_media was removed; perception is a declared tool now")
		}
	}
}
