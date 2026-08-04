package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

func historyStore(t *testing.T) (*runs.Store, string) {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	id, err := st.NewSession(runs.Meta{Workdir: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "how do we renew the wildcard certificate"},
		{Role: llm.RoleAssistant, Content: "with certbot, the cron job runs monthly"},
		{Role: llm.RoleTool, Content: "certbot output noise"},
	} {
		if err := st.AppendMessage(id, m); err != nil {
			t.Fatal(err)
		}
	}
	return st, id
}

func execHistory(t *testing.T, st *runs.Store, args string) string {
	t.Helper()
	out, err := HistoryHandler{}.Execute(context.Background(), "", json.RawMessage(args), ToolConfig{Store: st})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out
}

func TestHistoryToolSearchAndRead(t *testing.T) {
	st, id := historyStore(t)

	out := execHistory(t, st, `{"query": "certificate"}`)
	if !strings.Contains(out, id) || !strings.Contains(out, "certificate") {
		t.Fatalf("search output missing hit: %q", out)
	}
	if strings.Contains(out, "noise") {
		t.Fatalf("tool output leaked into search: %q", out)
	}

	out = execHistory(t, st, `{"session": "`+id+`", "around": 1}`)
	if !strings.Contains(out, "#0 user:") || !strings.Contains(out, "#1 assistant:") {
		t.Fatalf("read output missing transcript lines: %q", out)
	}
}

func TestHistoryToolDegradesGracefully(t *testing.T) {
	st, _ := historyStore(t)

	if out := execHistory(t, st, `{}`); !strings.Contains(out, "needs a query") {
		t.Fatalf("no-args guidance missing: %q", out)
	}
	if out := execHistory(t, st, `{"query": "zzzznothing"}`); out != "no matches" {
		t.Fatalf("want no matches, got %q", out)
	}
	// Nil store (a session without persistence) answers, never errors.
	out, err := HistoryHandler{}.Execute(context.Background(), "", json.RawMessage(`{"query":"x"}`), ToolConfig{})
	if err != nil || !strings.Contains(out, "unavailable") {
		t.Fatalf("nil-store path: %q %v", out, err)
	}
}
