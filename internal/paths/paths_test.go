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
	if g.Credentials != "/home/user/.shell3/credentials.shell3" {
		t.Fatalf("Credentials: got %q", g.Credentials)
	}
	if g.Secrets != "/home/user/.shell3/secrets.shell3" {
		t.Fatalf("Secrets: got %q", g.Secrets)
	}
	if g.Projects != "/home/user/.shell3/projects" {
		t.Fatalf("Projects: got %q", g.Projects)
	}
	if g.LogFile != "/home/user/.shell3/shell3.log" {
		t.Fatalf("LogFile: got %q", g.LogFile)
	}
}

func TestProject(t *testing.T) {
	g := paths.NewGlobal("/home/user")
	p := paths.NewProject(g, "abc-123")
	if p.Dir != "/home/user/.shell3/projects/abc-123" {
		t.Fatalf("Dir: got %q", p.Dir)
	}
	if p.DB != "/home/user/.shell3/projects/abc-123/shell3.db" {
		t.Fatalf("DB: got %q", p.DB)
	}
	if p.Meta != "/home/user/.shell3/projects/abc-123/meta.json" {
		t.Fatalf("Meta: got %q", p.Meta)
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
	if l.Personas != "/work/project/.shell3/personas" {
		t.Fatalf("Personas: got %q", l.Personas)
	}
}
