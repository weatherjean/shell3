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
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// ─── fake LLM ──────────────────────────────────────────────────────────────────

// newFakeLLM builds an OpenAI-compatible SSE server for /chat/completions.
//
// Each incoming request pops the next script from the queue. Script formats:
//   - Plain text  → streamed as two content-delta chunks then [DONE].
//   - "tool:<name>:<argsJSON>" → streamed as a single tool_calls delta
//     (with full arguments in one chunk) then finish_reason "tool_calls" then [DONE].
//
// The SSE wire shape matches exactly what internal/adapter/openai/client.go
// expects:
//
//	data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{...},"finish_reason":null}]}\n\n
//	...
//	data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\n
//	data: [DONE]\n\n
//
// newFakeLLM builds an OpenAI-compatible SSE server for /chat/completions.
//
// The releaseCh parameter gates the special "block" script: the handler
// streams one content token then blocks until releaseCh is closed or the
// request context is cancelled (whichever comes first). Pass a non-nil
// channel when tests need to control when the blocking turn ends.
func newFakeLLM(t *testing.T, releaseCh chan struct{}, scripts ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	idx := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		script := ""
		if idx < len(scripts) {
			script = scripts[idx]
			idx++
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, _ := w.(http.Flusher)

		writeEvent := func(data string) {
			fmt.Fprintf(w, "data: %s\n\n", data)
			if fl != nil {
				fl.Flush()
			}
		}

		if script == "block" {
			// Stream one token so the agent observes at least one event and
			// the recorder signals "turn in flight", then block.
			chunk := map[string]any{
				"id":     "chatcmpl-test",
				"object": "chat.completion.chunk",
				"choices": []any{map[string]any{
					"index":         0,
					"delta":         map[string]any{"content": "…"},
					"finish_reason": nil,
				}},
			}
			b, _ := json.Marshal(chunk)
			writeEvent(string(b))
			var waitCh <-chan struct{}
			if releaseCh != nil {
				waitCh = releaseCh
			} else {
				// Never-released channel: block until request cancelled.
				waitCh = make(chan struct{})
			}
			select {
			case <-waitCh:
			case <-r.Context().Done():
			}
			// Write stop so the turn ends cleanly when released normally.
			stop := map[string]any{
				"id":     "chatcmpl-test",
				"object": "chat.completion.chunk",
				"choices": []any{map[string]any{
					"index": 0, "delta": map[string]any{}, "finish_reason": "stop",
				}},
			}
			bs, _ := json.Marshal(stop)
			writeEvent(string(bs))
			writeEvent("[DONE]")
			return
		}

		if strings.HasPrefix(script, "tool:") {
			// Format: "tool:<name>:<argsJSON>"
			rest := strings.TrimPrefix(script, "tool:")
			colonIdx := strings.Index(rest, ":")
			toolName, toolArgs := rest, "{}"
			if colonIdx >= 0 {
				toolName = rest[:colonIdx]
				toolArgs = rest[colonIdx+1:]
			}

			// Single chunk carrying the full tool call.
			chunk := map[string]any{
				"id":     "chatcmpl-test",
				"object": "chat.completion.chunk",
				"choices": []any{
					map[string]any{
						"index": 0,
						"delta": map[string]any{
							"tool_calls": []any{
								map[string]any{
									"index": 0,
									"id":    "call_0",
									"type":  "function",
									"function": map[string]any{
										"name":      toolName,
										"arguments": toolArgs,
									},
								},
							},
						},
						"finish_reason": nil,
					},
				},
			}
			b, _ := json.Marshal(chunk)
			writeEvent(string(b))

			// Terminating chunk with finish_reason "tool_calls".
			term := map[string]any{
				"id":     "chatcmpl-test",
				"object": "chat.completion.chunk",
				"choices": []any{
					map[string]any{
						"index":         0,
						"delta":         map[string]any{},
						"finish_reason": "tool_calls",
					},
				},
			}
			bt, _ := json.Marshal(term)
			writeEvent(string(bt))
		} else {
			// Plain text: split into two content-delta chunks.
			mid := len(script) / 2
			parts := [2]string{script[:mid], script[mid:]}
			for _, part := range parts {
				chunk := map[string]any{
					"id":     "chatcmpl-test",
					"object": "chat.completion.chunk",
					"choices": []any{
						map[string]any{
							"index": 0,
							"delta": map[string]any{
								"content": part,
							},
							"finish_reason": nil,
						},
					},
				}
				b, _ := json.Marshal(chunk)
				writeEvent(string(b))
			}
			// Terminating chunk with finish_reason "stop".
			stop := map[string]any{
				"id":     "chatcmpl-test",
				"object": "chat.completion.chunk",
				"choices": []any{
					map[string]any{
						"index":         0,
						"delta":         map[string]any{},
						"finish_reason": "stop",
					},
				},
			}
			bs, _ := json.Marshal(stop)
			writeEvent(string(bs))
		}
		writeEvent("[DONE]")
	}))

	t.Cleanup(srv.Close)
	return srv
}

