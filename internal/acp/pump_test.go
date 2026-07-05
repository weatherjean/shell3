package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// buildPumpEnv wires a shell3 Runtime behind ACP from a caller-supplied lua
// config (which must already embed a fake-LLM base_url). It mirrors
// newTestEnvFull but takes the full lua verbatim so the pump tests can declare
// bespoke agents/subagents and point at content-routed LLM servers.
func buildPumpEnv(t *testing.T, luaContent string) *env {
	t.Helper()

	cfgDir := t.TempDir()
	luaPath := filepath.Join(cfgDir, "shell3.lua")
	if err := os.WriteFile(luaPath, []byte(luaContent), 0o644); err != nil {
		t.Fatalf("buildPumpEnv: write shell3.lua: %v", err)
	}

	workDir := t.TempDir()
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigPath: luaPath, WorkDir: workDir})
	if err != nil {
		t.Fatalf("buildPumpEnv: NewRuntime: %v", err)
	}

	return startEnv(t, rt, nil, workDir)
}

// waitForUpdateText polls the recorder up to timeout for the concatenation of
// all agent_message_chunk texts (in receipt order) to contain want. The fake LLM
// streams plain text as two half-chunks and forward maps each Token to a separate
// chunk, so a turn's output spans multiple chunks — hence the concatenation
// rather than a per-chunk match. Bounded (no bare sleep as sync): a false return
// is turned into a t.Fatal with a diagnostic by the caller.
func waitForUpdateText(rec *recorder, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var sb strings.Builder
		for _, n := range rec.snapshotUpdates() {
			c := n.Update.AgentMessageChunk
			if c != nil && c.Content.Text != nil {
				sb.WriteString(c.Content.Text.Text)
			}
		}
		if strings.Contains(sb.String(), want) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// ─── content-routed fake LLM ────────────────────────────────────────────────

// route matches an inbound /chat/completions request by a substring of its raw
// body and serves a response script. When gate is non-nil the handler blocks on
// it (or the request context) before streaming — used to hold a subagent's turn
// open until the test releases it, so completion ordering is deterministic.
type route struct {
	match  string
	script string
	gate   chan struct{}
}

// newRoutedLLM serves an OpenAI-compatible SSE endpoint that picks a response by
// matching the request body against routes in order (first match wins), falling
// back to fallback when none match. Routing by body content (not a global pop
// counter) is what makes the concurrent parent+child request interleaving
// deterministic.
func newRoutedLLM(t *testing.T, fallback string, routes ...route) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bs := string(body)

		script := fallback
		var gate chan struct{}
		for _, rt := range routes {
			if strings.Contains(bs, rt.match) {
				script = rt.script
				gate = rt.gate
				break
			}
		}
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, _ := w.(http.Flusher)
		streamScript(w, fl, script)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// streamScript writes one response script as OpenAI SSE chunks: a "tool:" script
// emits a single tool_calls delta + tool_calls finish; anything else is streamed
// as two content deltas + a stop finish. Shape matches internal/adapter/openai.
func streamScript(w http.ResponseWriter, fl http.Flusher, script string) {
	writeEvent := func(data string) {
		fmt.Fprintf(w, "data: %s\n\n", data)
		if fl != nil {
			fl.Flush()
		}
	}
	if strings.HasPrefix(script, "tool:") {
		rest := strings.TrimPrefix(script, "tool:")
		toolName, toolArgs := rest, "{}"
		if name, args, ok := strings.Cut(rest, ":"); ok {
			toolName, toolArgs = name, args
		}
		chunk := map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "call_0", "type": "function",
					"function": map[string]any{"name": toolName, "arguments": toolArgs},
				}}},
				"finish_reason": nil,
			}},
		}
		b, _ := json.Marshal(chunk)
		writeEvent(string(b))
		term := map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
		}
		bt, _ := json.Marshal(term)
		writeEvent(string(bt))
		writeEvent("[DONE]")
		return
	}
	mid := len(script) / 2
	for _, part := range [2]string{script[:mid], script[mid:]} {
		chunk := map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": part}, "finish_reason": nil}},
		}
		b, _ := json.Marshal(chunk)
		writeEvent(string(b))
	}
	stop := map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	}
	bs, _ := json.Marshal(stop)
	writeEvent(string(bs))
	writeEvent("[DONE]")
}

// ─── tests ──────────────────────────────────────────────────────────────────

