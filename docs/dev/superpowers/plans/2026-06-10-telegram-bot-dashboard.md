# Telegram bot + Mini App dashboard — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a personal Telegram bot front-end (control + push + tool approvals) and a read-only Telegram Mini App dashboard (rich conversation observability) over `pkg/shell3.Runtime`, with zero changes to the chat/runtime engine.

**Architecture:** A new `shell3 telegram` cobra subcommand builds a `shell3.Runtime` with one persistent session and drives it from a long-poll loop. The bot's *testable core* depends on a small `tgClient` interface (faked in tests); a thin adapter wraps `github.com/go-telegram/bot`. The dashboard is a read-only `net/http` server, `initData`-authed, rendering `Session.History()`/`Snapshot()` + a per-session JSONL tail, exposed via Tailscale. Config (`shell3.telegram{...}`) is parsed by `luacfg` and threaded through `agentsetup` → `pkg/shell3` → the subcommand. **Lua is king** (policy in Lua), **don't reinvent the wheel** (maintained libs for the Bot API, `initData`, cron-free here).

**Tech Stack:** Go, cobra, `github.com/go-telegram/bot`, `github.com/telegram-mini-apps/init-data-golang`, `gopher-lua` (existing), `net/http` + SSE, optional `tailscale.com/tsnet`.

**Source of truth for signatures:** `docs/dev/superpowers/specs/2026-06-10-telegram-bot-dashboard-design.md` and the pkg/shell3 API (see types inline below — they are copied verbatim from the current code).

---

## Task 0: Spike — confirm Mini App loads a `.ts.net` URL (GATE)

**Not code. Do this first; it can invalidate the hosting assumption cheaply.**

- [ ] **Step 1:** Install Tailscale on the dev machine and the phone running Telegram; bring both up on the same tailnet. Run a throwaway static server: `python3 -m http.server 8765` then `tailscale serve https / proxy 127.0.0.1:8765`. Note the `https://<host>.<tailnet>.ts.net/` URL.
- [ ] **Step 2:** In @BotFather, create a test bot, set its menu button Web App URL to that `.ts.net` URL (`/setmenubutton` or via Bot API `setChatMenuButton`).
- [ ] **Step 3:** Open the bot on the phone, tap the menu button. **Expected:** the page loads inside Telegram's webview.
  - If it loads → proceed with Tailscale Serve as designed.
  - If it does NOT load → fall back to Tailscale **Funnel** (public `.ts.net`; `initData` becomes sole auth) and note the change in Task 9's hosting comment. The dashboard code is identical either way.
- [ ] **Step 4:** Record the outcome in the plan PR description. Do not start Task 9 until this is known.

---

## Task 1: `luacfg` — parse `shell3.telegram{...}`

**Files:**
- Modify: `internal/luacfg/luacfg.go` (add `Telegram` field + getter on `LoadedConfig`)
- Modify: `internal/luacfg/register.go` (add `luaTelegram` + register it)
- Create: `internal/luacfg/telegram_test.go`

Config shape (a Lua **function call**, like `shell3.model`):
```lua
shell3.telegram({
  token   = shell3.env.secret("TELEGRAM_BOT_TOKEN"),
  chat_id = "123456789",
  dashboard = { enabled = true, addr = "127.0.0.1:8765", url = "https://host.tailnet.ts.net/" },
})
```

- [ ] **Step 1: Write the failing test**

```go
// internal/luacfg/telegram_test.go
package luacfg

import "testing"

func TestLoadTelegram(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.telegram({
  token = "bot-token",
  chat_id = "123456789",
  dashboard = { enabled = true, addr = "127.0.0.1:8765", url = "https://h.ts.net/" },
})
shell3.agent({ name="a", model="main", prompt="hi", tools={} })
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tg := c.Telegram()
	if tg.Token != "bot-token" || tg.ChatID != "123456789" {
		t.Fatalf("bad telegram: %+v", tg)
	}
	if !tg.Dashboard.Enabled || tg.Dashboard.Addr != "127.0.0.1:8765" || tg.Dashboard.URL != "https://h.ts.net/" {
		t.Fatalf("bad dashboard: %+v", tg.Dashboard)
	}
}

func TestLoadTelegramUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.telegram({ token="x", chat_id="1", nope=true })
shell3.agent({ name="a", model="main", prompt="hi", tools={} })
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), `unknown key "nope"`) {
		t.Fatalf("wrong error: %v", err)
	}
}
```
Add `import "strings"` to the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/luacfg/ -run TestLoadTelegram -v`
Expected: FAIL — `c.Telegram undefined` (compile error).

- [ ] **Step 3: Add the types + getter** in `internal/luacfg/luacfg.go`

Add near the other config types:
```go
// TelegramConfig is the parsed shell3.telegram{...} block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	Dashboard DashboardConfig
}

type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
}
```
Add a field to `LoadedConfig` (the struct at luacfg.go:86):
```go
	telegram TelegramConfig
```
Add a getter (mirrors `Model`):
```go
// Telegram returns the parsed shell3.telegram{} block (zero value if absent).
func (c *LoadedConfig) Telegram() TelegramConfig { return c.telegram }
```

- [ ] **Step 4: Add the parser + registration** in `internal/luacfg/register.go`

Add the key allowlists and parser (mirror `luaModel` at register.go:33):
```go
var telegramKeys = map[string]bool{"token": true, "chat_id": true, "dashboard": true}
var telegramDashboardKeys = map[string]bool{"enabled": true, "addr": true, "url": true}

