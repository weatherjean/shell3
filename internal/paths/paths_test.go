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
	if g.Data != "/home/user/.shell3/data" {
		t.Fatalf("Data: got %q", g.Data)
	}
	if g.DB != "/home/user/.shell3/data/shell3.db" {
		t.Fatalf("DB: got %q", g.DB)
	}
	if g.LogFile != "/home/user/.shell3/shell3.log" {
		t.Fatalf("LogFile: got %q", g.LogFile)
	}
}

func TestLocal(t *testing.T) {
	l := paths.NewLocal("/work/project")
	if l.Root != "/work/project/.shell3" {
		t.Fatalf("Root: got %q", l.Root)
	}
	if l.Ref != "/work/project/.shell3/.ref" {
		t.Fatalf("Ref: got %q", l.Ref)
	}
	if l.BGJobs != "/work/project/.shell3/bg.json" {
		t.Fatalf("BGJobs: got %q", l.BGJobs)
	}
}

func TestBGLog(t *testing.T) {
	if got := paths.BGLogDir(); got != "/tmp/shell3/runs" {
		t.Fatalf("BGLogDir: got %q", got)
	}
	if got := paths.BGLogPath("bg_abc123"); got != "/tmp/shell3/runs/bg_abc123.log" {
		t.Fatalf("BGLogPath: got %q", got)
	}
}
