# Phase 1: Structural Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract built-in tool dispatch from a monolithic if-else into a `ToolHandler` interface, decompose `chat.Config` into `TurnConfig`/`ToolConfig`, and add targeted hardening (panic stack capture, bash context threading, openai diagnostic comments).

**Architecture:** Interface-first, incremental wiring. Each handler is extracted and tested independently before the dispatch loop is replaced. `chat.Config` stays as the public assembly struct; internal functions receive the narrower `TurnConfig`. Existing tests must stay green at every commit.

**Tech Stack:** Go 1.25, `encoding/json`, `context`, `runtime/debug`. No new dependencies.

---

## File Map

**Create:**
- `internal/chat/toolhandler.go` — `ToolHandler` interface + `ToolConfig` + `TurnConfig` structs
- `internal/chat/handler_bash.go` — `BashHandler` struct implementing `ToolHandler`
- `internal/chat/handler_bash_test.go`
- `internal/chat/handler_prune.go` — `PruneHandler` struct + `looksLikeError` + prune helpers
- `internal/chat/handler_prune_test.go`
- `internal/chat/handler_store.go` — `StoreHandler` struct + all memory/history helpers
- `internal/chat/handler_store_test.go`
- `internal/chat/handler_docs.go` — `DocsHandler` struct
- `internal/chat/handler_docs_test.go`
- `internal/chat/handler_edit.go` — `EditHandler` struct (wraps `handleEditTool` in `edit_dispatch.go`)

**Modify:**
- `internal/chat/toolhandler.go` (created above)
- `internal/chat/tools.go` — remove extracted functions/constants; keep `dispatchUserTool`, `truncateOutput`, `handleCompactHistory` (updated signature), `saveHistory` (updated signature), `toolCallSummary`, `parseRawArgs`
- `internal/chat/turn.go` — replace if-else dispatch with handler map lookup; update `runTurn` signature to `TurnConfig`; update panic recovery; update `dumpStreamError`/`saveHistory` calls
- `internal/chat/chat.go` — add `NewHandlers()` constructor; update `RunInteractive` and `RunOnce` to build `TurnConfig`
- `internal/chat/tools_test.go` — update `handleCompactHistory` call after signature change
- `internal/adapters/openai/client.go` — add intentional-ignore comment on two `io.ReadAll` lines

---

## Task 1: Define ToolHandler Interface, ToolConfig, and TurnConfig

**Files:**
- Create: `internal/chat/toolhandler.go`

No existing code changes yet. Just define the contracts.

- [ ] **Step 1: Create toolhandler.go**

```go
package chat

import (
	"context"
	"encoding/json"

	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/usertools"
)

// ToolHandler is the interface for built-in tool implementations.
// Each built-in tool (bash, edit_file, prune_tool_result, etc.) implements this.
// User tools (YAML-defined) use a separate dispatch path and do not implement this interface.
type ToolHandler interface {
	Name() string
	Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}

// ToolConfig holds per-invocation state passed to ToolHandler.Execute.
// Mutations to AllMsgs and SessMsgs elements propagate to the caller's slices.
type ToolConfig struct {
	Store    *store.Store
	WorkDir  string
	Secrets  map[string]string
	AllMsgs  []llm.Message
	SessMsgs []llm.Message
}

// TurnConfig holds all dependencies needed for one user→assistant turn.
// It is constructed from Config in RunInteractive/RunOnce and passed to runTurn.
type TurnConfig struct {
	LLM         LLMClient
	Hooks        *hooks.Runner
	Personality  persona.Persona
	StatusLine   string
	WorkDir      string
	Store        *store.Store
	UserTools    map[string]usertools.Tool
	Secrets      map[string]string
	Truncate     bool
	Handlers     map[string]ToolHandler
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /path/to/shell3 && go build ./internal/chat/...
```

Expected: no errors (new file, no dependencies broken).

- [ ] **Step 3: Commit**

```bash
git add internal/chat/toolhandler.go
git commit -m "feat(chat): define ToolHandler interface, ToolConfig, and TurnConfig"
```

---

## Task 2: Implement BashHandler

**Files:**
- Create: `internal/chat/handler_bash.go`
- Create: `internal/chat/handler_bash_test.go`

Extracts `executeBash` and `parseBashCommand` from `tools.go`. Removes `bashTimeout` constant — the turn context is used directly. The handler is not wired into the dispatch loop yet.

- [ ] **Step 1: Write the failing test**

Create `internal/chat/handler_bash_test.go`:

```go
package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBashHandler_Name(t *testing.T) {
	h := BashHandler{}
	if h.Name() != "bash" {
		t.Fatalf("Name() = %q, want %q", h.Name(), "bash")
	}
}

func TestBashHandler_Execute_echo(t *testing.T) {
	h := BashHandler{}
	args := json.RawMessage(`{"command":"echo hello"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected output to contain 'hello', got %q", out)
	}
}

