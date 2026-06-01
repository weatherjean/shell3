//go:build !no_e2e

package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIE2E_HeadlessRun spins up a fake OpenAI-compat server and exercises
// the CLI's thick bootstrap path end-to-end via the --out JSONL log.
func TestCLIE2E_HeadlessRun(t *testing.T) {
	binary, err := filepath.Abs(filepath.Join("..", "shell3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skip("binary not built — run: go build -o shell3 ./cmd/shell3")
	}

	// Fake OpenAI-compat /v1/chat/completions server that returns one SSE stream.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"id":"c1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	homeDir := t.TempDir()
	workDir := t.TempDir()

	configPath := filepath.Join(workDir, "shell3.lua")
	cfg := fmt.Sprintf(`shell3.model("fake", {
  base_url = "%s/v1",
  api_key = "test",
  model = "test-model",
  context_window = 4096,
})
shell3.agent({ name = "tester", model = "fake", prompt = "you are a test", tools = {} })
`, server.URL)
	if err := os.WriteFile(configPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(workDir, "out.jsonl")
	cmd := exec.Command(binary,
		"-c", configPath,
		"--out", outPath,
		"hello",
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell3 failed: %v\noutput:\n%s", err, out)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read out.jsonl: %v (stdout:\n%s)", err, out)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("no jsonl lines produced\nstdout:\n%s\nraw out.jsonl:\n%s", out, data)
	}
	kinds := map[string]int{}
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("bad jsonl line: %s (%v)", line, err)
			continue
		}
		if k, ok := rec["kind"].(string); ok {
			kinds[k]++
		}
	}

	// The CLI's OutSink emits "start"/"end" sink envelope events plus per-event
	// chat kinds. session_start/session_end are TUI-level events not piped to the
	// sink in RunOnce, so we assert the sink envelope and the chat events that
	// are written.
	wantKinds := []string{"start", "user_message", "assistant_token", "assistant_message", "turn_done", "end"}
	for _, k := range wantKinds {
		if kinds[k] == 0 {
			t.Errorf("missing %q event in jsonl (got kinds: %v)\nshell3 stdout:\n%s\nraw out.jsonl:\n%s", k, kinds, out, data)
		}
	}
}
