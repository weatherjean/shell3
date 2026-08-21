package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// evCfg writes a kit declaring main plus one event subscriber over the given
// kinds, and installs it the way agentsetup.LoadKit does.
func evCfg(t *testing.T, on []string, body string) (*LoadedConfig, string) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "seen.log")
	src := minWiring + `
#---
# agent: main
# model: m1
# use: [bash]
#---
main_prompt() { cat <<'EOF'
You are a test agent.
EOF
}

#---
# event: [main]
# on: [` + strings.Join(on, ", ") + `]
#---
main_event() {
` + body + `
}
`
	writeTree(t, dir, map[string]string{KitFileName: src})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.SetKitHooks(filepath.Join(dir, KitFileName), "main",
		KitHooks{Events: map[string]EventSub{"main": {Func: "main_event", On: on}}})
	t.Setenv("SEEN", out)
	return c, out
}

// A subscribed kind reaches the function with the event JSON on stdin.
func TestRunEventDeliversSubscribedKind(t *testing.T) {
	c, out := evCfg(t, []string{"turn_done"}, `cat > "$SEEN"`)
	c.RunEvent(context.Background(), "main", "turn_done", []byte(`{"event":"turn_done","x":1}`))

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	if !strings.Contains(string(b), `"turn_done"`) {
		t.Errorf("stdin = %q", b)
	}
}

// An unsubscribed kind never forks a shell. This is the whole point of the
// mandatory on: filter — assistant_token fires per streamed token.
func TestRunEventSkipsUnsubscribedKind(t *testing.T) {
	c, out := evCfg(t, []string{"turn_done"}, `cat > "$SEEN"`)
	c.RunEvent(context.Background(), "main", "assistant_token", []byte(`{"event":"assistant_token"}`))

	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("hook ran for an unsubscribed kind (stat err = %v)", err)
	}
}

// An agent with no subscriber is untouched; there is no fallback between
// agents, the same rule gate:/note: follow.
func TestRunEventUngovernedAgentIsNoop(t *testing.T) {
	c, out := evCfg(t, []string{"turn_done"}, `cat > "$SEEN"`)
	c.RunEvent(context.Background(), "explorer", "turn_done", []byte(`{}`))

	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("hook ran for an agent it does not name (stat err = %v)", err)
	}
}

// A failing subscriber is reported, never fatal: an observer cannot refuse
// anything, so there is nothing to fail closed on.
func TestRunEventFailureIsReportedNotFatal(t *testing.T) {
	c, _ := evCfg(t, []string{"error"}, `echo boom >&2; exit 3`)
	err := c.RunEvent(context.Background(), "main", "error", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the stderr reported", err)
	}
}

// SubscribedKinds is what the runtime asks before serialising an event: if
// nobody subscribes to a kind, it is never rendered to JSON at all.
func TestSubscribedKinds(t *testing.T) {
	c, _ := evCfg(t, []string{"turn_done", "error"}, `cat`)
	if !c.SubscribesTo("main", "turn_done") {
		t.Error("SubscribesTo(main, turn_done) = false")
	}
	if c.SubscribesTo("main", "assistant_token") {
		t.Error("SubscribesTo(main, assistant_token) = true, want false")
	}
	if c.SubscribesTo("explorer", "turn_done") {
		t.Error("SubscribesTo(explorer, turn_done) = true, want false")
	}
}