func TestBashHandler_Execute_emptyOutput(t *testing.T) {
	h := BashHandler{}
	args := json.RawMessage(`{"command":"true"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "(no output)" {
		t.Fatalf("expected '(no output)', got %q", out)
	}
}

func TestBashHandler_Execute_canceledContext(t *testing.T) {
	h := BashHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args := json.RawMessage(`{"command":"echo should not run"}`)
	out, _ := h.Execute(ctx, "1", args, ToolConfig{})
	// Should return error output or empty — must not block.
	_ = out
}

func TestBashHandler_Execute_nonzeroExit(t *testing.T) {
	h := BashHandler{}
	args := json.RawMessage(`{"command":"echo oops && exit 1"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err) // Execute never returns an error — exit codes are in output
	}
	if !strings.Contains(out, "oops") {
		t.Fatalf("expected 'oops' in output, got %q", out)
	}
}
```

- [ ] **Step 2: Run failing test**

```bash
go test ./internal/chat/... -run TestBashHandler -v
```

Expected: FAIL — `BashHandler undefined`.

- [ ] **Step 3: Create handler_bash.go**

```go
package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// BashHandler executes a bash command and returns its combined stdout+stderr.
// It respects context cancellation — callers set timeouts before invoking Execute.
// Exit codes are not returned as errors; non-zero exit appends the error to output.
type BashHandler struct{}

func (BashHandler) Name() string { return "bash" }

func (BashHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	command := parseBashArgs(string(args))
	c := exec.CommandContext(ctx, "bash", "-c", command)
	c.Dir = cfg.WorkDir
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Run(); err != nil {
		if buf.Len() == 0 {
			fmt.Fprintf(&buf, "error: %v\n", err)
		}
	}
	if buf.Len() == 0 {
		return "(no output)", nil
	}
	return buf.String(), nil
}

// parseBashArgs extracts the "command" field from bash tool JSON args.
// Takes string (not json.RawMessage) so it can be called from turn.go
// where tc.RawArgs is a string without a type conversion.
func parseBashArgs(raw string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return raw
	}
	return args.Command
}
```

- [ ] **Step 4: Run passing test**

```bash
go test ./internal/chat/... -run TestBashHandler -v
```

Expected: all 5 `TestBashHandler_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/handler_bash.go internal/chat/handler_bash_test.go
git commit -m "feat(chat): add BashHandler implementing ToolHandler"
```

---

## Task 3: Implement PruneHandler

**Files:**
- Create: `internal/chat/handler_prune.go`
- Create: `internal/chat/handler_prune_test.go`

Moves `handlePruneToolResult`, `pruneToolResultByID`, `looksLikeError`, and `minPruneBytes` from `tools.go` to this file. The functions stay unexported — they move, not change. Do NOT delete them from `tools.go` yet (Task 9 does that).

- [ ] **Step 1: Write the failing test**

Create `internal/chat/handler_prune_test.go`:

```go
package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestPruneHandler_Name(t *testing.T) {
	if (PruneHandler{}).Name() != "prune_tool_result" {
		t.Fatal("wrong name")
	}
}

func TestPruneHandler_Execute_success(t *testing.T) {
	h := PruneHandler{}
	// Build a message slice with a tool result large enough to prune.
	content := strings.Repeat("x", 600)
	allMsgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "42", Name: "bash", Content: content},
	}
	cfg := ToolConfig{AllMsgs: allMsgs, SessMsgs: allMsgs}
	args := json.RawMessage(`{"tool_call_id":"42","reason":"no longer needed"}`)

	out, err := h.Execute(context.Background(), "1", args, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Pruned result of bash") {
		t.Fatalf("unexpected output: %q", out)
	}
	// Mutation propagates back through the slice.
	if !strings.HasPrefix(allMsgs[0].Content, "[pruned:") {
		t.Fatalf("expected content to be stubbed, got %q", allMsgs[0].Content)
	}
}

func TestPruneHandler_Execute_tooSmall(t *testing.T) {
	h := PruneHandler{}
	allMsgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "1", Content: "tiny"},
	}
	cfg := ToolConfig{AllMsgs: allMsgs, SessMsgs: allMsgs}
	args := json.RawMessage(`{"tool_call_id":"1","reason":"test"}`)
	out, _ := h.Execute(context.Background(), "1", args, cfg)
	if !strings.Contains(out, "below") {
		t.Fatalf("expected below-threshold message, got %q", out)
	}
}

func TestPruneHandler_Execute_missingID(t *testing.T) {
	h := PruneHandler{}
	cfg := ToolConfig{}
	args := json.RawMessage(`{"reason":"test"}`)
	out, _ := h.Execute(context.Background(), "1", args, cfg)
	if !strings.Contains(out, "tool_call_id required") {
		t.Fatalf("expected validation error, got %q", out)
	}
}

func TestLooksLikeError(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"error: something failed", true},
		{"Error something", true},
		{"[tool_call_id=1]\nerror: boom", true},
		{"[tool_call_id=1]\nok output", false},
		{"normal output", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := looksLikeError(tt.input); got != tt.want {
			t.Errorf("looksLikeError(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run failing test**

```bash
go test ./internal/chat/... -run "TestPruneHandler|TestLooksLikeError" -v
```

Expected: FAIL — `PruneHandler undefined`.

- [ ] **Step 3: Create handler_prune.go**

```go
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

const minPruneBytes = 500

// PruneHandler implements the prune_tool_result built-in tool.
// It replaces a prior tool result in the conversation with a short stub,
// freeing context window space. Mutations propagate through the slice elements.
type PruneHandler struct{}

func (PruneHandler) Name() string { return "prune_tool_result" }

func (PruneHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return handlePruneToolResultFrom(string(args), cfg.AllMsgs, cfg.SessMsgs), nil
}

func handlePruneToolResultFrom(rawArgs string, slices ...[]llm.Message) string {
	var args struct {
		ToolCallID string `json:"tool_call_id"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if args.ToolCallID == "" {
		return "error: tool_call_id required"
	}
	if args.Reason == "" {
		return "error: reason required"
	}
	stem := fmt.Sprintf("pruned: %s", args.Reason)
	return pruneByID(args.ToolCallID, stem, slices...)
}

func pruneByID(toolCallID, stem string, slices ...[]llm.Message) string {
	var target *llm.Message
	var name string
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				target = &msgs[i]
				name = msgs[i].Name
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("error: no tool result with id %q in conversation", toolCallID)
	}

	content := target.Content
	if len(content) < minPruneBytes {
		return fmt.Sprintf("error: result is %d bytes; below %d-byte prune threshold", len(content), minPruneBytes)
	}
	if looksLikeError(content) {
		return "error: refusing to prune a result that looks like a tool error"
	}

	stub := fmt.Sprintf("[%s — original was %d bytes]", stem, len(content))
	count := 0
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				msgs[i].Content = stub
				count++
			}
		}
	}
	if count == 0 {
		return "error: failed to update message content"
	}
	return fmt.Sprintf("Pruned result of %s (id=%s): freed %d bytes", name, toolCallID, len(content)-len(stub))
}

