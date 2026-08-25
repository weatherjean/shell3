package agentsetup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/config"
)

// A kit-declared `gate:` must reach the tool-call gate execution path
// reaches. Testing the shell function directly proves the RULES; only going
// through RunToolCall proves the WIRING — that the declaration was parsed,
// installed onto the LoadedConfig, and is actually consulted before a tool
// runs. Without this, a gate could parse cleanly, pass every rule test, and
// govern nothing.
const gateKit = `#---
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
# agent: helper
# description: a second agent, governed by the same gate
# model: m
#---
helper_prompt() { cat <<'EOF'
you are the helper
EOF
}

#---
# gate: [main, helper]
#---
shared_gate() {
  in=$(cat)
  case "$in" in
    *forbidden*) printf '{"block":true,"reason":"that one is refused"}' ;;
    *) printf '{}' ;;
  esac
}

#---
# note: main
#---
main_note() {
  in=$(cat)
  printf '%s' "$in" | sed 's/$/ [seen]/' | head -c 0
  printf '{"output":"rewritten by the note"}'
}
`

func kitGateParts(t *testing.T) (*agentsetup.Parts, func()) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(gateKit), 0o644); err != nil {
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

func TestKitGateGovernsToolCalls(t *testing.T) {
	parts, cleanup := kitGateParts(t)
	defer cleanup()
	lc := parts.LoadedConfig()

	if !lc.HasToolCall() {
		t.Fatal("a kit declaring gate: installed no tool-call hook")
	}

	// Both named agents are governed by the one function: a subagent left
	// ungated is a way around every rule the main agent has.
	for _, agent := range []string{"main", "helper"} {
		v := lc.RunToolCall(context.Background(), agent, "bash", "echo forbidden", "{}", true)
		if v.Action != config.ActionBlock {
			t.Errorf("agent %q: forbidden command was not blocked", agent)
		}
		if v = lc.RunToolCall(context.Background(), agent, "bash", "echo fine", "{}", true); v.Action != config.ActionRun {
			t.Errorf("agent %q: ordinary command = %v, want run", agent, v.Action)
		}
	}
}

func TestKitNoteRewritesToolResults(t *testing.T) {
	parts, cleanup := kitGateParts(t)
	defer cleanup()
	lc := parts.LoadedConfig()

	if !lc.HasToolResult() {
		t.Fatal("a kit declaring note: installed no tool-result hook")
	}
	got := lc.RunToolResult(context.Background(), "main", "bash", "{}", "original")
	if !strings.Contains(got, "rewritten by the note") {
		t.Errorf("tool result = %q, want the note's rewrite", got)
	}
}