func (c *LoadedConfig) luaTelegram(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "telegram", telegramKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	tg := TelegramConfig{
		Token:  optStr(opts, "token"),
		ChatID: optStr(opts, "chat_id"),
	}
	if d, ok := opts.RawGetString("dashboard").(*lua.LTable); ok {
		if err := checkKeys(d, "telegram.dashboard", telegramDashboardKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		tg.Dashboard = DashboardConfig{
			Enabled: optBool(d, "enabled"),
			Addr:    optStr(d, "addr"),
			URL:     optStr(d, "url"),
		}
	}
	c.telegram = tg
	return 0
}
```
Register it where the other `shell3.*` functions are set (in `registerShell3`, register.go:5 — find the `L.SetField(tbl, "model", ...)` line and add beside it):
```go
	L.SetField(tbl, "telegram", L.NewFunction(c.luaTelegram))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/luacfg/ -run TestLoadTelegram -v`
Expected: PASS (both).

- [ ] **Step 6: Commit**

```bash
git add internal/luacfg/luacfg.go internal/luacfg/register.go internal/luacfg/telegram_test.go
git commit -m "feat(luacfg): parse shell3.telegram{} config block"
```

---

## Task 2: Thread telegram config through `agentsetup` → `pkg/shell3`

**Files:**
- Modify: `internal/agentsetup/agentsetup.go` (add `Parts.Telegram()` accessor)
- Modify: `pkg/shell3/runtime.go` (capture config on `Runtime`, add `Runtime.Telegram()` + re-exported types)
- Create: `pkg/shell3/telegram_config_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/shell3/telegram_config_test.go
package shell3_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestRuntime_TelegramConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
shell3.telegram({ token="bot-token", chat_id="42", dashboard={ enabled=true, addr="127.0.0.1:8765", url="https://h.ts.net/" } })
`
	path := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	tg := rt.Telegram()
	if tg.Token != "bot-token" || tg.ChatID != "42" || !tg.Dashboard.Enabled {
		t.Fatalf("bad telegram config: %+v", tg)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/shell3/ -run TestRuntime_TelegramConfig -v`
Expected: FAIL — `rt.Telegram undefined`.

- [ ] **Step 3: Add the `Parts` accessor** in `internal/agentsetup/agentsetup.go`

`Parts` already holds `lc *luacfg.LoadedConfig` (agentsetup.go:55). Add beside the other accessors (e.g. near `AgentNames()`):
```go
// Telegram returns the parsed shell3.telegram{} config (zero value if absent).
func (p *Parts) Telegram() luacfg.TelegramConfig { return p.lc.Telegram() }
```

- [ ] **Step 4: Capture + expose on `Runtime`** in `pkg/shell3/runtime.go`

Re-export the config types so consumers don't import `internal/*`:
```go
// TelegramConfig mirrors the parsed shell3.telegram{} block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	Dashboard DashboardConfig
}
type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
}
```
Add a field to the `Runtime` struct:
```go
	telegram TelegramConfig
```
In `NewRuntime` (runtime.go:120), after `BuildParts` returns `parts`, capture it (convert from the luacfg type):
```go
	tg := parts.Telegram()
	rt.telegram = TelegramConfig{
		Token:  tg.Token,
		ChatID: tg.ChatID,
		Dashboard: DashboardConfig{
			Enabled: tg.Dashboard.Enabled,
			Addr:    tg.Dashboard.Addr,
			URL:     tg.Dashboard.URL,
		},
	}
```
(Place this where `rt` is assembled; if `rt` is built as a struct literal, add `telegram:` there instead.) Add the accessor:
```go
// Telegram returns the parsed shell3.telegram{} config (zero value if absent).
func (rt *Runtime) Telegram() TelegramConfig { return rt.telegram }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/shell3/ -run TestRuntime_TelegramConfig -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agentsetup/agentsetup.go pkg/shell3/runtime.go pkg/shell3/telegram_config_test.go
git commit -m "feat(pkg): surface telegram config via Runtime.Telegram()"
```

---

## Task 3: `internal/telegram` — transport interface + Bot skeleton

**Files:**
- Create: `internal/telegram/client.go` (the `tgClient` interface + concrete types it uses)
- Create: `internal/telegram/bot.go` (the `Bot` struct + constructor)
- Create: `internal/telegram/fake_test.go` (a fake client for tests)

The interface isolates the bot logic from `go-telegram/bot` so it is unit-testable. Keep it minimal — only what the bot uses.

- [ ] **Step 1: Define the transport interface** in `internal/telegram/client.go`

```go
//go:build unix

package telegram

import "context"

// Msg is an inbound message, normalized from a Telegram update.
type Msg struct {
	ChatID   int64
	Text     string
	Media    []Media // photos/voice/documents already resolved to bytes
	Callback *Callback
}

// Media is a downloaded attachment.
type Media struct {
	Bytes []byte
	MIME  string // e.g. "image/jpeg", "audio/ogg"
}

// Callback is an inline-button press.
type Callback struct {
	ID    string // callback query id (answer it to stop the spinner)
	Data  string // opaque payload we set on the button
	MsgID int    // message the buttons were attached to (to edit it)
}

// Button is one inline keyboard button.
type Button struct {
	Text string
	Data string
}

// tgClient is the transport surface the Bot depends on. The real impl wraps
// github.com/go-telegram/bot; tests inject a fake.
type tgClient interface {
	// Updates delivers normalized inbound messages until ctx is cancelled.
	Updates(ctx context.Context) <-chan Msg
	// Send posts text; returns the sent message id. buttons optional (one row).
	Send(ctx context.Context, chatID int64, text string, buttons []Button) (msgID int, err error)
	// EditText replaces a message's text and clears its buttons.
	EditText(ctx context.Context, chatID int64, msgID int, text string) error
	// Typing shows the "typing…" chat action.
	Typing(ctx context.Context, chatID int64) error
	// AnswerCallback acknowledges a button press (stops the client spinner).
	AnswerCallback(ctx context.Context, callbackID string) error
}
```

- [ ] **Step 2: Define the Bot struct** in `internal/telegram/bot.go`

```go
//go:build unix

package telegram

import (
	"context"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// Bot routes one Telegram chat to one shell3 Session.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	sess   *shell3.Session
	chatID int64 // the single allowed chat

	approvals *approvalRegistry // Task 6
}

// NewBot wires a Bot. sess must be the runtime's persistent "telegram" session.
func NewBot(client tgClient, rt *shell3.Runtime, sess *shell3.Session, chatID int64) *Bot {
	return &Bot{
		client:    client,
		rt:        rt,
		sess:      sess,
		chatID:    chatID,
		approvals: newApprovalRegistry(),
	}
}
```
> Note: `approvalRegistry` is defined in Task 6. To keep this task compiling, temporarily stub it: add `type approvalRegistry struct{}` and `func newApprovalRegistry() *approvalRegistry { return &approvalRegistry{} }` in `bot.go`; Task 6 replaces the stub with the real type in its own file and removes these lines.

- [ ] **Step 3: Write a fake client** in `internal/telegram/fake_test.go`

```go
//go:build unix

package telegram

import (
	"context"
	"sync"
)

type fakeClient struct {
	in   chan Msg
	mu   sync.Mutex
	sent []sentMsg
	next int
}

type sentMsg struct {
	chatID  int64
	text    string
	buttons []Button
	edited  bool
}

func newFakeClient() *fakeClient { return &fakeClient{in: make(chan Msg, 16)} }

func (f *fakeClient) Updates(ctx context.Context) <-chan Msg { return f.in }

func (f *fakeClient) Send(ctx context.Context, chatID int64, text string, buttons []Button) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.sent = append(f.sent, sentMsg{chatID: chatID, text: text, buttons: buttons})
	return f.next, nil
}
func (f *fakeClient) EditText(ctx context.Context, chatID int64, msgID int, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMsg{chatID: chatID, text: text, edited: true})
	return nil
}
func (f *fakeClient) Typing(ctx context.Context, chatID int64) error          { return nil }
func (f *fakeClient) AnswerCallback(ctx context.Context, callbackID string) error { return nil }

func (f *fakeClient) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, m := range f.sent {
		out[i] = m.text
	}
	return out
}
```

- [ ] **Step 4: Verify it compiles**

Run: `go build ./internal/telegram/ && go vet ./internal/telegram/`
Expected: no output (success).

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/client.go internal/telegram/bot.go internal/telegram/fake_test.go
git commit -m "feat(telegram): transport interface + Bot skeleton + fake client"
```

---

## Task 4: Inbound text routing (idle→Send, busy→Interject, chunked replies)

**Files:**
- Create: `internal/telegram/render.go` (output: drain events, chunk, send)
- Modify: `internal/telegram/bot.go` (add `Run` + `handleMsg`)
- Create: `internal/telegram/routing_test.go`

Key contract (from the API): one turn at a time; **channel close is end-of-turn**; `Send` returns `ErrBusy` as a single `Error` event; `Interject` never blocks and auto-wakes when idle. So: if a turn is already running, route to `Interject`; else `SendParts` and render.

- [ ] **Step 1: Write the failing tests**

```go
// internal/telegram/routing_test.go
//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHandleMsg_IdleSendsReply(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "hello from agent") // helper in runtime_fake_test.go (Step 3)
	b := NewBot(fc, rt, sess, 42)

	ctx := context.Background()
	b.handleMsg(ctx, Msg{ChatID: 42, Text: "hi"})

	got := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(got, "hello from agent") {
		t.Fatalf("expected agent reply, got: %q", got)
	}
}