func looksLikeError(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "[tool_call_id=") {
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			t = strings.TrimSpace(t[nl+1:])
		} else {
			return false
		}
	}
	if t == "" {
		return false
	}
	low := strings.ToLower(t)
	return strings.HasPrefix(low, "error:") || strings.HasPrefix(low, "error ")
}
```

- [ ] **Step 4: Run passing test**

```bash
go test ./internal/chat/... -run "TestPruneHandler|TestLooksLikeError" -v
```

Expected: all PASS. Note: `looksLikeError` is now defined twice (tools.go + handler_prune.go). This will cause a compile error. Temporarily rename the one in tools.go to `looksLikeErrorOld` to get tests green, or simply delete the duplicate from tools.go now (Task 9 will clean the rest). Simplest: delete just `looksLikeError` from tools.go now since it's self-contained.

```bash
# Remove looksLikeError from tools.go (lines 90-106)
# Use your editor or sed — the function has no callers left after handler_prune.go defines it.
go build ./internal/chat/...
```

- [ ] **Step 5: Run full test suite**

```bash
go test ./internal/chat/...
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/handler_prune.go internal/chat/handler_prune_test.go internal/chat/tools.go
git commit -m "feat(chat): add PruneHandler, move prune logic out of tools.go"
```

---

## Task 4: Implement StoreHandler

**Files:**
- Create: `internal/chat/handler_store.go`
- Create: `internal/chat/handler_store_test.go`

Extracts all memory/history helpers from `tools.go`. Uses a single `StoreHandler` struct parameterized by tool name — one struct, five instances in the handler map.

- [ ] **Step 1: Write the failing test**

Create `internal/chat/handler_store_test.go`:

```go
package chat

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	st, err := store.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestStoreHandler_Name(t *testing.T) {
	for _, name := range []string{"memory_upsert", "memory_list", "memory_search", "history_get", "history_search"} {
		h := StoreHandler{toolName: name}
		if h.Name() != name {
			t.Errorf("Name() = %q, want %q", h.Name(), name)
		}
	}
}

