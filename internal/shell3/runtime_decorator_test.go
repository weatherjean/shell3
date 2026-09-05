//go:build unix

package shell3

import (
	"context"
	"testing"
)

func dummyTool(name string) HostTool {
	return HostTool{
		Name:       name,
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:    func(ctx context.Context, argsJSON string) (string, error) { return "ok", nil },
	}
}

func hasTool(s *Session, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, td := range s.cfg.Profile.Tools {
		if td.Name == name {
			return true
		}
	}
	return false
}

func TestSessionDecorator_AppliesToNewSessions(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	rt.SetSessionDecorator(func(s *Session) {
		_ = s.RegisterHostTool(dummyTool("image_generate"))
	})
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTool(s, "image_generate") {
		t.Fatal("new session missing decorated tool")
	}
	again, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	again.mu.Lock()
	for _, td := range again.cfg.Profile.Tools {
		if td.Name == "image_generate" {
			n++
		}
	}
	again.mu.Unlock()
	if n != 1 {
		t.Fatalf("tool registered %d times after re-request, want 1", n)
	}
}

func TestSessionDecorator_AppliesToExistingSessions(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	rt.SetSessionDecorator(func(s *Session) {
		_ = s.RegisterHostTool(dummyTool("image_generate"))
	})
	if !hasTool(s, "image_generate") {
		t.Fatal("existing session missing decorated tool")
	}
}

func TestSessionDecorator_AppliesToSubagentChildren(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	rt.SetSessionDecorator(func(s *Session) {
		_ = s.RegisterHostTool(dummyTool("image_generate"))
	})
	child, err := rt.Session(SessionOpts{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTool(child, "image_generate") {
		t.Fatal("headless child session missing decorated tool")
	}
	if !child.Headless() {
		t.Fatal("child session should report Headless")
	}
}