func TestHandleMsg_WrongChatDropped(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "should not run")
	b := NewBot(fc, rt, sess, 42)

	b.handleMsg(context.Background(), Msg{ChatID: 999, Text: "hi"})

	if len(fc.sentTexts()) != 0 {
		t.Fatalf("expected no output for unauthorized chat, got %v", fc.sentTexts())
	}
}

func TestChunk_SplitsAt4096(t *testing.T) {
	long := strings.Repeat("a", 5000)
	chunks := chunk(long, 4096)
	if len(chunks) != 2 || len(chunks[0]) > 4096 {
		t.Fatalf("bad chunking: %d chunks, first len %d", len(chunks), len(chunks[0]))
	}
}

var _ = time.Second // keep import if unused after edits
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/telegram/ -run 'TestHandleMsg|TestChunk' -v`
Expected: FAIL — `handleMsg`, `chunk`, `newFakeRuntime` undefined.

- [ ] **Step 3: Add a fake-runtime test helper** `internal/telegram/runtime_fake_test.go`

```go
//go:build unix

package telegram

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// newFakeRuntime builds a real Runtime backed by a config whose model is a
// stub that always replies with replyText. Uses the scaffold's fakellm path is
// not available here, so we point at a tiny echo model via the test config.
//
// NOTE: pkg/shell3 has no public fakellm injection. For these tests we load a
// minimal real config and rely on the *_test.go fakellm helpers exposed in
// pkg/shell3's own tests being unavailable — so instead drive the bot with a
// Runtime whose session we pre-seed. If a public test seam is missing, add a
// `shell3.NewRuntimeForTest(cfg)` export in pkg/shell3 (see Step 3a).
func newFakeRuntime(t *testing.T, replyText string) (*shell3.Runtime, *shell3.Session) {
	t.Helper()
	rt := shell3.NewRuntimeForTest(t, replyText) // Step 3a
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	_ = os.Stat
	_ = filepath.Join
	return rt, sess
}
```

- [ ] **Step 3a: Add a public test seam** in `pkg/shell3` (`pkg/shell3/testseam.go`)

`internal/telegram` can't reach the unexported `newTestRuntime`. Export a tiny, clearly-named test helper guarded so it's obviously test-only:
```go
package shell3

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// NewRuntimeForTest builds a Runtime whose model always streams replyText.
// For tests in other packages only.
func NewRuntimeForTest(t *testing.T, replyText string) *Runtime {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			return chat.Config{
				LLM: fakellm.New(
					fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
					fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
				),
				ModeLabel: "code",
				Headless:  o.Headless,
			}, nil
		},
		events:   make(chan HostEvent, 64),
		workDir:  t.TempDir(),
		ctx:      ctx,
		cancel:   cancel,
		cleanup:  func() {},
		sessions: map[string]*Session{},
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}
```
> If `Runtime`'s field set differs, copy it from `newTestRuntime` (runtime_test.go:17) verbatim — that helper is the canonical shape. Keep this file un-tagged (no `//go:build`) so it compiles in normal builds but is only referenced by tests.

- [ ] **Step 4: Implement output rendering** `internal/telegram/render.go`

```go
//go:build unix

package telegram

import (
	"context"
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

const tgMaxMessage = 4096

// drainToReply consumes a turn's event channel and returns the assistant text.
// Channel close is the authoritative end-of-turn signal.
func drainToReply(ch <-chan shell3.Event) string {
	var b strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case shell3.Token:
			b.WriteString(ev.Text)
		case shell3.Error:
			if ev.Err != nil {
				b.WriteString("\n⚠️ " + ev.Err.Error())
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// chunk splits s into pieces no longer than max bytes, preferring newline
// boundaries.
func chunk(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var out []string
	for len(s) > max {
		cut := strings.LastIndex(s[:max], "\n")
		if cut <= 0 {
			cut = max
		}
		out = append(out, s[:cut])
		s = strings.TrimPrefix(s[cut:], "\n")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// sendReply posts text to the chat, chunked.
func (b *Bot) sendReply(ctx context.Context, text string) {
	if text == "" {
		text = "(no output)"
	}
	for _, c := range chunk(text, tgMaxMessage) {
		_, _ = b.client.Send(ctx, b.chatID, c, nil)
	}
}
```

- [ ] **Step 5: Implement routing** in `internal/telegram/bot.go`

```go
// Run consumes inbound messages and the wake bus until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	go b.consumeWakes(ctx) // Task 7
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-b.client.Updates(ctx):
			if !ok {
				return
			}
			b.handleMsg(ctx, m)
		}
	}
}

// handleMsg routes one inbound message.
func (b *Bot) handleMsg(ctx context.Context, m Msg) {
	if m.ChatID != b.chatID {
		return // unauthorized: drop silently
	}
	if m.Callback != nil {
		b.handleCallback(ctx, m.Callback) // Task 6
		return
	}
	if strings.HasPrefix(m.Text, "/") {
		b.handleCommand(ctx, m) // Task 8
		return
	}
	parts := mediaToParts(m.Media) // Task 5
	if b.sess.HasQueuedInput() {
		// A turn may be running; Interject never blocks and steers it.
		b.sess.Interject(m.Text, parts...)
		return
	}
	_ = b.client.Typing(ctx, b.chatID)
	ch := b.sess.SendParts(ctx, m.Text, parts)
	reply := drainToReply(ch)
	b.sendReply(ctx, reply)
}
```
Add imports `"context"` and `"strings"` to bot.go. Add a temporary stub `func (b *Bot) handleCallback(context.Context, *Callback) {}`, `func (b *Bot) handleCommand(context.Context, Msg) {}`, and `func mediaToParts([]Media) []shell3.Part { return nil }` so this compiles; later tasks replace them (delete the stub when the real one lands).

