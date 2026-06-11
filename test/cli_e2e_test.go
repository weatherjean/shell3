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

// TestCLIE2E_AppendSinkfileSelfReport asserts that a subagent invocation
// (--append-sinkfile + --id) appends exactly one agent_done notification to the
// sink on completion, with the chosen id, ok status, transcript pointer, and a
// preview derived from the final assistant text. This is the child self-report
// half of the bash-first subagent model.
func TestCLIE2E_AppendSinkfileSelfReport(t *testing.T) {
	binary, err := filepath.Abs(filepath.Join("..", "shell3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skip("binary not built — run: go build -o shell3 ./cmd/shell3")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"id":"c1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"all"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":" done here"},"finish_reason":null}]}`,
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

	transcript := filepath.Join(workDir, ".shell3", "agents", "sub1.jsonl")
	sinkPath := filepath.Join(workDir, "parent-sink.jsonl")
	cmd := exec.Command(binary,
		"-c", configPath,
		"--agent", "tester",
		"--out", transcript,
		"--append-sinkfile", sinkPath,
		"--id", "sub1",
		"--no-subagents",
		"do the task",
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shell3 failed: %v\noutput:\n%s", err, out)
	}

	data, err := os.ReadFile(sinkPath)
	if err != nil {
		t.Fatalf("read sink: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one agent_done line, got %d:\n%s", len(lines), data)
	}
	var n struct {
		Kind, ID, Status, Transcript, Preview string
	}
	if err := json.Unmarshal([]byte(lines[0]), &n); err != nil {
		t.Fatalf("bad sink line %q: %v", lines[0], err)
	}
	if n.Kind != "agent_done" {
		t.Errorf("kind = %q, want agent_done", n.Kind)
	}
	if n.ID != "sub1" {
		t.Errorf("id = %q, want sub1", n.ID)
	}
	if n.Status != "ok" {
		t.Errorf("status = %q, want ok", n.Status)
	}
	if n.Transcript != transcript {
		t.Errorf("transcript = %q, want %q", n.Transcript, transcript)
	}
	if !strings.Contains(n.Preview, "all done here") {
		t.Errorf("preview = %q, want it to contain the final assistant text", n.Preview)
	}
}
