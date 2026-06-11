package shell3

import (
	"context"
	"errors"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
)

// TestRegisterHostTool_NameRouting pins the closure-chaining in RegisterHostTool:
// two registered tools each route to their own handler, an unknown name returns
// an ErrHostToolNotFound-wrapped error (so dispatchCustomTool can fall through),
// and both names land in the schema and custom-tool set.
func TestRegisterHostTool_NameRouting(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RegisterHostTool(HostTool{
		Name:    "alpha",
		Handler: func(ctx context.Context, argsJSON string) (string, error) { return "alpha-out", nil },
	}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	if err := s.RegisterHostTool(HostTool{
		Name:    "beta",
		Handler: func(ctx context.Context, argsJSON string) (string, error) { return "beta-out", nil },
	}); err != nil {
		t.Fatalf("register beta: %v", err)
	}

	ctx := context.Background()
	if out, err := s.cfg.HostTool(ctx, "alpha", "{}"); err != nil || out != "alpha-out" {
		t.Errorf("alpha = (%q, %v), want (\"alpha-out\", nil)", out, err)
	}
	if out, err := s.cfg.HostTool(ctx, "beta", "{}"); err != nil || out != "beta-out" {
		t.Errorf("beta = (%q, %v), want (\"beta-out\", nil)", out, err)
	}

	_, err = s.cfg.HostTool(ctx, "gamma", "{}")
	if !errors.Is(err, chat.ErrHostToolNotFound) {
		t.Errorf("gamma error = %v, want ErrHostToolNotFound", err)
	}

	for _, name := range []string{"alpha", "beta"} {
		if !s.cfg.CustomToolNames[name] {
			t.Errorf("CustomToolNames missing %q", name)
		}
		found := false
		for _, td := range s.cfg.Personality.Tools {
			if td.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Personality.Tools missing %q", name)
		}
	}
}