> **Busy detection nuance:** `HasQueuedInput()` reports inbox state, not "a turn is running." For v1 the personal single-chat flow is effectively serial (one user, messages handled one at a time in `handleMsg`), so a running turn blocks `handleMsg` until its channel drains. The `HasQueuedInput` check catches the case where a wake/cron item is already queued. This is sufficient for v1; note it in a comment.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/telegram/ -run 'TestHandleMsg|TestChunk' -v` and `go test ./pkg/shell3/ -run TestRuntime -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/telegram/ pkg/shell3/testseam.go
git commit -m "feat(telegram): inbound routing (idle Send, busy Interject) + chunked replies"
```

---

## Task 5: Media — Telegram attachments → `shell3.Part`

**Files:**
- Create: `internal/telegram/media.go`
- Modify: `internal/telegram/bot.go` (remove the `mediaToParts` stub)
- Create: `internal/telegram/media_test.go`

`shell3.Part` is filled directly (no constructors): `Part{Kind: PartImage|PartAudio, Data: bytes, MIME: "image/png"}`. Caps: 10 MB image, 25 MB audio; images re-encoded to JPEG by the engine; audio must be wav/mp3 (Telegram voice is OGG/Opus — see note).

- [ ] **Step 1: Write the failing test**

```go
// internal/telegram/media_test.go
//go:build unix

package telegram

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestMediaToParts_ImageAndAudio(t *testing.T) {
	in := []Media{
		{Bytes: []byte("\xff\xd8\xff"), MIME: "image/jpeg"},
		{Bytes: []byte("RIFF"), MIME: "audio/wav"},
	}
	parts := mediaToParts(in)
	if len(parts) != 2 {
		t.Fatalf("want 2 parts, got %d", len(parts))
	}
	if parts[0].Kind != shell3.PartImage || parts[0].MIME != "image/jpeg" {
		t.Fatalf("bad image part: %+v", parts[0])
	}
	if parts[1].Kind != shell3.PartAudio {
		t.Fatalf("bad audio part: %+v", parts[1])
	}
}

