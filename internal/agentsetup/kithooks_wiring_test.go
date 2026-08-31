package agentsetup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// Declaring command:/event: proves the RULES; going through BuildParts proves
// the WIRING — that the blocks were parsed, installed onto the LoadedConfig,
// and are actually reachable. Without this a kit could parse cleanly and
// declare hooks that govern nothing.
const hookWiringKit = `#---
# shell3:
#   models:
#     m:
#       base_url: http://x/v1
#       api_key: env:K
#       model: m
#---

#---
# agent: main
# model: m
#---
main_prompt() { cat <<'EOF'
you are the agent
EOF
}

#---
# command: standup
# description: report the arg back
#---
cmd_standup() {
  printf 'standup:%s' "$ARG"
}

#---
# event: [main]
# on: [turn_done]
#---
main_event() {
  cat > "$SHELL3_TEST_EVENTLOG"
}
`

func hookWiringParts(t *testing.T) (*agentsetup.Parts, func()) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(hookWiringKit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("K=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: dir, CWD: dir, HomeDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	return parts, cleanup
}

func TestKitCommandIsReachableThroughParts(t *testing.T) {
	parts, cleanup := hookWiringParts(t)
	defer cleanup()
	lc := parts.LoadedConfig()

	if !lc.HasCommand("standup") {
		t.Fatal("a kit declaring command: installed no command hook")
	}
	out, err := lc.RunCommand(context.Background(), "standup", "week")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if out != "standup:week" {
		t.Errorf("out = %q, want standup:week", out)
	}
	if k := parts.Kit(); k.Commands["standup"].Desc != "report the arg back" {
		t.Errorf("kit command desc = %q", k.Commands["standup"].Desc)
	}
}

func TestKitEventSubscriberFiresThroughSessionConfig(t *testing.T) {
	parts, cleanup := hookWiringParts(t)
	defer cleanup()

	log := filepath.Join(t.TempDir(), "events.json")
	t.Setenv("SHELL3_TEST_EVENTLOG", log)

	cfg, err := parts.SessionConfig(agentsetup.SessionOptions{Agent: "main"})
	if err != nil {
		t.Fatalf("SessionConfig: %v", err)
	}
	if cfg.OnEvent == nil {
		t.Fatal("a kit declaring event: wired no OnEvent observer")
	}

	cfg.OnEvent(chat.Event{Kind: chat.EventAssistantToken, Text: "x"})
	cfg.OnEvent(chat.Event{Kind: chat.EventTurnDone, SessionID: "sess-1"})

	deadline := time.Now().Add(5 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(log); err == nil && len(b) > 0 {
			body = b
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(body) == 0 {
		t.Fatal("the event subscriber never ran")
	}
	if !strings.Contains(string(body), `"turn_done"`) || !strings.Contains(string(body), `"sess-1"`) {
		t.Errorf("payload = %s", body)
	}
	if strings.Contains(string(body), "assistant_token") {
		t.Errorf("an unsubscribed kind reached the hook: %s", body)
	}
}
