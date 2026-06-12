package patchapp

import "testing"

func TestDispatchSlash_NoSlashPrefix_NoMatch(t *testing.T) {
	a := &App{}
	handled := false
	a.RegisterSlash(SlashCommand{Name: "x", Handler: func(string) { handled = true }})
	a.dispatchSlash("hello world") // returns early before any Refresh
	if handled {
		t.Errorf("non-slash input should not be handled")
	}
}

func TestDispatchSlash_RoutesByName(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	got := ""
	a.RegisterSlash(SlashCommand{Name: "say", Handler: func(args string) { got = args }})
	a.dispatchSlash("/say hi there")
	if got != "hi there" {
		t.Errorf("args = %q, want %q", got, "hi there")
	}
}

func TestDispatchSlash_AliasesWork(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	hits := 0
	a.RegisterSlash(SlashCommand{
		Name: "exit", Aliases: []string{"quit", "q"},
		Handler: func(string) { hits++ },
	})
	a.dispatchSlash("/quit")
	a.dispatchSlash("/q")
	a.dispatchSlash("/exit")
	if hits != 3 {
		t.Errorf("aliases not all routed: hits=%d", hits)
	}
}

func TestDispatchSlash_CaseInsensitive(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	hit := false
	a.RegisterSlash(SlashCommand{Name: "Foo", Handler: func(string) { hit = true }})
	a.dispatchSlash("/FOO")
	if !hit {
		t.Errorf("case-insensitive lookup failed")
	}
}