// TestPumpForwardsBgNotice exercises the pump's Wake branch driven by a bash_bg
// completion. A bash_bg job's completion notice is injected into the session
// inbox (injectNoticeNoWake — no bus event of its own). By holding the turn open
// (the "block" script) until the notice has landed, the turn ends with a
// non-empty inbox → the runtime emits an end-of-turn Wake → the pump drains it as
// a fresh, out-of-turn turn whose model output is forwarded to the client. The
// assertion runs only AFTER the client Prompt has returned, so the forwarded
// update is provably out-of-turn.
func TestPumpForwardsBgNotice(t *testing.T) {
	releaseCh := make(chan struct{})
	llm := newFakeLLM(t, releaseCh,
		`tool:bash_bg:{"command":"true"}`, // req1: start the bg job
		"block",                           // req2: hold the turn open (waits on releaseCh)
		"bg job relayed to user",          // req3: the wake-turn's model output
	)

	lua := fmt.Sprintf(`
shell3.model("test", { base_url = %q, api_key = "test", model = "gpt-4o", context_window = 128000 })
shell3.agent({
  name = "code",
  model = "test",
  prompt = "You are a coding assistant.",
  delegation = true,
  tools = { bash = true, bash_bg = true },
})
`, llm.URL)

	e := buildPumpEnv(t, lua)
	a := e.getAgent(t)
	ctx := context.Background()

	sessID := newSession(t, e.conn)
	as := a.sessionByID(string(sessID))
	if as == nil {
		t.Fatal("session not registered after NewSession")
	}

	promptDone := make(chan struct{})
	go func() {
		_, _ = e.conn.Prompt(ctx, promptRequest(sessID, "start a bg job"))
		close(promptDone)
	}()

	// Poll until the bg completion notice has reached the inbox. The turn is held
	// open by the "block" script, so this cannot race the end-of-turn drain.
	deadline := time.Now().Add(5 * time.Second)
	for !as.sess.HasQueuedInput() {
		if time.Now().After(deadline) {
			t.Fatal("bg completion notice never reached the inbox within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Release the held turn: end-of-turn sees a non-empty inbox → Wake → pump.
	close(releaseCh)

	select {
	case <-promptDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Prompt did not return after release")
	}

	// With NO client prompt in flight, the wake-turn output must arrive out-of-turn.
	if !waitForUpdateText(e.rec, "bg job relayed to user", 5*time.Second) {
		t.Fatalf("no out-of-turn wake-turn update; got %d updates", len(e.rec.snapshotUpdates()))
	}
}

// TestPumpRunsQueuedOnWake exercises the pump's Wake branch driven by an async
// subagent completion. The parent turn spawns a subagent (task tool) and ends.
// The subagent's turn is gated so it completes only AFTER the parent Prompt has
// returned; on completion it injects its result into the parent and (parent idle)
// wakes it. The pump's drainQueued then runs the parent's wake-turn, whose output
// ("child finished summary") must arrive out-of-turn.
//
// The fake LLM is content-routed (not pop-ordered) because the parent and child
// sessions issue LLM requests concurrently and the pop order is nondeterministic.
func TestPumpRunsQueuedOnWake(t *testing.T) {
	childGate := make(chan struct{})
	llm := newRoutedLLM(t,
		// fallback = the first parent request: emit the task tool call.
		`tool:task:{"subagent_type":"helper","prompt":"delegate the thing","description":"d"}`,
		// Child subagent turn — matched by its distinctive system-prompt marker.
		// Gated so it does not finish (and inject) until the test releases it.
		route{match: "SUBAGENT_HELPER_MARKER", script: "childdone-marker", gate: childGate},
		// Parent wake-turn — its request carries the child's result via the
		// agent_done notice ("childdone-marker"). Ordered BEFORE the tool-result
		// route because the wake-turn history ALSO contains "started subagent".
		route{match: "childdone-marker", script: "child finished summary"},
		// Parent round after the task tool returns.
		route{match: "started subagent", script: "spawned"},
	)

	lua := fmt.Sprintf(`
local helper = shell3.subagent({
  name = "helper",
  description = "delegation test helper",
  model = "test",
  prompt = "SUBAGENT_HELPER_MARKER",
  tools = { bash = true },
})
shell3.model("test", { base_url = %q, api_key = "test", model = "gpt-4o", context_window = 128000 })
shell3.agent({
  name = "code",
  model = "test",
  prompt = "You are a coding assistant.",
  delegation = true,
  tools = { bash = true, subagents = { helper } },
})
`, llm.URL)

	e := buildPumpEnv(t, lua)
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	// Parent turn: model calls task (spawns child), then says "spawned"; turn ends.
	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "delegate the work"))
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want end_turn", resp.StopReason)
	}

	// Prompt has returned — no client prompt is in flight. Release the child; it
	// completes, injects its result into the parent, and (parent idle) wakes it.
	close(childGate)

	// The pump's drainQueued runs the parent's wake-turn; its output must arrive
	// as an out-of-turn agent_message_chunk.
	if !waitForUpdateText(e.rec, "child finished summary", 5*time.Second) {
		t.Fatalf("no out-of-turn wake-turn update; got %d updates", len(e.rec.snapshotUpdates()))
	}
}
