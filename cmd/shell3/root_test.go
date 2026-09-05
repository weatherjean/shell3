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
	"testing"

	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/paths"
)

func TestRootInteractiveCoexistsWithPersistentWakeListener(t *testing.T) {
	var request map[string]any
	var authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"index":0,"delta":{"content":"console-ready"},"finish_reason":"stop"}]}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "data: [DONE]")
		fmt.Fprintln(w)
	}))
	defer srv.Close()

	dir, err := os.MkdirTemp("/tmp", "shell3-console-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	configPath := filepath.Join(dir, "shell3.lisp")
	src := fmt.Sprintf(`(shell3
  (version 1)
  (model primary
    (base-url %q)
    (api-key-env SHELL3_TUI_TEST_KEY)
    (id "test-model"))
  (orchestrator (model primary) (prompt "test orchestrator")))`, srv.URL)
	if err := os.WriteFile(configPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL3_TUI_TEST_KEY", "process-key")
	listener, err := inbox.StartListener(context.Background(), inbox.Store{Root: paths.NewLocal(dir).Root})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cmd := newRootCommand()
	var out, diag bytes.Buffer
	cmd.SetIn(strings.NewReader("say ready\n/exit\n"))
	cmd.SetOut(&out)
	cmd.SetErr(&diag)
	cmd.SetArgs([]string{"--config", configPath, "--workdir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "console-ready") {
		t.Fatalf("stdout = %q", out.String())
	}
	if authorization != "Bearer process-key" {
		t.Fatalf("authorization used wrong secret source: %q", authorization)
	}
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("request tools = %#v", request["tools"])
	}
	var names []string
	for _, raw := range tools {
		tool := raw.(map[string]any)
		fn := tool["function"].(map[string]any)
		names = append(names, fn["name"].(string))
	}
	if strings.Join(names, ",") != "bash,bash_bg" {
		t.Fatalf("tool names = %v", names)
	}
}