// ─── recorder (acp.Client impl) ────────────────────────────────────────────────

// recorder implements acp.Client and captures every inbound call so tests can
// assert on what the agent sent.
type recorder struct {
	mu      sync.Mutex
	updates []acpsdk.SessionNotification
	perms   []acpsdk.RequestPermissionRequest

	// permFunc, when non-nil, is called for each RequestPermission.
	// Return (response, true) to answer; (_, false) falls through to the default.
	// Default: cancelled (deny without a selected option).
	permFunc func(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, bool)

	// firstUpdateCh is closed on the first SessionUpdate call.
	// Initialized by newTestEnv; tests use waitForFirstUpdate.
	firstUpdateOnce sync.Once
	firstUpdateCh   chan struct{}

	// fsFiles, when non-nil, turns the recorder into a fake editor-buffer
	// backend: ReadTextFile serves from it, WriteTextFile stores into it, and
	// fsReads/fsWrites record the request paths (see fs_e2e_test.go).
	fsFiles  map[string]string
	fsReads  []string
	fsWrites []string
}

func (r *recorder) SessionUpdate(_ context.Context, params acpsdk.SessionNotification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, params)
	// Only signal "first update" for turn-content updates — not housekeeping
	// notifications like available_commands_update. Tests that call
	// waitForFirstUpdate use it to detect "a Prompt turn is in flight" (e.g.
	// the first agent_message_chunk from a streaming LLM response). Triggering
	// on housekeeping updates would cause those tests to race-skip the wait.
	u := params.Update
	isTurnContent := u.AgentMessageChunk != nil ||
		u.AgentThoughtChunk != nil ||
		u.ToolCall != nil ||
		u.ToolCallUpdate != nil ||
		u.UserMessageChunk != nil ||
		u.UsageUpdate != nil
	if isTurnContent {
		r.firstUpdateOnce.Do(func() {
			if r.firstUpdateCh != nil {
				close(r.firstUpdateCh)
			}
		})
	}
	return nil
}

// waitForFirstUpdate blocks until the first turn-content SessionUpdate is
// received or the timeout elapses. Returns true if an update arrived in time.
func (r *recorder) waitForFirstUpdate(timeout time.Duration) bool {
	if r.firstUpdateCh == nil {
		return false
	}
	select {
	case <-r.firstUpdateCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (r *recorder) RequestPermission(ctx context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	r.mu.Lock()
	r.perms = append(r.perms, params)
	fn := r.permFunc
	r.mu.Unlock()
	if fn != nil {
		if resp, ok := fn(ctx, params); ok {
			return resp, nil
		}
	}
	// Default: respond with "cancelled" (safe headless deny).
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.NewRequestPermissionOutcomeCancelled(),
	}, nil
}

// ReadTextFile serves fsFiles when the test installed an editor-buffer fake
// (see fs_e2e_test.go); with no fsFiles it errors like a capability-less client.
func (r *recorder) ReadTextFile(_ context.Context, req acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fsFiles != nil {
		r.fsReads = append(r.fsReads, req.Path)
		if content, ok := r.fsFiles[req.Path]; ok {
			return acpsdk.ReadTextFileResponse{Content: content}, nil
		}
		return acpsdk.ReadTextFileResponse{}, acpsdk.NewInvalidRequest("file not found: " + req.Path)
	}
	return acpsdk.ReadTextFileResponse{}, acpsdk.NewInvalidRequest("ReadTextFile not supported by test recorder")
}

// WriteTextFile records the write into fsFiles when the editor-buffer fake is
// installed; otherwise it errors like a capability-less client.
func (r *recorder) WriteTextFile(_ context.Context, req acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fsFiles != nil {
		r.fsFiles[req.Path] = req.Content
		r.fsWrites = append(r.fsWrites, req.Path)
		return acpsdk.WriteTextFileResponse{}, nil
	}
	return acpsdk.WriteTextFileResponse{}, acpsdk.NewInvalidRequest("WriteTextFile not supported by test recorder")
}

func (r *recorder) CreateTerminal(_ context.Context, _ acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, acpsdk.NewInvalidRequest("CreateTerminal not supported by test recorder")
}

func (r *recorder) KillTerminal(_ context.Context, _ acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, acpsdk.NewInvalidRequest("KillTerminal not supported by test recorder")
}

func (r *recorder) TerminalOutput(_ context.Context, _ acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, acpsdk.NewInvalidRequest("TerminalOutput not supported by test recorder")
}

func (r *recorder) ReleaseTerminal(_ context.Context, _ acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, acpsdk.NewInvalidRequest("ReleaseTerminal not supported by test recorder")
}

func (r *recorder) WaitForTerminalExit(_ context.Context, _ acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, acpsdk.NewInvalidRequest("WaitForTerminalExit not supported by test recorder")
}

// snapshotUpdates returns a point-in-time copy of recorded SessionNotifications.
// Safe to call concurrently with the SDK inbound goroutine.
func (r *recorder) snapshotUpdates() []acpsdk.SessionNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]acpsdk.SessionNotification(nil), r.updates...)
}

