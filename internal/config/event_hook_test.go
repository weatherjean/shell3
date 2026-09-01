package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
)

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
	writeTree(t, dir, map[string]string{kit.FileName: src})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.SetKitHooks(filepath.Join(dir, kit.FileName), "main",
		KitHooks{Events: map[string]EventSub{"main": {Func: "main_event", On: on}}})
	t.Setenv("SEEN", out)
	return c, out
}

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

func TestRunEventSkipsUnsubscribedKind(t *testing.T) {
	c, out := evCfg(t, []string{"turn_done"}, `cat > "$SEEN"`)
	c.RunEvent(context.Background(), "main", "assistant_token", []byte(`{"event":"assistant_token"}`))

	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("hook ran for an unsubscribed kind (stat err = %v)", err)
	}
}

func TestRunEventUngovernedAgentIsNoop(t *testing.T) {
	c, out := evCfg(t, []string{"turn_done"}, `cat > "$SEEN"`)
	c.RunEvent(context.Background(), "explorer", "turn_done", []byte(`{}`))

	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("hook ran for an agent it does not name (stat err = %v)", err)
	}
}

func TestRunEventFailureIsReportedNotFatal(t *testing.T) {
	c, _ := evCfg(t, []string{"error"}, `echo boom >&2; exit 3`)
	err := c.RunEvent(context.Background(), "main", "error", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the stderr reported", err)
	}
}

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
