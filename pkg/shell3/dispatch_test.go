package shell3

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// dispatchCfg builds a config whose subagent run streams the given text. Mirrors fakeCfg in runtime_test.go.
func dispatchCfg(text string) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: text}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: text}}},
			),
			ModeLabel: "code",
		}
	}
}

func TestDispatch_NotifyDeliversAndWakes(t *testing.T) {
	rt := newTestRuntime(t, dispatchCfg("job done"))
	main, err := rt.Session(SessionOpts{Name: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := main.Dispatch("explorer", "do the thing", DispatchOpts{Label: "cron:nightly", Notify: true})
	if err != nil || id == "" {
		t.Fatalf("dispatch: id=%q err=%v", id, err)
	}
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "telegram" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a Wake from the notify=true dispatch")
	}
	reply := drainText(main.RunQueued(dispatchCtx()))
	if !strings.Contains(reply, "job done") {
		t.Fatalf("expected the labeled result in the wake turn, got %q", reply)
	}
}

func TestDispatch_QuietSuccessNoWake(t *testing.T) {
	rt := newTestRuntime(t, dispatchCfg("quiet result"))
	main, _ := rt.Session(SessionOpts{Name: "telegram"})
	if _, err := main.Dispatch("explorer", "bg job", DispatchOpts{Notify: false}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-rt.Events():
		t.Fatalf("quiet success should not wake, got %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
	if main.HasQueuedInput() {
		t.Fatal("quiet success should not queue input on the main session")
	}
}

func TestDispatch_RejectedFromSubagentSession(t *testing.T) {
	rt := newTestRuntime(t, dispatchCfg("x"))
	sub, _ := rt.Session(SessionOpts{Name: "sub:a1"})
	if _, err := sub.Dispatch("explorer", "nope", DispatchOpts{}); err == nil {
		t.Fatal("dispatch from a subagent session must be rejected (depth-1)")
	}
}

func dispatchCtx() context.Context { return context.Background() }
func drainText(ch <-chan Event) string {
	var b strings.Builder
	for ev := range ch {
		if ev.Kind == Token {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}
