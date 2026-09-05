//go:build unix

package main

import (
	"bytes"
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

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
	"github.com/weatherjean/shell3/internal/telegram"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func TestTelegramInboxNotifierPostsCountWithoutClaiming(t *testing.T) {
	rt := shell3test.NewRuntimeForTest(t, "must not run")
	var out lockedBuffer
	bot := telegram.NewBot(telegram.NewConsoleClient(strings.NewReader(""), &out, telegram.ConsoleChatID), rt,
		telegram.ConsoleChatID, telegram.NewSessionIndex(func() *runs.Store { return rt.Store() }, "telegram"))
	store := inbox.Store{Root: t.TempDir()}
	receipt, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: "workflow complete"})
	if err != nil {
		t.Fatal(err)
	}
	hints := make(chan struct{}, 1)
	hints <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		notifyTelegramInbox(ctx, bot, store, hints, false, time.Hour, applog.Noop{})
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "Inbox: 1 pending notice") {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	pending, err := store.Read("main", receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != inbox.StatusNew || pending.Message.Body != "workflow complete" {
		t.Fatalf("pending notice = %+v", pending)
	}
	if !strings.Contains(out.String(), "✉️ Inbox: 1 pending notice") ||
		!strings.Contains(out.String(), "Latest: done — workflow complete") ||
		strings.Contains(out.String(), "must not run") {
		t.Fatalf("Telegram output = %q", out.String())
	}
	if strings.Count(out.String(), "Inbox: 1 pending notice") != 1 {
		t.Fatalf("duplicate notification = %q", out.String())
	}
}

func TestTelegramInboxNotifierReconcilesDroppedWake(t *testing.T) {
	rt := shell3test.NewRuntimeForTest(t, "must not run")
	var out lockedBuffer
	bot := telegram.NewBot(telegram.NewConsoleClient(strings.NewReader(""), &out, telegram.ConsoleChatID), rt,
		telegram.ConsoleChatID, telegram.NewSessionIndex(func() *runs.Store { return rt.Store() }, "telegram"))
	store := inbox.Store{Root: t.TempDir()}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		notifyTelegramInbox(ctx, bot, store, make(chan struct{}), false, 10*time.Millisecond, applog.Noop{})
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: "durable"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "Inbox: 1 pending notice") {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(out.String(), "✉️ Inbox: 1 pending notice") ||
		!strings.Contains(out.String(), "Latest: done — durable") ||
		strings.Contains(out.String(), "must not run") {
		t.Fatalf("Telegram output = %q", out.String())
	}
}

func TestTelegramInboxNotifierPostsStartupBeforePendingNotice(t *testing.T) {
	rt := shell3test.NewRuntimeForTest(t, "must not run")
	var out lockedBuffer
	bot := telegram.NewBot(telegram.NewConsoleClient(strings.NewReader(""), &out, telegram.ConsoleChatID), rt,
		telegram.ConsoleChatID, telegram.NewSessionIndex(func() *runs.Store { return rt.Store() }, "telegram"))
	store := inbox.Store{Root: t.TempDir()}
	if _, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: "ready"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		notifyTelegramInbox(ctx, bot, store, make(chan struct{}), true, time.Hour, applog.Noop{})
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "Inbox: 1 pending notice") {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	got := out.String()
	startup := strings.Index(got, telegram.StartupNotice)
	pending := strings.Index(got, "Inbox: 1 pending notice")
	if startup < 0 || pending < 0 || startup >= pending {
		t.Fatalf("startup must precede inbox notice: %q", got)
	}
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestLispTelegramConsoleUsesOrchestratorAndSingleTransportTool(t *testing.T) {
	var request map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"index":0,"delta":{"content":"remote-ready"},"finish_reason":"stop"}]}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "data: [DONE]")
		fmt.Fprintln(w)
	}))
	defer srv.Close()

	dir, err := os.MkdirTemp("/tmp", "shell3-telegram-lisp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	configPath := filepath.Join(dir, "shell3.lisp")
	src := fmt.Sprintf(`(shell3
  (version 1)
  (model primary
    (base-url %q)
    (api-key-env SHELL3_TELEGRAM_MODEL_TEST_KEY)
    (id "test-model"))
  (orchestrator (model primary) (prompt "test orchestrator"))
  (telegram
    (token-env UNUSED_CONSOLE_TOKEN)
    (home-chat 42)
    (allow-from 42)))`, srv.URL)
	if err := os.WriteFile(configPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL3_TELEGRAM_MODEL_TEST_KEY", "model-key")
	inputR, inputW := io.Pipe()
	var out lockedBuffer
	var diag bytes.Buffer
	cmd := newTelegramCommand()
	cmd.SetIn(inputR)
	cmd.SetOut(&out)
	cmd.SetErr(&diag)
	cmd.SetArgs([]string{"--console", "--config", configPath, "--workdir", dir})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	if _, err := fmt.Fprintln(inputW, "hello"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "remote-ready") {
		time.Sleep(20 * time.Millisecond)
	}
	_ = inputW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("telegram console: %v\n%s", err, diag.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("telegram console did not stop after EOF")
	}
	if !strings.Contains(out.String(), "remote-ready") {
		t.Fatalf("stdout = %q", out.String())
	}
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) != 3 {
		t.Fatalf("request tools = %#v", request["tools"])
	}
	var names []string
	for _, raw := range tools {
		fn := raw.(map[string]any)["function"].(map[string]any)
		names = append(names, fn["name"].(string))
	}
	if strings.Join(names, ",") != "bash,bash_bg,telegram" {
		t.Fatalf("tool names = %v", names)
	}
}

func TestValidateTelegramReloadRejectsScheduleChanges(t *testing.T) {
	parse := func(cron string) *lispconfig.Config {
		t.Helper()
		cfg, err := lispconfig.Parse("shell3.lisp", []byte(`(shell3 (version 1)
  (telegram (token-env TOKEN) (home-chat 1))
  (schedule probe
    (cron "`+cron+`")
    (timezone "UTC")
    (run (wrkfile "probe.wrk.lisp"))
    (output "result")
    (timeout "1m")))`))
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	current := parse("0 8 * * *")
	if err := validateTelegramReload("shell3.lisp", current, parse("0 8 * * *")); err != nil {
		t.Fatalf("unchanged reload = %v", err)
	}
	if err := validateTelegramReload("shell3.lisp", current, parse("0 9 * * *")); err == nil || !strings.Contains(err.Error(), "schedule declarations changed") {
		t.Fatalf("changed reload = %v", err)
	}
}