// snapshotPerms returns a point-in-time copy of recorded RequestPermissionRequests.
// Safe to call concurrently with the SDK inbound goroutine.
func (r *recorder) snapshotPerms() []acpsdk.RequestPermissionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]acpsdk.RequestPermissionRequest(nil), r.perms...)
}

// ─── test env ──────────────────────────────────────────────────────────────────

// env is a ready-to-use test environment: a shell3 Runtime behind ACP, plus an
// SDK client-side connection to talk to it.
type env struct {
	rt        *shell3.Runtime
	conn      *acpsdk.ClientSideConnection
	rec       *recorder
	cancel    context.CancelFunc
	releaseCh chan struct{} // close to unblock a "block" script in the fake LLM

	// workDir is the filesystem root used by this env's runtime. Shared across
	// environments created with newTestEnvSameDir so they see the same runs store.
	workDir string

	mu    sync.Mutex
	agent *acpAgent     // set by the onReady hook inside Run
	ready chan struct{} // closed once by the onReady hook; signals getAgent()
}

// getAgent waits up to 5 s for the onReady hook to fire and returns the agent.
// It blocks on a channel rather than spinning, so it yields the goroutine while
// waiting and fails loudly (t.Fatal) if the agent never becomes ready.
func (e *env) getAgent(t *testing.T) *acpAgent {
	t.Helper()
	select {
	case <-e.ready:
		e.mu.Lock()
		a := e.agent
		e.mu.Unlock()
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("getAgent: agent not ready within 5 s")
		return nil // unreachable; t.Fatal stops the test
	}
}

// newTestEnvFull is the internal constructor used by both newTestEnv and
// newTestEnvWithGate. When onToolCallLua is non-empty it is appended to the
// generated shell3.lua so tests can inject hook snippets (e.g. an on_tool_call
// gate) without touching the base harness.
func newTestEnvFull(t *testing.T, askPrompt, askReason string, scripts ...string) *env {
	t.Helper()

	releaseCh := make(chan struct{})
	llmSrv := newFakeLLM(t, releaseCh, scripts...)

	cfgDir := t.TempDir()
	luaPath := filepath.Join(cfgDir, "shell3.lua")

	onToolCallSnippet := ""
	if askPrompt != "" {
		onToolCallSnippet = fmt.Sprintf(`
shell3.on_tool_call(function(t)
  if t.name == "bash" then
    return { ask = %q, reason = %q }
  end
end)
`, askPrompt, askReason)
	}

	luaContent := fmt.Sprintf(`
local helper = shell3.subagent({
  name        = "helper",
  description = "A helper subagent for delegation tests.",
  model       = "test",
  prompt      = "You are a helper.",
  tools       = { bash = true },
})
shell3.model("test", {
  base_url       = %q,
  api_key        = "test",
  model          = "gpt-4o",
  context_window = 128000,
})
shell3.agent({
  name       = "code",
  model      = "test",
  prompt     = "You are a coding assistant.",
  delegation = true,
  tools = { bash = true, subagents = { helper } },
})
shell3.agent({
  name  = "plan",
  model = "test",
  prompt = "You are a planning assistant.",
  tools = { bash = true },
})
`+onToolCallSnippet, llmSrv.URL)
	if err := os.WriteFile(luaPath, []byte(luaContent), 0o644); err != nil {
		t.Fatalf("write shell3.lua: %v", err)
	}

	workDir := t.TempDir()
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{
		ConfigPath: luaPath,
		WorkDir:    workDir,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	e := &env{
		rt:        rt,
		rec:       &recorder{firstUpdateCh: make(chan struct{})},
		cancel:    cancel,
		ready:     make(chan struct{}),
		releaseCh: releaseCh,
		workDir:   workDir,
	}

	go func() {
		_ = Run(ctx, rt, agentIn, agentOut, Options{
			DefaultAgent: "code",
			onReady: func(a *acpAgent) {
				e.mu.Lock()
				e.agent = a
				e.mu.Unlock()
				close(e.ready)
			},
		})
		_ = agentOut.Close()
		_ = agentIn.Close()
	}()

	e.conn = acpsdk.NewClientSideConnection(e.rec, clientOut, clientIn)

	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = agentOut.Close()
		_ = rt.Close()
	})

	return e
}

