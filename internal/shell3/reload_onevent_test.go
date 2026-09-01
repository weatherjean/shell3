package shell3

import (
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
)

func TestReloadRepointsSessionOnEvent(t *testing.T) {
	var mu sync.Mutex
	oldSeen, newSeen := 0, 0

	rt := newTestRuntime(t, func() chat.Config {
		cfg := fakeCfg("ok")()
		cfg.OnEvent = func(chat.Event) {
			mu.Lock()
			oldSeen++
			mu.Unlock()
		}
		return cfg
	})
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.applyReload(fakeReloadState(rt, func() chat.Config {
		cfg := fakeCfg("ok")()
		cfg.OnEvent = func(chat.Event) {
			mu.Lock()
			newSeen++
			mu.Unlock()
		}
		return cfg
	}, func() {})); err != nil {
		t.Fatalf("applyReload: %v", err)
	}

	for ev := range s.Send(t.Context(), "hi") {
		_ = ev
	}

	mu.Lock()
	defer mu.Unlock()
	if newSeen == 0 {
		t.Errorf("the reloaded generation's observer saw no events (old saw %d) — the swap did not repoint it", oldSeen)
	}
}
