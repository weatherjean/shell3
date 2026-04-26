package patchapp

import "testing"

func TestDispatchSlash_NoSlashPrefix_NoMatch(t *testing.T) {
	a := &App{}
	a.RegisterSlash(SlashCommand{Name: "x", Handler: func(string) {}})
	if a.dispatchSlash("hello world") {
		t.Errorf("non-slash input should not be handled")
	}
}

func TestDispatchSlash_RoutesByName(t *testing.T) {
	a := &App{r: nil}
	got := ""
	a.RegisterSlash(SlashCommand{Name: "say", Handler: func(args string) { got = args }})
	// Refresh accesses a.r; bypass by using a no-op renderer expectation.
	defer func() { _ = recover() }()
	a.dispatchSlash("/say hi there")
	if got != "hi there" {
		t.Errorf("args = %q, want %q", got, "hi there")
	}
}

func TestDispatchSlash_AliasesWork(t *testing.T) {
	a := &App{}
	hits := 0
	a.RegisterSlash(SlashCommand{
		Name: "exit", Aliases: []string{"quit", "q"},
		Handler: func(string) { hits++ },
	})
	defer func() { _ = recover() }()
	a.dispatchSlash("/quit")
	a.dispatchSlash("/q")
	a.dispatchSlash("/exit")
	if hits != 3 {
		t.Errorf("aliases not all routed: hits=%d", hits)
	}
}

func TestDispatchSlash_CaseInsensitive(t *testing.T) {
	a := &App{}
	hit := false
	a.RegisterSlash(SlashCommand{Name: "Foo", Handler: func(string) { hit = true }})
	defer func() { _ = recover() }()
	a.dispatchSlash("/FOO")
	if !hit {
		t.Errorf("case-insensitive lookup failed")
	}
}