// newTestEnv builds a complete ACP test environment:
//   - Starts a fake OpenAI-compatible LLM server serving the supplied scripts.
//   - Writes a minimal shell3.lua in a temp dir pointing at the fake LLM.
//   - Creates a shell3.Runtime from that config.
//   - Connects agent ↔ client via in-process io.Pipe pairs.
//   - Launches Run() in a goroutine.
//   - Returns env with an SDK ClientSideConnection ready for use.
//
// Two agents — "code" (with delegation + a helper subagent) and "plan" — are
// declared so later mode tests have two selectable agents.
//
// Cleanup (via t.Cleanup) closes pipes, cancels context, and closes the runtime.
func newTestEnv(t *testing.T, scripts ...string) *env {
	t.Helper()
	return newTestEnvFull(t, "", "", scripts...)
}

// newTestEnvSameDir creates a second ACP test environment that shares parent's
// workDir. Because both runtimes use the same .shell3_project/runs/ directory
// (the file-native store has no inter-process lock), a session persisted in
// parent can be loaded by the new env.
//
// A fresh fake LLM server is started (so the env can handle its own prompts),
// and a new shell3.lua is written into a separate cfgDir. The new runtime uses
// parent.workDir as its WorkDir — that is the only shared state.
//
// Cleanup follows the same pattern as newTestEnvFull.
func newTestEnvSameDir(t *testing.T, parent *env, scripts ...string) *env {
	t.Helper()

	releaseCh := make(chan struct{})
	llmSrv := newFakeLLM(t, releaseCh, scripts...)

	cfgDir := t.TempDir()
	luaPath := filepath.Join(cfgDir, "shell3.lua")

	// Minimal config: same two agents as the standard env so mode tests work,
	// but pointing to the new fake LLM. No subagent needed for session tests.
	luaContent := fmt.Sprintf(`
shell3.model("test", {
  base_url       = %q,
  api_key        = "test",
  model          = "gpt-4o",
  context_window = 128000,
})
shell3.agent({
  name  = "code",
  model = "test",
  prompt = "You are a coding assistant.",
  tools = { bash = true },
})
shell3.agent({
  name  = "plan",
  model = "test",
  prompt = "You are a planning assistant.",
  tools = { bash = true },
})
`, llmSrv.URL)
	if err := os.WriteFile(luaPath, []byte(luaContent), 0o644); err != nil {
		t.Fatalf("newTestEnvSameDir: write shell3.lua: %v", err)
	}

	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{
		ConfigPath: luaPath,
		WorkDir:    parent.workDir, // shared with parent
	})
	if err != nil {
		t.Fatalf("newTestEnvSameDir: NewRuntime: %v", err)
	}

	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	e := &env{
		rt:        rt,
		rec:       &recorder{firstUpdateCh: make(chan struct{})},
		cancel:    cancel,
		ready:     make(chan struct{}),
		releaseCh: releaseCh,
		workDir:   parent.workDir,
	}

	go func() {
		_ = Run(ctx, rt, agentIn, agentOut, Options{
			DefaultAgent: "code",
			onReady: func(a *acpAgent) {
				e.mu.Lock()
				e.agent = a
				e.mu.Unlock()
				close(e.ready)
			},
		})
		_ = agentOut.Close()
		_ = agentIn.Close()
	}()

	e.conn = acpsdk.NewClientSideConnection(e.rec, clientOut, clientIn)

	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = agentOut.Close()
		_ = rt.Close()
	})

	return e
}