func TestMediaToParts_UnsupportedDropped(t *testing.T) {
	parts := mediaToParts([]Media{{Bytes: []byte("x"), MIME: "application/pdf"}})
	if len(parts) != 0 {
		t.Fatalf("want 0 parts for unsupported mime, got %d", len(parts))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/telegram/ -run TestMediaToParts -v`
Expected: FAIL — the stub returns nil so the first test fails on count.

- [ ] **Step 3: Implement** `internal/telegram/media.go`

```go
//go:build unix

package telegram

import (
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// mediaToParts converts resolved Telegram attachments to shell3 parts,
// dropping anything the engine can't ingest (images/audio only).
func mediaToParts(media []Media) []shell3.Part {
	var parts []shell3.Part
	for _, m := range media {
		switch {
		case strings.HasPrefix(m.MIME, "image/"):
			parts = append(parts, shell3.Part{Kind: shell3.PartImage, Data: m.Bytes, MIME: m.MIME})
		case m.MIME == "audio/wav", m.MIME == "audio/x-wav", m.MIME == "audio/mpeg", m.MIME == "audio/mp3":
			parts = append(parts, shell3.Part{Kind: shell3.PartAudio, Data: m.Bytes, MIME: m.MIME})
		default:
			// unsupported (e.g. OGG voice, PDF) — drop. See note re: voice transcoding.
		}
	}
	return parts
}
```
Remove the `mediaToParts` stub from `bot.go`.

> **Voice note (document, don't silently drop later):** Telegram voice messages are OGG/Opus, which the engine's audio loader (wav/mp3 only) rejects. v1 drops them. A future task can transcode OGG→wav via an `ffmpeg` shell-out before building the Part; note this limitation in the changelog so it isn't a silent cap.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/telegram/ -run TestMediaToParts -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/media.go internal/telegram/media_test.go internal/telegram/bot.go
git commit -m "feat(telegram): map image/audio attachments to shell3 parts"
```

---

## Task 6: Inline-button tool approvals

**Files:**
- Create: `internal/telegram/approval.go`
- Modify: `internal/telegram/bot.go` (remove approval stubs; wire `SetApprover`; real `handleCallback`)
- Create: `internal/telegram/approval_test.go`

Flow: the engine calls the approver (`SetApprover` fn) on a tool ask-guard. The approver sends a message with Approve/Deny buttons, registers a pending channel keyed by an id, and blocks until the callback handler resolves it or a timeout fires (→ deny). The callback data is `"ap:<id>:y"` / `"ap:<id>:n"`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/telegram/approval_test.go
//go:build unix

package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestApprover_ApproveResolvesTrue(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.approvalTimeout = time.Minute

	done := make(chan bool, 1)
	go func() {
		done <- b.approve(context.Background(), shell3.ApprovalRequest{Tool: "bash", RawArgs: `{"cmd":"rm x"}`})
	}()

	// the approver should have sent a prompt with two buttons
	waitFor(t, func() bool { return len(fc.lastButtons()) == 2 })
	id := buttonID(fc.lastButtons()[0].Data) // "ap:<id>:y" → "<id>"
	b.handleCallback(context.Background(), &Callback{ID: "cq1", Data: "ap:" + id + ":y", MsgID: 1})

	select {
	case got := <-done:
		if !got {
			t.Fatal("expected approve=true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approver did not resolve")
	}
}

func TestApprover_TimeoutDenies(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.approvalTimeout = 50 * time.Millisecond

	got := b.approve(context.Background(), shell3.ApprovalRequest{Tool: "bash"})
	if got {
		t.Fatal("expected timeout → deny")
	}
}
```
Add small test helpers to `fake_test.go`: `lastButtons()` (returns the buttons of the last sent msg) and to `approval_test.go`: `waitFor(t, cond)` (poll up to 1s) and `buttonID(data)` (`strings.Split(data, ":")[1]`).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/telegram/ -run TestApprover -v`
Expected: FAIL — `approve`, `approvalTimeout`, real registry undefined.

- [ ] **Step 3: Implement the registry + approver** `internal/telegram/approval.go`

```go
//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/pkg/shell3"
)

type approvalRegistry struct {
	mu      sync.Mutex
	pending map[string]chan bool
	seq     int
}

func newApprovalRegistry() *approvalRegistry {
	return &approvalRegistry{pending: map[string]chan bool{}}
}

func (r *approvalRegistry) add() (id string, ch chan bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	id = fmt.Sprintf("%d", r.seq)
	ch = make(chan bool, 1)
	r.pending[id] = ch
	return id, ch
}

func (r *approvalRegistry) resolve(id string, ok bool) {
	r.mu.Lock()
	ch := r.pending[id]
	delete(r.pending, id)
	r.mu.Unlock()
	if ch != nil {
		ch <- ok
	}
}

// approve is the SetApprover hook. It blocks until a button press or timeout.
func (b *Bot) approve(ctx context.Context, req shell3.ApprovalRequest) bool {
	id, ch := b.approvals.add()
	text := fmt.Sprintf("🔐 Approve tool *%s*?\n`%s`", req.Tool, truncate(req.RawArgs, 300))
	if req.Reason != "" {
		text += "\n_" + req.Reason + "_"
	}
	buttons := []Button{
		{Text: "✅ Approve", Data: "ap:" + id + ":y"},
		{Text: "🚫 Deny", Data: "ap:" + id + ":n"},
	}
	msgID, _ := b.client.Send(ctx, b.chatID, text, buttons)

	timeout := b.approvalTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	select {
	case ok := <-ch:
		_ = b.client.EditText(ctx, b.chatID, msgID, decisionText(req.Tool, ok))
		return ok
	case <-time.After(timeout):
		b.approvals.resolve(id, false) // clean up the map entry
		_ = b.client.EditText(ctx, b.chatID, msgID, "⏱ denied (timeout): "+req.Tool)
		return false
	case <-ctx.Done():
		b.approvals.resolve(id, false)
		return false
	}
}

// handleCallback resolves a pending approval from a button press.
func (b *Bot) handleCallback(ctx context.Context, cb *Callback) {
	_ = b.client.AnswerCallback(ctx, cb.ID)
	parts := strings.Split(cb.Data, ":")
	if len(parts) != 3 || parts[0] != "ap" {
		return
	}
	b.approvals.resolve(parts[1], parts[2] == "y")
}

func decisionText(tool string, ok bool) string {
	if ok {
		return "✅ approved: " + tool
	}
	return "🚫 denied: " + tool
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
```

- [ ] **Step 4: Wire it into the Bot** in `bot.go`

Remove the `approvalRegistry` stub and the `handleCallback` stub from bot.go (now in approval.go). Add the timeout field and register the approver:
```go
type Bot struct {
	// ...existing fields...
	approvalTimeout time.Duration
}
```
In `NewBot`, register the approver on the session before returning:
```go
	b := &Bot{ /* ...as before... */ }
	_ = sess.SetApprover(b.approve)
	return b
```
Add `"time"` to bot.go imports.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/telegram/ -run TestApprover -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/telegram/approval.go internal/telegram/approval_test.go internal/telegram/bot.go internal/telegram/fake_test.go
git commit -m "feat(telegram): inline-button tool approvals with timeout-deny"
```

---

## Task 7: Wake bus → unprompted push

**Files:**
- Modify: `internal/telegram/bot.go` (real `consumeWakes`)
- Create: `internal/telegram/wake_test.go`

When a subagent result (or a cron job, Spec B) lands in the inbox on an idle session, `rt.Events()` emits a `Wake` for that session. The bot runs `RunQueued` and pushes the result.

- [ ] **Step 1: Write the failing test**

```go
// internal/telegram/wake_test.go
//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConsumeWakes_PushesResult(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "woke up and ran")
	b := NewBot(fc, rt, sess, 42)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	// Interject on an idle session queues input and emits a Wake.
	sess.Interject("scheduled job result")

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "woke up and ran")
	})
	_ = time.Now
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/telegram/ -run TestConsumeWakes -v`
Expected: FAIL — the stub `consumeWakes` does nothing.

- [ ] **Step 3: Implement** in `bot.go` (replace the Task 4 reference)

```go
// consumeWakes pushes results when the session wakes (subagent/cron results).
func (b *Bot) consumeWakes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.rt.Events():
			if !ok {
				return
			}
			if ev.Kind != shell3.Wake || ev.Session != b.sess.Name() {
				continue
			}
			reply := drainToReply(b.sess.RunQueued(ctx))
			if reply != "" {
				b.sendReply(ctx, reply)
			}
		}
	}
}
```
Ensure `shell3` is imported in bot.go.

> **Single-consumer note:** `rt.Events()` is one channel; the bot is its only consumer here (single session). If a future front-end shares the Runtime, route by `ev.Session`. Documented so the assumption is explicit.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/telegram/ -run TestConsumeWakes -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/bot.go internal/telegram/wake_test.go
git commit -m "feat(telegram): consume wake bus, push subagent/cron results"
```

---

## Task 8: Bot commands

**Files:**
- Create: `internal/telegram/commands.go`
- Modify: `internal/telegram/bot.go` (remove `handleCommand` stub)
- Create: `internal/telegram/commands_test.go`

Commands map to Session methods. `/stop` cancels the in-flight turn — track a per-turn cancel.

- [ ] **Step 1: Write the failing tests**

```go
// internal/telegram/commands_test.go
//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestCommand_Agents(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/agents"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "code") {
		t.Fatalf("expected agent list, got %v", fc.sentTexts())
	}
}

func TestCommand_Clear(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/clear"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "cleared") {
		t.Fatalf("expected clear ack, got %v", fc.sentTexts())
	}
}

func TestCommand_Dash(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.dashURL = "https://h.ts.net/"
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/dash"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "h.ts.net") {
		t.Fatalf("expected dashboard link, got %v", fc.sentTexts())
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/telegram/ -run TestCommand -v`
Expected: FAIL — real `handleCommand`/`dashURL` undefined.

- [ ] **Step 3: Implement** `internal/telegram/commands.go`

```go
//go:build unix

package telegram

import (
	"context"
	"strings"
)

func (b *Bot) handleCommand(ctx context.Context, m Msg) {
	fields := strings.Fields(m.Text)
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(m.Text, cmd))
	switch cmd {
	case "/clear":
		if err := b.sess.Clear(); err != nil {
			b.sendReply(ctx, "clear failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "🧹 cleared")
	case "/agents":
		b.sendReply(ctx, "agents: "+strings.Join(b.sess.AgentNames(), ", "))
	case "/agent":
		if err := b.sess.SwitchAgent(arg); err != nil {
			b.sendReply(ctx, "switch failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "🤖 agent → "+b.sess.ActiveAgent())
	case "/set":
		kv := strings.SplitN(arg, " ", 2)
		if len(kv) != 2 {
			b.sendReply(ctx, "usage: /set <name> <value>")
			return
		}
		if err := b.sess.SetParam(kv[0], kv[1]); err != nil {
			b.sendReply(ctx, "set failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "⚙️ "+kv[0]+" = "+kv[1])
	case "/rollback":
		ok, err := b.sess.Rollback()
		if err != nil {
			b.sendReply(ctx, "rollback failed: "+err.Error())
			return
		}
		if !ok {
			b.sendReply(ctx, "nothing to roll back")
			return
		}
		b.sendReply(ctx, "↩️ rolled back")
	case "/stop":
		if c := b.cancelTurn; c != nil {
			c()
			b.sendReply(ctx, "⏹ stopped")
			return
		}
		b.sendReply(ctx, "nothing running")
	case "/dash":
		if b.dashURL == "" {
			b.sendReply(ctx, "dashboard is disabled")
			return
		}
		b.sendReply(ctx, "📊 dashboard: "+b.dashURL)
	default:
		b.sendReply(ctx, "unknown command: "+cmd)
	}
}
```

