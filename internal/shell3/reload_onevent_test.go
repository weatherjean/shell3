package shell3

import (
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
)

// A reload must repoint the session's kit event observer at the NEW
// generation. The chat session survives the swap (it is the history), so the
// observer it was created with stays bound to the old Parts — whose event
// dispatcher the reload's teardown closes. Left unswapped, an `event:`
// subscriber goes silent on the first /reload and stays silent, with nothing
// in the log to say why.
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
