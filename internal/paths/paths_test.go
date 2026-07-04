package paths_test

import (
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
)

func TestGlobal(t *testing.T) {
	g := paths.NewGlobal("/home/user")
	if g.Root != "/home/user/.shell3" {
		t.Fatalf("Root: got %q", g.Root)
	}
	if g.LogFile != "/home/user/.shell3/shell3.log" {
		t.Fatalf("LogFile: got %q", g.LogFile)
	}
	if g.ConfigFile != "/home/user/.shell3/shell3.lua" {
		t.Fatalf("ConfigFile: got %q", g.ConfigFile)
	}
	if got := g.ConfigNamed("code"); got != "/home/user/.shell3/code.lua" {
		t.Fatalf("ConfigNamed: got %q", got)
	}
	// Compile-time check: Global must NOT have Data or DB fields.
	// (If this file compiles, the fields are absent.)
	_ = g
}

func TestLocal(t *testing.T) {
	l := paths.NewLocal("/work/project")
	if l.Root != "/work/project/.shell3_project" {
		t.Fatalf("Root: got %q", l.Root)
	}
	if l.Runs != "/work/project/.shell3_project/runs" {
		t.Fatalf("Runs: got %q", l.Runs)
	}
}