- [ ] **Step 4: Add fields + cancel tracking** in `bot.go`

Add to `Bot`: `dashURL string` and `cancelTurn context.CancelFunc`. In `handleMsg` (Task 4), wrap the turn ctx so `/stop` works:
```go
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	ch := b.sess.SendParts(turnCtx, m.Text, parts)
	reply := drainToReply(ch)
	b.cancelTurn = nil
	cancel()
	b.sendReply(ctx, reply)
```
Set `dashURL` in `NewBot` from a new param or via a setter; simplest: add `dashURL string` to `NewBot`'s signature. Update `NewBot` and its callers/tests accordingly (tests set `b.dashURL` directly, which still works).

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/telegram/ -run TestCommand -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/telegram/commands.go internal/telegram/commands_test.go internal/telegram/bot.go
git commit -m "feat(telegram): bot commands (/clear /agent /agents /set /rollback /stop /dash)"
```

---

## Task 9: Dashboard server — `initData` auth + history + SSE

**Files:**
- Create: `internal/telegram/web/auth.go` (initData verification via the lib)
- Create: `internal/telegram/web/server.go` (router, history, SSE)
- Create: `internal/telegram/web/auth_test.go`
- Create: `internal/telegram/web/server_test.go`

Add the dependency first: `go get github.com/telegram-mini-apps/init-data-golang@latest`.

- [ ] **Step 1: Write the failing auth test**

```go
// internal/telegram/web/auth_test.go
//go:build unix

package web

import "testing"

func TestVerifyInitData_RejectsTampered(t *testing.T) {
	// A syntactically-valid but unsigned/invalid initData must be rejected.
	if ok, _ := verifyInitData("user=%7B%22id%22%3A42%7D&hash=deadbeef", "bot-token", 42); ok {
		t.Fatal("tampered initData accepted")
	}
}
```
> A positive-path test needs a correctly-signed payload. Use the library's own signing helper if present, or compute the HMAC in the test with the documented algorithm. If neither is convenient, keep only the negative test here and cover the happy path in `server_test.go` by stubbing `verifyInitData` through an interface seam (preferred: a `validator func(string) (int64, bool)` field on the server, defaulting to the real one, overridden in tests).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/telegram/web/ -run TestVerifyInitData -v`
Expected: FAIL — package/func missing.

- [ ] **Step 3: Implement auth** `internal/telegram/web/auth.go`

```go
//go:build unix

package web

import (
	"strconv"
	"time"

	initdata "github.com/telegram-mini-apps/init-data-golang"
)

// verifyInitData validates the Telegram Mini App initData against the bot
// token and returns the embedded user id if it matches wantUser.
func verifyInitData(raw, botToken string, wantUser int64) (bool, int64) {
	if err := initdata.Validate(raw, botToken, time.Hour); err != nil {
		return false, 0
	}
	parsed, err := initdata.Parse(raw)
	if err != nil {
		return false, 0
	}
	uid := parsed.User.ID
	if strconv.FormatInt(uid, 10) != strconv.FormatInt(wantUser, 10) {
		return false, 0
	}
	return true, uid
}
```
> **Verify the library API** (`Validate(raw, token, expIn)` and `Parse(raw).User.ID`) against the installed version's godoc; adjust names if the package's surface differs. This is the one external boundary — confirm signatures, don't guess in the final code.

- [ ] **Step 4: Implement the server** `internal/telegram/web/server.go`

```go
//go:build unix

package web

import (
	"encoding/json"
	"net/http"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// Server is the read-only dashboard.
type Server struct {
	sess     *shell3.Session
	rt       *shell3.Runtime
	token    string
	chatID   int64
	validate func(initData string) (int64, bool) // seam for tests
}

func NewServer(rt *shell3.Runtime, sess *shell3.Session, token string, chatID int64) *Server {
	s := &Server{rt: rt, sess: sess, token: token, chatID: chatID}
	s.validate = func(initData string) (int64, bool) {
		ok, uid := verifyInitData(initData, s.token, s.chatID)
		return uid, ok
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/history", s.auth(s.handleHistory))
	mux.HandleFunc("/api/stream", s.auth(s.handleStream))
	return mux
}

// auth gates an endpoint on valid initData (passed as ?initData= or header).
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Init-Data")
		if raw == "" {
			raw = r.URL.Query().Get("initData")
		}
		if _, ok := s.validate(raw); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

type historyEntry struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	hist := s.sess.History()
	out := make([]historyEntry, len(hist))
	for i, h := range hist {
		out[i] = historyEntry{Role: h.Role, Content: h.Content, ToolName: h.ToolName, ToolCallID: h.ToolCallID}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-s.rt.Events():
			if !ok {
				return
			}
			if ev.Session != s.sess.Name() {
				continue
			}
			b, _ := json.Marshal(map[string]any{"session": ev.Session, "kind": int(ev.Kind)})
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			fl.Flush()
		}
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML) // Task 10
}
```
> **`rt.Events()` is a single shared channel.** The SSE handler and the bot's `consumeWakes` would compete for it if both range it. For v1 (one front-end), have the **bot own `rt.Events()`** and expose a fan-out: add a tiny broadcast hub the bot publishes `HostEvent`s to, and let SSE subscribe. Simpler v1 alternative that avoids the contention entirely: the dashboard polls `/api/history` on an interval and the SSE endpoint streams only a lightweight heartbeat. **Pick the poll-based approach for v1** (note the simplification in the changelog); wire true SSE fan-out in a follow-up. Update this handler to the chosen approach and adjust the test accordingly.

- [ ] **Step 5: Write the server test**

```go
// internal/telegram/web/server_test.go
//go:build unix

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistory_RequiresAuth(t *testing.T) {
	s := &Server{validate: func(string) (int64, bool) { return 0, false }}
	s.sess = nil
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	s.auth(s.handleHistory)(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}
```
> A history-content test needs a real session; reuse the pkg/shell3 test seam pattern (build a Runtime, seed a turn, then `NewServer`). Keep this task's test to the auth gate to avoid cross-package fixtures; cover content rendering in an integration test (Task 11) if desired.

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/telegram/web/ -v`
Expected: PASS (auth tests).

- [ ] **Step 7: Commit**

```bash
git add internal/telegram/web/ go.mod go.sum
git commit -m "feat(telegram/web): read-only dashboard server with initData auth"
```

---

## Task 10: Mini App static page

**Files:**
- Create: `internal/telegram/web/index.go` (embedded HTML/JS via `//go:embed`)
- Create: `internal/telegram/web/static/index.html`