func TestStoreHandler_MemoryUpsertAndList(t *testing.T) {
	st := openTestStore(t)
	h := StoreHandler{toolName: "memory_upsert"}
	args := json.RawMessage(`{"key":"color","value":"blue"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Stored") {
		t.Fatalf("unexpected upsert output: %q", out)
	}

	lh := StoreHandler{toolName: "memory_list"}
	out, _ = lh.Execute(context.Background(), "2", json.RawMessage(`{}`), ToolConfig{Store: st})
	if !strings.Contains(out, "color") || !strings.Contains(out, "blue") {
		t.Fatalf("memory_list missing stored entry: %q", out)
	}
}

func TestStoreHandler_MemorySearch(t *testing.T) {
	st := openTestStore(t)
	// Upsert first.
	uh := StoreHandler{toolName: "memory_upsert"}
	_, _ = uh.Execute(context.Background(), "1", json.RawMessage(`{"key":"lang","value":"golang"}`), ToolConfig{Store: st})

	sh := StoreHandler{toolName: "memory_search"}
	args := json.RawMessage(`{"terms":["golang"]}`)
	out, _ := sh.Execute(context.Background(), "2", args, ToolConfig{Store: st})
	if !strings.Contains(out, "golang") {
		t.Fatalf("memory_search did not find entry: %q", out)
	}
}

func TestStoreHandler_NilStore(t *testing.T) {
	h := StoreHandler{toolName: "memory_list"}
	out, _ := h.Execute(context.Background(), "1", json.RawMessage(`{}`), ToolConfig{Store: nil})
	if !strings.Contains(out, "store not available") {
		t.Fatalf("expected store-not-available error, got %q", out)
	}
}

func TestStoreHandler_HistoryGet_noHistory(t *testing.T) {
	st := openTestStore(t)
	h := StoreHandler{toolName: "history_get"}
	out, _ := h.Execute(context.Background(), "1", json.RawMessage(`{}`), ToolConfig{Store: st})
	if out != "No history found." {
		t.Fatalf("expected 'No history found.', got %q", out)
	}
}
```

- [ ] **Step 2: Run failing test**

```bash
go test ./internal/chat/... -run TestStoreHandler -v
```

Expected: FAIL — `StoreHandler undefined`.

- [ ] **Step 3: Create handler_store.go**

```go
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/store"
)

// StoreHandler implements memory and history built-in tools.
// One struct, five tool names: memory_upsert, memory_list, memory_search,
// history_get, history_search. Each instance handles one tool name.
type StoreHandler struct {
	toolName string
}

func (h StoreHandler) Name() string { return h.toolName }

func (h StoreHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	if cfg.Store == nil {
		return fmt.Sprintf("error: store not available for tool %s", h.toolName), nil
	}
	switch h.toolName {
	case "memory_upsert":
		return storeMemoryUpsert(string(args), cfg.Store), nil
	case "memory_list":
		return storeMemoryList(string(args), cfg.Store), nil
	case "memory_search":
		return storeMemorySearch(string(args), cfg.Store), nil
	case "history_get":
		return storeHistoryGet(string(args), cfg.Store), nil
	case "history_search":
		return storeHistorySearch(string(args), cfg.Store), nil
	default:
		return fmt.Sprintf("unknown tool: %s", h.toolName), nil
	}
}

func storeMemoryUpsert(rawArgs string, st *store.Store) string {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Core  *bool  `json:"core"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if args.Key == "" {
		return "error: key required"
	}
	if err := st.MemoryUpsert(args.Key, args.Value, args.Core); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if args.Value == "" {
		return "Removed: " + args.Key
	}
	if args.Core != nil && *args.Core {
		return "Stored (core): " + args.Key
	}
	return "Stored: " + args.Key
}

func storeMemoryList(rawArgs string, st *store.Store) string {
	var args struct {
		CoreOnly bool `json:"core_only"`
		Limit    int  `json:"limit"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &args)
	results, err := st.MemoryQuery("", args.CoreOnly, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return renderMemoryEntries(results)
}

func storeMemorySearch(rawArgs string, st *store.Store) string {
	var args struct {
		Terms    []string `json:"terms"`
		Match    string   `json:"match"`
		CoreOnly bool     `json:"core_only"`
		Limit    int      `json:"limit"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if len(args.Terms) == 0 {
		return "error: terms[] required (one concept per element)"
	}
	expr := store.BuildFTSExpr(args.Terms, args.Match == "all")
	if expr == "" {
		return "No memories found."
	}
	results, err := st.MemorySearchExpr(expr, args.CoreOnly, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return renderMemoryEntries(results)
}

func renderMemoryEntries(results []store.MemoryEntry) string {
	if len(results) == 0 {
		return "No memories found."
	}
	var sb strings.Builder
	for _, r := range results {
		marker := ""
		if r.Core {
			marker = " (core)"
		}
		fmt.Fprintf(&sb, "[%s%s]: %s\n", r.Key, marker, r.Value)
	}
	return sb.String()
}

func storeHistoryGet(rawArgs string, st *store.Store) string {
	var args struct {
		SessionID int64 `json:"session_id"`
		Chunk     int   `json:"chunk"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &args)
	chunk := args.Chunk
	if chunk > 0 {
		chunk--
	}
	res, err := st.HistoryGet(args.SessionID, chunk)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if res.SessionID == 0 && len(res.Turns) == 0 {
		return "No history found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "session %d, chunk %d/%d (started %s)",
		res.SessionID, res.Chunk+1, res.TotalChunks,
		res.SessionStartedAt.Format("2006-01-02 15:04"))
	if res.PrevSessionID != 0 {
		fmt.Fprintf(&sb, " | prev=%d", res.PrevSessionID)
	}
	if res.NextSessionID != 0 {
		fmt.Fprintf(&sb, " | next=%d", res.NextSessionID)
	}
	sb.WriteByte('\n')
	for _, t := range res.Turns {
		fmt.Fprintf(&sb, "[%s | %s] %s\n",
			t.CreatedAt.Format("2006-01-02 15:04"), t.Role, t.Content)
	}
	return sb.String()
}

func storeHistorySearch(rawArgs string, st *store.Store) string {
	var args struct {
		Terms []string `json:"terms"`
		Match string   `json:"match"`
		Limit int      `json:"limit"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if len(args.Terms) == 0 {
		return "error: terms[] required (one concept per element)"
	}
	expr := store.BuildFTSExpr(args.Terms, args.Match == "all")
	if expr == "" {
		return "No history found."
	}
	res, err := st.HistorySearchExpr(expr, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if res.TotalHits == 0 {
		return "No history found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "search hits: %d\n", res.TotalHits)
	for _, h := range res.Hits {
		fmt.Fprintf(&sb, "[session %d chunk %d | %s | %s] %s\n",
			h.SessionID, h.Chunk+1,
			h.CreatedAt.Format("2006-01-02 15:04"), h.Role, h.Content)
	}
	return sb.String()
}
```

- [ ] **Step 4: Run passing test**

```bash
go test ./internal/chat/... -run TestStoreHandler -v
```

Expected: all PASS. Note: `renderMemories` in tools.go and `renderMemoryEntries` in handler_store.go coexist — no conflict (different names). The old functions in tools.go will be deleted in Task 9.

- [ ] **Step 5: Run full suite**

```bash
go test ./internal/chat/...
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/handler_store.go internal/chat/handler_store_test.go
git commit -m "feat(chat): add StoreHandler for memory/history tools"
```

---

## Task 5: Implement DocsHandler

**Files:**
- Create: `internal/chat/handler_docs.go`
- Create: `internal/chat/handler_docs_test.go`

Simple: returns the docs string passed at construction time.

- [ ] **Step 1: Write the failing test**

Create `internal/chat/handler_docs_test.go`:

```go
package chat

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDocsHandler_Name(t *testing.T) {
	if (DocsHandler{}).Name() != "shell3_docs" {
		t.Fatal("wrong name")
	}
}

func TestDocsHandler_Execute_withDocs(t *testing.T) {
	h := DocsHandler{docs: "# shell3 docs"}
	out, err := h.Execute(context.Background(), "1", json.RawMessage(`{}`), ToolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "# shell3 docs" {
		t.Fatalf("got %q", out)
	}
}

func TestDocsHandler_Execute_noDocs(t *testing.T) {
	h := DocsHandler{}
	out, _ := h.Execute(context.Background(), "1", json.RawMessage(`{}`), ToolConfig{})
	if out != "Documentation not available." {
		t.Fatalf("expected fallback, got %q", out)
	}
}
```

- [ ] **Step 2: Run failing test**

```bash
go test ./internal/chat/... -run TestDocsHandler -v
```

Expected: FAIL — `DocsHandler undefined`.

- [ ] **Step 3: Create handler_docs.go**

```go
package chat

import (
	"context"
	"encoding/json"
)

// DocsHandler implements the shell3_docs built-in tool.
// The docs string is set at construction time from cfg.Docs.
type DocsHandler struct {
	docs string
}

func (DocsHandler) Name() string { return "shell3_docs" }

func (h DocsHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	if h.docs == "" {
		return "Documentation not available.", nil
	}
	return h.docs, nil
}
```

- [ ] **Step 4: Run passing test**

```bash
go test ./internal/chat/... -run TestDocsHandler -v
```

Expected: all PASS.

- [ ] **Step 5: Run full suite**

```bash
go test ./internal/chat/...
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/handler_docs.go internal/chat/handler_docs_test.go
git commit -m "feat(chat): add DocsHandler for shell3_docs tool"
```

---

## Task 6: Implement EditHandler

**Files:**
- Create: `internal/chat/handler_edit.go`

Wraps `handleEditTool` from `edit_dispatch.go`. No code moves — edit_dispatch.go is unchanged. The handler delegates to the existing function.

- [ ] **Step 1: Create handler_edit.go** (no failing test first — handleEditTool is already tested in edit_dispatch_test.go; this is a thin wrapper)

```go
package chat

import (
	"context"
	"encoding/json"
)

// EditHandler implements the edit_file built-in tool.
// It delegates to handleEditTool in edit_dispatch.go.
type EditHandler struct{}

func (EditHandler) Name() string { return "edit_file" }

func (EditHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return handleEditTool("edit_file", string(args), cfg.WorkDir), nil
}
```

- [ ] **Step 2: Build and run full suite**

```bash
go build ./internal/chat/... && go test ./internal/chat/...
```

Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/chat/handler_edit.go
git commit -m "feat(chat): add EditHandler wrapping handleEditTool"
```

---

## Task 7: Wire Handlers into TurnConfig, Update RunInteractive and RunOnce

**Files:**
- Modify: `internal/chat/chat.go`
- Modify: `internal/chat/turn.go` (signature change only, no dispatch rewrite yet)
- Modify: `internal/chat/tools.go` (handleCompactHistory + saveHistory signature change)
- Modify: `internal/chat/tools_test.go` (update handleCompactHistory call)

This task changes function signatures. Do it all in one commit to keep the build green.

- [ ] **Step 1: Add NewHandlers() to chat.go**

In `internal/chat/chat.go`, add this function after the `Config` struct (around line 54):

```go
// NewHandlers constructs the built-in tool handler map from a Config.
// Handlers are injected into TurnConfig and looked up by tool name during dispatch.
func NewHandlers(cfg Config) map[string]ToolHandler {
	handlers := []ToolHandler{
		BashHandler{},
		EditHandler{},
		PruneHandler{},
		DocsHandler{docs: cfg.Docs},
		StoreHandler{toolName: "memory_upsert"},
		StoreHandler{toolName: "memory_list"},
		StoreHandler{toolName: "memory_search"},
		StoreHandler{toolName: "history_get"},
		StoreHandler{toolName: "history_search"},
	}
	m := make(map[string]ToolHandler, len(handlers))
	for _, h := range handlers {
		m[h.Name()] = h
	}
	return m
}
```

- [ ] **Step 2: Update RunInteractive in chat.go to build TurnConfig**

Replace the `launchTurn` closure definition (around lines 90-101) to build a `TurnConfig`:

```go
handlers := NewHandlers(cfg)

// launchTurn starts a turn goroutine for userMsg and wires drain.
launchTurn := func(userMsg llm.Message) {
    ch := make(chan patchapp.Event, 256)
    turnCtx, cancel := context.WithCancel(ctx)
    app.SetBusy(true, cancel)
    prevLen := len(sess.messages)
    tc := TurnConfig{
        LLM:         cfg.LLM,
        Hooks:        cfg.Hooks,
        Personality:  cfg.Personality,
        StatusLine:   cfg.StatusLine,
        WorkDir:      cfg.WorkDir,
        Store:        cfg.Store,
        UserTools:    cfg.UserTools,
        Secrets:      cfg.Secrets,
        Truncate:     cfg.Truncate,
        Handlers:     handlers,
    }
    go func() {
        defer cancel()
        runTurn(turnCtx, tc, sess, userMsg, ch)
        saveHistory(cfg.Store, sess, sess.id, prevLen)
    }()
    go drainTurn(ch, app, &lastUsage, &cfg)
}
```

- [ ] **Step 3: Update RunOnce in chat.go to build TurnConfig**

`RunOnce` is at the bottom of chat.go (line 537). Update it:

```go
func RunOnce(ctx context.Context, cfg Config, input string) error {
	sess := &session{}
	ch := make(chan patchapp.Event, 256)
	tc := TurnConfig{
		LLM:        cfg.LLM,
		Hooks:       cfg.Hooks,
		Personality: cfg.Personality,
		StatusLine:  cfg.StatusLine,
		WorkDir:     cfg.WorkDir,
		Store:       cfg.Store,
		UserTools:   cfg.UserTools,
		Secrets:     cfg.Secrets,
		Truncate:    cfg.Truncate,
		Handlers:    NewHandlers(cfg),
	}
	go runTurn(ctx, tc, sess, llm.Message{Role: llm.RoleUser, Content: input}, ch)
	for ev := range ch {
		switch v := ev.(type) {
		case patchapp.ChunkEvent:
			fmt.Print(v.Text)
		case patchapp.AppendEvent:
			fmt.Print(v.Text)
		case patchapp.TurnErrEvent:
			fmt.Fprintln(os.Stderr, "error:", v.Err)
		case patchapp.TurnDoneEvent:
			fmt.Println()
		}
	}
	return nil
}
```

- [ ] **Step 4: Update runTurn signature in turn.go**

Change line 82:
```go
// Before:
func runTurn(ctx context.Context, cfg Config, sess *session, userMsg llm.Message, ch chan<- patchapp.Event) {

// After:
func runTurn(ctx context.Context, cfg TurnConfig, sess *session, userMsg llm.Message, ch chan<- patchapp.Event) {
```

Update the `dumpStreamError` call inside `runTurn` (it currently passes `cfg Config`). Change `dumpStreamError` to accept `TurnConfig`:

```go
// In turn.go, update dumpStreamError signature:
func dumpStreamError(cfg TurnConfig, msgs []llm.Message, streamErr error) {
	if cfg.WorkDir == "" {
		return
	}
	var reqBody, resBody []byte
	if ts, ok := cfg.LLM.(llm.TrafficInspector); ok {
		reqBody, resBody = ts.LastTraffic()
	}
	rec := map[string]any{
		"timestamp":     time.Now().Format(time.RFC3339),
		"error":         streamErr.Error(),
		"messages":      msgs,
		"request_body":  string(reqBody),
		"response_body": string(resBody),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(cfg.WorkDir, ".shell3", "last_error.json")
	_ = os.WriteFile(path, data, 0644)
}
```

- [ ] **Step 5: Update saveHistory signature in tools.go**

Change `saveHistory` (currently takes `cfg Config`) to take `*store.Store` directly:

```go
// In tools.go, replace saveHistory:
func saveHistory(st *store.Store, sess *session, sessionID int64, from int) {
	if st == nil {
		return
	}
	if from > len(sess.messages) {
		return
	}
	for _, m := range sess.messages[from:] {
		switch m.Role {
		case llm.RoleUser, llm.RoleAssistant:
			_ = st.AppendHistory(sessionID, string(m.Role), m.Content)
			for _, tc := range m.ToolCalls {
				_ = st.AppendHistory(sessionID, "tool", toolCallSummary(tc))
			}
		}
	}
}
```

- [ ] **Step 6: Update handleCompactHistory signature in tools.go**

Change `handleCompactHistory` to take `*store.Store` instead of `Config`:

```go
func handleCompactHistory(rawArgs string, st *store.Store, sess *session, allMsgs []llm.Message) (out string, newAllMsgs []llm.Message) {
```

Inside the function, replace all `cfg.Store` references with `st`. The function body is otherwise unchanged — just find/replace `cfg.Store` → `st`.

- [ ] **Step 7: Update the call site in turn.go**

The call to `handleCompactHistory` is around turn.go line 183. Update it:

```go
// Before:
out, allMsgs = handleCompactHistory(tc.RawArgs, cfg, sess, allMsgs)

// After:
out, allMsgs = handleCompactHistory(tc.RawArgs, cfg.Store, sess, allMsgs)
```

- [ ] **Step 8: Update tools_test.go**

The test `TestHandleCompactHistoryIncludesSkillsToReread` (tools_test.go line 10) calls:
```go
handleCompactHistory(`{"summary":"summary","skills":["writing-plans","/tmp/codebase-discovery.md"]}`, Config{}, sess, allMsgs)
```

Update to:
```go
handleCompactHistory(`{"summary":"summary","skills":["writing-plans","/tmp/codebase-discovery.md"]}`, nil, sess, allMsgs)
```

- [ ] **Step 9: Build and run full test suite**

```bash
go build ./... && go test ./internal/chat/...
```

Expected: all PASS. Fix any compilation errors before committing.

- [ ] **Step 10: Commit**

```bash
git add internal/chat/chat.go internal/chat/turn.go internal/chat/tools.go internal/chat/tools_test.go
git commit -m "refactor(chat): wire TurnConfig through runTurn, update saveHistory and handleCompactHistory signatures"
```

---

## Task 8: Replace if-else Dispatch in turn.go with Handler Map Lookup

**Files:**
- Modify: `internal/chat/turn.go` (lines 157–216)

Replace the monolithic if-else with a map lookup for all registered handlers. Keep `compact_history` and `shell_interactive` as special cases (they mutate conversation state or need the event channel). User tools stay on their own path.

- [ ] **Step 1: Replace the dispatch block in turn.go**

The current dispatch block runs from approximately line 157 (`for _, tc := range toolCalls {`) through line 216. The inner if-else (`if tc.Name == "bash"` ... `else { dispatchStore... }`) is replaced. Here is the complete new dispatch block to substitute inside the `for _, tc := range toolCalls` loop:

```go
for _, tc := range toolCalls {
    if ctx.Err() != nil {
        return
    }

    allowed, hookErr := cfg.Hooks.OnToolCall(ctx, tc.Name, parseRawArgs(tc.RawArgs))
    var out string
    if hookErr != nil || !allowed {
        out = fmt.Sprintf("Tool call blocked: %v", hookErr)
    } else if tc.Name == "compact_history" {
        ch <- patchapp.AppendEvent{Text: toolCallHeader(tc.ID, tc.Name, "", false) + "\n"}
        out, allMsgs = handleCompactHistory(tc.RawArgs, cfg.Store, sess, allMsgs)
        ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(out, "\n")) + "\n\n"}
        ch <- patchapp.AppendEvent{Text: patchtui.Dim + "tip: run /reload to pick up any new memories or skills" + patchtui.Reset + "\n\n"}
    } else if tc.Name == "shell_interactive" {
        command := parseBashArgs(tc.RawArgs)
        ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.Yellow+patchtui.Bold+"#%s $ %s"+patchtui.Reset+" (interactive)\n", tc.ID, command)}
        replyC := make(chan string, 1)
        ch <- patchapp.TTYExecEvent{Cmd: command, WorkDir: cfg.WorkDir, ReplyC: replyC}
        out = <-replyC
    } else if userTool, ok := cfg.UserTools[tc.Name]; ok {
        ch <- patchapp.AppendEvent{Text: toolCallHeader(tc.ID, tc.Name, tc.RawArgs, true) + "\n"}
        out = dispatchUserTool(ctx, userTool, tc.RawArgs, cfg.Secrets, cfg.WorkDir)
        display := truncateOutput(out)
        if cfg.Truncate {
            display = out
        }
        ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(display, "\n")) + "\n\n"}
    } else if handler, ok := cfg.Handlers[tc.Name]; ok {
        toolCfg := ToolConfig{
            Store:    cfg.Store,
            WorkDir:  cfg.WorkDir,
            Secrets:  cfg.Secrets,
            AllMsgs:  allMsgs,
            SessMsgs: sess.messages,
        }
        switch tc.Name {
        case "bash":
            command := parseBashArgs(tc.RawArgs)
            ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.Yellow+patchtui.Bold+"#%s $ %s"+patchtui.Reset+"\n", tc.ID, command)}
            out, _ = handler.Execute(ctx, tc.ID, json.RawMessage(tc.RawArgs), toolCfg)
            display := truncateOutput(out)
            if cfg.Truncate {
                display = out
            }
            ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(display, "\n")) + "\n\n"}
        case "edit_file":
            ch <- patchapp.AppendEvent{Text: toolCallHeader(tc.ID, tc.Name, summarizeEditArgs(tc.RawArgs), false) + "\n"}
            out, _ = handler.Execute(ctx, tc.ID, json.RawMessage(tc.RawArgs), toolCfg)
            ch <- patchapp.AppendEvent{Text: colorizeEditOutput(strings.TrimRight(out, "\n")) + "\n\n"}
        case "prune_tool_result":
            ch <- patchapp.AppendEvent{Text: toolCallHeader(tc.ID, tc.Name, tc.RawArgs, false) + "\n"}
            out, _ = handler.Execute(ctx, tc.ID, json.RawMessage(tc.RawArgs), toolCfg)
            ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(out, "\n")) + "\n\n"}
        default:
            ch <- patchapp.AppendEvent{Text: toolCallHeader(tc.ID, tc.Name, tc.RawArgs, false) + "\n"}
            out, _ = handler.Execute(ctx, tc.ID, json.RawMessage(tc.RawArgs), toolCfg)
            display := truncateOutput(out)
            if cfg.Truncate {
                display = out
            }
            ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(display, "\n")) + "\n\n"}
        }
    } else {
        out = fmt.Sprintf("error: unknown tool %q", tc.Name)
        ch <- patchapp.AppendEvent{Text: toolCallHeader(tc.ID, tc.Name, tc.RawArgs, false) + "\n"}
        ch <- patchapp.AppendEvent{Text: dimLines(out) + "\n\n"}
    }

    cfg.Hooks.OnToolResult(ctx, tc.Name, out)
    content := fmt.Sprintf("[tool_call_id=%s]\n%s", tc.ID, out)
    toolMsg := llm.Message{
        Role:       llm.RoleTool,
        Content:    content,
        ToolCallID: tc.ID,
        Name:       tc.Name,
    }
    allMsgs = append(allMsgs, toolMsg)
    sess.append(toolMsg)
}
```

Also add `"encoding/json"` to turn.go imports if not already present (needed for `json.RawMessage`).

Remove the now-unused `parseBashCommand` function from tools.go (it was superseded by `parseBashArgs` in handler_bash.go). Search for any other callers first:

```bash
grep -rn "parseBashCommand" internal/
```

If only tools.go and possibly turn.go use it, delete it from tools.go. The new dispatch uses `parseBashArgs` from handler_bash.go.

- [ ] **Step 2: Build**

```bash
go build ./...
```

Fix any compilation errors (missing imports, unused variables).

- [ ] **Step 3: Run full test suite**

```bash
go test ./internal/chat/...
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chat/turn.go internal/chat/tools.go
git commit -m "refactor(chat): replace if-else tool dispatch with handler map lookup"
```

---

## Task 9: Clean Up tools.go

**Files:**
- Modify: `internal/chat/tools.go`

Remove all functions that have been moved to handler files. After this task, tools.go should only contain: `dispatchUserTool`, `truncateOutput`, `handleCompactHistory`, `saveHistory`, `toolCallSummary`, `parseRawArgs`.

- [ ] **Step 1: Remove dead code from tools.go**

Functions to remove (they now live in handler files):
- `minPruneBytes` constant → moved to handler_prune.go
- `handlePruneToolResult` → superseded by `handlePruneToolResultFrom` in handler_prune.go
- `pruneToolResultByID` → superseded by `pruneByID` in handler_prune.go
- `looksLikeError` → moved to handler_prune.go (already removed in Task 3)
- `bashTimeout` constant → removed (bash now uses context)
- `executeBash` → moved to handler_bash.go
- `parseBashCommand` → superseded by `parseBashArgs` in handler_bash.go
- `dispatchStore` → superseded by StoreHandler
- `handleMemoryUpsert` → moved to handler_store.go as `storeMemoryUpsert`
- `handleMemoryList` → moved to handler_store.go as `storeMemoryList`
- `handleMemorySearch` → moved to handler_store.go as `storeMemorySearch`
- `renderMemories` → moved to handler_store.go as `renderMemoryEntries`
- `handleHistoryGet` → moved to handler_store.go as `storeHistoryGet`
- `handleHistorySearch` → moved to handler_store.go as `storeHistorySearch`

Also remove the `"time"` import from tools.go (was used by `bashTimeout`).

- [ ] **Step 2: Build**

```bash
go build ./...
```

Fix any remaining references.

- [ ] **Step 3: Run full test suite**

```bash
go test ./internal/chat/...
```

Expected: all PASS.

- [ ] **Step 4: Run the broader test suite**

```bash
go test ./...
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/tools.go
git commit -m "refactor(chat): remove functions extracted to handler files from tools.go"
```

---

## Task 10: Fix Panic Recovery in turn.go

**Files:**
- Modify: `internal/chat/turn.go` (lines 84–90)

Add `runtime/debug` stack capture to the existing panic recovery defer.

- [ ] **Step 1: Add import and update defer**

Add `"runtime/debug"` to turn.go's import block.

Replace the existing panic recovery defer (around lines 84–90):

```go
// Before:
defer func() {
    if r := recover(); r != nil {
        err := fmt.Errorf("panic: %v", r)
        cfg.Hooks.OnError(ctx, err)
        ch <- patchapp.TurnErrEvent{Err: err}
    }
}()

// After:
defer func() {
    if r := recover(); r != nil {
        stack := debug.Stack()
        err := fmt.Errorf("panic: %v\n%s", r, stack)
        cfg.Hooks.OnError(ctx, err)
        ch <- patchapp.TurnErrEvent{Err: err}
    }
}()
```

- [ ] **Step 2: Build and test**

```bash
go build ./... && go test ./internal/chat/...
```

Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/chat/turn.go
git commit -m "fix(chat): capture stack trace in panic recovery"
```

---

## Task 11: Add Intentional-Ignore Comments to openai Adapter

**Files:**
- Modify: `internal/adapters/openai/client.go` (lines 35, 49)

Minimal touch: add comments clarifying that the `io.ReadAll` errors are intentionally discarded in a diagnostic-only code path.

- [ ] **Step 1: Update lines 35 and 49**

Line 35 (inside `RoundTrip`, request body buffering):
```go
// Before:
buf, _ := io.ReadAll(req.Body)

// After:
buf, _ := io.ReadAll(req.Body) // err ignored: empty buf is acceptable for diagnostics
```

Line 49 (inside `RoundTrip`, error response body):
```go
// Before:
buf, _ := io.ReadAll(res.Body)

// After:
buf, _ := io.ReadAll(res.Body) // err ignored: empty buf is acceptable for diagnostics
```

- [ ] **Step 2: Build and test**

```bash
go build ./... && go test ./...
```

Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/openai/client.go
git commit -m "chore(openai): document intentional io.ReadAll error ignores"
```

---

## Task 12: Final Verification and Cleanup

- [ ] **Step 1: Run the full test suite including integration tests**

```bash
go test ./... -count=1
```

Expected: all PASS. `-count=1` bypasses the cache to force a real run.

- [ ] **Step 2: Check for any remaining references to deleted functions**

```bash
grep -rn "executeBash\|parseBashCommand\|dispatchStore\|handleMemoryUpsert\|handleMemoryList\|handleMemorySearch\|renderMemories\|handleHistoryGet\|handleHistorySearch\|handlePruneToolResult\|pruneToolResultByID\|bashTimeout" internal/
```

Expected: no matches. If any appear, they are callers that need updating.

- [ ] **Step 3: Check for any remaining references to old Config in runTurn callers**

```bash
grep -n "runTurn" internal/chat/*.go
```

Expected: only definition (turn.go) and two call sites (RunInteractive, RunOnce in chat.go), all passing `TurnConfig`.

- [ ] **Step 4: Run linter**

```bash
go vet ./...
```

Expected: no issues.

- [ ] **Step 5: Verify handler coverage by listing all handler names registered in NewHandlers**

After NewHandlers runs, the map should contain exactly: `bash`, `edit_file`, `prune_tool_result`, `shell3_docs`, `memory_upsert`, `memory_list`, `memory_search`, `history_get`, `history_search`. Cross-check against `persona.go` built-in tool definitions:

```bash
grep 'Name:' internal/persona/persona.go | grep -v '//'
```

Confirm every tool name defined in persona.go has either a handler in the map, is `compact_history` (special case), or is `shell_interactive` (special case).

- [ ] **Step 6: Final commit if any cleanup was needed**

```bash
git add -p && git commit -m "chore(chat): final cleanup after phase 1 structural refactor"
```
