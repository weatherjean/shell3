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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/orchestrator"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
	"github.com/weatherjean/shell3/internal/telegram"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func TestTelegramInboxNotifierDispatchesAndReconciles(t *testing.T) {
	for _, mode := range []string{"initial", "dropped-wake", "startup"} {
		t.Run(mode, func(t *testing.T) {
			rt := shell3test.NewRuntimeForTest(t, "inbox handled")
			var out lockedBuffer
			bot := telegram.NewBot(telegram.NewConsoleClient(strings.NewReader(""), &out, telegram.ConsoleChatID), rt, telegram.ConsoleChatID, telegram.NewSessionIndex(func() *runs.Store { return rt.Store() }, "telegram"))
			store := inbox.Store{Root: t.TempDir()}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			go func() {
				notifyTelegramInbox(ctx, bot, store, make(chan struct{}), mode == "startup", 10*time.Millisecond, applog.Noop{})
				close(done)
			}()
			if mode == "dropped-wake" {
				time.Sleep(20 * time.Millisecond)
			}
			receipt, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: "workflow complete"})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && !strings.Contains(out.String(), "inbox handled") {
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			<-done
			n, err := store.Read("main", receipt.ID)
			if err != nil || n.Status != inbox.StatusArchived {
				t.Fatalf("notice not automatically cleared: %+v %v", n, err)
			}
			if strings.Count(out.String(), "inbox handled") != 1 {
				t.Fatalf("reply=%q", out.String())
			}
			if mode == "startup" && strings.Index(out.String(), telegram.StartupNotice) > strings.Index(out.String(), "inbox handled") {
				t.Fatal("startup after delivery")
			}
		})
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

func TestLispTelegramConsoleUsesOrchestratorAndHostTools(t *testing.T) {
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
	if !ok || len(tools) != 4 {
		t.Fatalf("request tools = %#v", request["tools"])
	}
	var names []string
	for _, raw := range tools {
		fn := raw.(map[string]any)["function"].(map[string]any)
		names = append(names, fn["name"].(string))
	}
	if strings.Join(names, ",") != "bash,bash_bg,shell3,telegram" {
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

func TestTelegramHostControllerReloadsAtomicallyAndClassifiesRestart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lisp")
	t.Setenv("SHELL3_CONTROL_TEST_KEY", "test-key")
	t.Setenv("SHELL3_CONTROL_TEST_KEY_2", "test-key-2")
	writeConfig := func(prompt, modelTokenEnv, telegramTokenEnv string, contextWindow int) {
		t.Helper()
		src := fmt.Sprintf(`(shell3
  (version 1)
  (model primary
    (base-url "http://127.0.0.1")
    (api-key-env %s)
    (id "test-model")
    (context-window %d))
  (orchestrator (model primary) (prompt %q))
  (telegram (token-env %s) (home-chat 42)))`, modelTokenEnv, contextWindow, prompt, telegramTokenEnv)
		if err := os.WriteFile(configPath, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("old prompt", "SHELL3_CONTROL_TEST_KEY", "UNUSED_TELEGRAM_TOKEN", 1000)
	cfg, err := lispconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := orchestrator.OpenTelegram(t.Context(), configPath, dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	bot := telegram.NewBot(
		telegram.NewConsoleClient(strings.NewReader(""), io.Discard, telegram.ConsoleChatID),
		rt, telegram.ConsoleChatID,
		telegram.NewSessionIndex(func() *runs.Store { return rt.Store() }, "telegram"),
	)
	control := &telegramHostController{
		configPath: configPath, workDir: dir, rt: rt, bot: bot, current: cfg,
		currentLoaded: time.Now().UTC(),
	}
	sess, err := rt.Session(shell3.SessionOpts{Name: "control-test"})
	if err != nil {
		t.Fatal(err)
	}

	writeConfig("new prompt", "SHELL3_CONTROL_TEST_KEY", "UNUSED_TELEGRAM_TOKEN", 2000)
	status, err := control.status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status["config_valid"] != true || status["config_change"] != "reloadable" ||
		!reflect.DeepEqual(status["changed_sections"], []string{"models", "orchestrator"}) ||
		status["active_config_fingerprint"] == status["disk_config_fingerprint"] {
		t.Fatalf("status observability = %#v", status)
	}
	result, err := control.reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["config_change"] != "none" || result["applied_config_change"] != "reloadable" {
		t.Fatalf("reload result = %#v", result)
	}
	if !reflect.DeepEqual(result["applied_sections"], []string{"models", "orchestrator"}) ||
		!reflect.DeepEqual(result["changed_sections"], []string{}) ||
		result["active_config_fingerprint"] != result["disk_config_fingerprint"] {
		t.Fatalf("reload observability = %#v", result)
	}
	if _, err := time.Parse(time.RFC3339Nano, result["active_config_loaded_at"].(string)); err != nil {
		t.Fatalf("active_config_loaded_at = %q: %v", result["active_config_loaded_at"], err)
	}
	if got := sess.Snapshot().ContextWindow; got != 2000 {
		t.Fatalf("reloaded context window = %d", got)
	}

	writeConfig("must not apply", "SHELL3_CONTROL_TEST_KEY", "UNUSED_TELEGRAM_TOKEN_2", 3000)
	result, err = control.reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != false || result["config_change"] != "restart_required" || result["restart_required"] != true ||
		!reflect.DeepEqual(result["changed_sections"], []string{"models", "orchestrator", "telegram"}) || result["reload"] != "rejected" {
		t.Fatalf("restart classification = %#v", result)
	}
	if got := sess.Snapshot().ContextWindow; got != 2000 {
		t.Fatalf("restart-only generation was partially applied: context window %d", got)
	}

	if err := os.WriteFile(configPath, []byte("(broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := control.validate(t.Context()); err == nil {
		t.Fatal("invalid generation passed validation")
	}
	if got := sess.Snapshot().ContextWindow; got != 2000 {
		t.Fatalf("failed validation changed active context window: %d", got)
	}
}