Vanilla HTML + the Telegram WebApp SDK (no build step — "don't reinvent the wheel," no bundler toolchain).

- [ ] **Step 1: Create the page** `internal/telegram/web/static/index.html`

```html
<!doctype html>
<html><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<script src="https://telegram.org/js/telegram-web-app.js"></script>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; margin: 0; padding: 12px;
    background: var(--tg-theme-bg-color, #fff); color: var(--tg-theme-text-color, #000); }
  .msg { padding: 8px 10px; border-radius: 8px; margin: 6px 0; white-space: pre-wrap; }
  .user { background: var(--tg-theme-secondary-bg-color, #eef); }
  .assistant { background: var(--tg-theme-secondary-bg-color, #efe); }
  .tool { font-family: ui-monospace, monospace; font-size: 12px; opacity: .8; }
  pre { overflow-x: auto; background: #00000010; padding: 8px; border-radius: 6px; }
</style></head>
<body>
<div id="log">loading…</div>
<script>
  const tg = window.Telegram.WebApp; tg.ready(); tg.expand();
  const initData = tg.initData || "";
  async function load() {
    const r = await fetch("/api/history", { headers: { "X-Init-Data": initData } });
    if (!r.ok) { document.getElementById("log").textContent = "unauthorized"; return; }
    const hist = await r.json();
    document.getElementById("log").innerHTML = hist.map(h => {
      const cls = h.role === "user" ? "user" : h.role === "assistant" ? "assistant" : "tool";
      const body = h.tool_name ? `<div class="tool">[${h.tool_name}] ${escapeHtml(h.content)}</div>`
                               : `<div>${escapeHtml(h.content)}</div>`;
      return `<div class="msg ${cls}">${body}</div>`;
    }).join("");
    window.scrollTo(0, document.body.scrollHeight);
  }
  function escapeHtml(s){ return (s||"").replace(/[&<>]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;'}[c])); }
  load(); setInterval(load, 4000); // poll (v1; see SSE follow-up)
</script>
</body></html>
```

- [ ] **Step 2: Embed it** `internal/telegram/web/index.go`

```go
//go:build unix

package web

import _ "embed"

//go:embed static/index.html
var indexHTML []byte
```

- [ ] **Step 3: Verify build**

Run: `go build ./internal/telegram/web/`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add internal/telegram/web/index.go internal/telegram/web/static/index.html
git commit -m "feat(telegram/web): Mini App page (themed, polling history view)"
```

---

## Task 11: `shell3 telegram` subcommand + real client adapter + wiring

**Files:**
- Create: `cmd/shell3/telegram.go` (the cobra subcommand)
- Create: `internal/telegram/client_botapi.go` (real `tgClient` over `go-telegram/bot`)
- Modify: `cmd/shell3/main.go` (`root.AddCommand(newTelegramCommand())`)

Add deps: `go get github.com/go-telegram/bot@latest`.

- [ ] **Step 1: Implement the real client adapter** `internal/telegram/client_botapi.go`

```go
//go:build unix

package telegram

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type botAPIClient struct {
	b   *bot.Bot
	out chan Msg
}

// NewBotAPIClient builds the real transport. token from config.
func NewBotAPIClient(ctx context.Context, token string) (*botAPIClient, error) {
	c := &botAPIClient{out: make(chan Msg, 32)}
	b, err := bot.New(token, bot.WithDefaultHandler(c.onUpdate))
	if err != nil {
		return nil, err
	}
	c.b = b
	go b.Start(ctx) // long-polls until ctx cancelled
	return c, nil
}

func (c *botAPIClient) onUpdate(ctx context.Context, b *bot.Bot, u *models.Update) {
	switch {
	case u.CallbackQuery != nil:
		cq := u.CallbackQuery
		c.out <- Msg{
			ChatID:   cq.Message.Message.Chat.ID,
			Callback: &Callback{ID: cq.ID, Data: cq.Data, MsgID: cq.Message.Message.ID},
		}
	case u.Message != nil:
		m := u.Message
		msg := Msg{ChatID: m.Chat.ID, Text: m.Text}
		// Media (photo/voice/document) → download bytes via getFile + FileDownloadLink.
		// See Step 1a; omitted here for brevity but REQUIRED — resolve to msg.Media.
		c.out <- msg
	}
}

func (c *botAPIClient) Updates(ctx context.Context) <-chan Msg { return c.out }

func (c *botAPIClient) Send(ctx context.Context, chatID int64, text string, buttons []Button) (int, error) {
	p := &bot.SendMessageParams{ChatID: chatID, Text: text, ParseMode: models.ParseModeMarkdown}
	if len(buttons) > 0 {
		row := make([]models.InlineKeyboardButton, len(buttons))
		for i, btn := range buttons {
			row[i] = models.InlineKeyboardButton{Text: btn.Text, CallbackData: btn.Data}
		}
		p.ReplyMarkup = models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
	}
	m, err := c.b.SendMessage(ctx, p)
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}

func (c *botAPIClient) EditText(ctx context.Context, chatID int64, msgID int, text string) error {
	_, err := c.b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: text})
	return err
}
func (c *botAPIClient) Typing(ctx context.Context, chatID int64) error {
	_, err := c.b.SendChatAction(ctx, &bot.SendChatActionParams{ChatID: chatID, Action: models.ChatActionTyping})
	return err
}
func (c *botAPIClient) AnswerCallback(ctx context.Context, id string) error {
	_, err := c.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: id})
	return err
}
```
> **Verify against the installed `go-telegram/bot` API** — param struct names (`SendMessageParams`, `EditMessageTextParams`, etc.), `ChatID any`, and the `CallbackQuery.Message` shape vary by version. This adapter is the second external boundary; confirm signatures, don't ship guessed names.

- [ ] **Step 1a: Implement media download** (in `onUpdate`, for `m.Photo`/`m.Voice`/`m.Document`): call `c.b.GetFile(ctx, &bot.GetFileParams{FileID: id})`, then `c.b.FileDownloadLink(file)`, `http.Get` the bytes, set MIME from the file/extension, append to `msg.Media`. Cap downloads (skip files > 25 MB). Keep this in a helper `resolveMedia(ctx, m) []Media`.

- [ ] **Step 2: Implement the subcommand** `cmd/shell3/telegram.go`

```go
//go:build unix

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/telegram"
	"github.com/weatherjean/shell3/internal/telegram/web"
	"github.com/weatherjean/shell3/pkg/shell3"
)

func newTelegramCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Run the personal Telegram bot front-end",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			cwd, _ := os.Getwd()
			rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: configPath, WorkDir: cwd})
			if err != nil {
				return err
			}
			defer rt.Close()

			tg := rt.Telegram()
			if tg.Token == "" || tg.ChatID == "" {
				return fmt.Errorf("no telegram config: add shell3.telegram{ token=..., chat_id=... } to shell3.lua")
			}
			chatID, err := strconv.ParseInt(tg.ChatID, 10, 64)
			if err != nil {
				return fmt.Errorf("telegram chat_id %q is not a number: %w", tg.ChatID, err)
			}

			outPath := ""
			if tg.Dashboard.Enabled {
				outPath = "" // optional per-session JSONL tail; wire if dashboard tails it
			}
			sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", OutPath: outPath})
			if err != nil {
				return err
			}

			client, err := telegram.NewBotAPIClient(ctx, tg.Token)
			if err != nil {
				return err
			}
			b := telegram.NewBot(client, rt, sess, chatID, tg.Dashboard.URL)

			if tg.Dashboard.Enabled && tg.Dashboard.Addr != "" {
				srv := web.NewServer(rt, sess, tg.Token, chatID)
				go func() {
					_ = startDashboard(ctx, tg.Dashboard.Addr, srv.Handler())
				}()
				fmt.Printf("dashboard on %s (expose via: tailscale serve https / proxy %s)\n", tg.Dashboard.Addr, tg.Dashboard.Addr)
			}

			fmt.Printf("shell3 telegram: listening for chat %d\n", chatID)
			b.Run(ctx)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "path to shell3.lua")
	return cmd
}
```
Add a small `startDashboard` helper (an `http.Server` with `BaseContext`/graceful `Shutdown` on ctx cancel) in the same file. Update `NewBot` to accept the `dashURL string` param (Task 8 added the field).

- [ ] **Step 3: Register the subcommand** in `cmd/shell3/main.go`

Beside `root.AddCommand(newBootCommand())`:
```go
	root.AddCommand(newTelegramCommand())
```

- [ ] **Step 4: Build + vet + full test**

Run: `go build ./... && go vet ./... && go test ./... && go test -race ./internal/telegram/...`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/shell3/telegram.go cmd/shell3/main.go internal/telegram/client_botapi.go internal/telegram/bot.go go.mod go.sum
git commit -m "feat(cmd): shell3 telegram subcommand + go-telegram/bot adapter"
```

---

## Task 12: Scaffold + docs

**Files:**
- Modify: `internal/scaffold/defaults/base/shell3.lua.tmpl` (commented `shell3.telegram{}` block)
- Modify: `internal/scaffold/defaults/base/.env` template (add `TELEGRAM_BOT_TOKEN=`)
- Modify: `CHANGELOG.md`
- Modify: `internal/scaffold/scaffold_test.go` if it asserts rendered content

- [ ] **Step 1:** Add to `shell3.lua.tmpl` (commented, so existing configs are unaffected):
```lua
-- Personal Telegram bot (run: `shell3 telegram`). Uncomment + set your chat id.
-- shell3.telegram({
--   token   = shell3.env.secret("TELEGRAM_BOT_TOKEN"),
--   chat_id = "000000000",
--   dashboard = { enabled = true, addr = "127.0.0.1:8765", url = "https://HOST.TAILNET.ts.net/" },
-- })
```

- [ ] **Step 2:** Add `TELEGRAM_BOT_TOKEN=` to the `.env` template (find the template under `internal/scaffold/defaults/`).

- [ ] **Step 3:** Add a CHANGELOG entry summarizing the bot + dashboard, and **explicitly note the v1 limitations** (voice/OGG dropped; dashboard uses polling not SSE; Tailscale exposure is operator-configured) so they aren't silent caps.

- [ ] **Step 4:** Run `go test ./internal/scaffold/...`; fix any rendered-content assertion. If the test renders + loads the config, ensure the commented block doesn't break the loader (it won't — it's commented).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/ CHANGELOG.md
git commit -m "docs(scaffold): telegram config example + .env token + changelog"
```

---

## Self-Review

**Spec coverage** (against `2026-06-10-telegram-bot-dashboard-design.md`):
- Config block (`shell3.telegram`) → Task 1; threading → Task 2. ✓
- Bot: long-poll + chat-id allowlist → Tasks 3,4; typing→final + chunking → Task 4; media → Task 5; inline-button approvals + timeout → Task 6; Wake push → Task 7; commands → Task 8. ✓
- Dashboard: initData auth → Task 9; read-only history + stream → Task 9; Mini App page → Task 10; Tailscale exposure → Task 11 (operator) + Task 0 spike. ✓
- Subcommand + scaffold → Tasks 11,12. ✓
- "Lua is king" (policy in Lua) and "maintained libs" (`go-telegram/bot`, `init-data-golang`) → honored in Tasks 1, 9, 11. ✓

**Known deviations from a naive reading, made explicit:**
- Spec showed `telegram = {...}`; real mechanism is `shell3.telegram({...})` (matches `shell3.model`). Plan uses the real form.
- SSE live-tail is **deferred**; v1 dashboard **polls** `/api/history` (single-channel `rt.Events()` contention). Flagged in Tasks 9 & 12.
- Telegram voice (OGG/Opus) is **dropped** in v1 (engine takes wav/mp3 only). Flagged in Tasks 5 & 12.

**Placeholder scan:** External-library boundaries (`init-data-golang`, `go-telegram/bot`) carry explicit "verify against installed API" notes rather than guessed-as-final signatures — these are real boundaries, not lazy placeholders, and each has working best-known code. No `TODO`/`TBD` left in engine-side code.

**Type consistency:** `tgClient` interface methods (`Updates/Send/EditText/Typing/AnswerCallback`) are used identically by the fake (Task 3), the bot (Tasks 4–8), and the real adapter (Task 11). `Bot` fields (`approvals`, `approvalTimeout`, `dashURL`, `cancelTurn`) are introduced before use. `shell3.Part{Kind,Data,MIME}`, `shell3.Event{Kind,Text,Err}`, `shell3.ApprovalRequest{Tool,RawArgs,Reason}`, `shell3.Wake`, `Session.Name()` all match the reference sheet.

**Build approach:** Tasks are mostly disjoint by file, so they map cleanly onto **parallel Sonnet subagents** (Task 1 and 2 first — they unblock the rest; 3 before 4–8; 9–10 independent of the bot; 11 integrates; 12 last). After the subagent build, the orchestrator verifies with `go build ./... && go vet ./... && gofmt -l . && go test -race ./...` and fixes integration seams.

---

## Execution Handoff

See the handoff prompt provided separately for launching this in a fresh session.
