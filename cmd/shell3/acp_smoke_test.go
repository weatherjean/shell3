//go:build unix

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// findModuleRoot walks up from the test's cwd to find the directory containing
// go.mod. This is robust regardless of where go test is invoked from.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("findModuleRoot: abs: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("findModuleRoot: go.mod not found")
		}
		dir = parent
	}
}

// TestAcpSmoke is a subprocess smoke test for the `shell3 acp` subcommand.
// It verifies the ACP JSON-RPC 2.0 handshake (initialize + session/new) over
// stdio without hitting any real LLM (the two methods complete before the
// model is contacted).
func TestAcpSmoke(t *testing.T) {
	moduleRoot := findModuleRoot(t)

	// ── Step 1: build the binary ──────────────────────────────────────────────
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "shell3")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/shell3")
	buildCmd.Dir = moduleRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	// ── Step 2: write minimal shell3.lua (fake base_url; LLM never called) ──
	cfgDir := t.TempDir()
	luaPath := filepath.Join(cfgDir, "shell3.lua")
	luaContent := `
shell3.model("test", {
  base_url       = "http://127.0.0.1:1",
  api_key        = "test",
  model          = "gpt-4o",
  context_window = 128000,
})
shell3.agent({
  name   = "code",
  model  = "test",
  prompt = "You are a coding assistant.",
  tools  = { bash = true },
})
`
	if err := os.WriteFile(luaPath, []byte(luaContent), 0o644); err != nil {
		t.Fatalf("write shell3.lua: %v", err)
	}

	// Absolute workdir for session/new.
	workDir := t.TempDir()

	// ── Step 3: start the subprocess ─────────────────────────────────────────
	proc := exec.Command(binPath, "acp", "--config", luaPath)

	stdinPipe, err := proc.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutPipe, err := proc.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderrBuf strings.Builder
	proc.Stderr = &stderrBuf

	if err := proc.Start(); err != nil {
		t.Fatalf("proc start: %v", err)
	}

	// ── Step 4: send two ndjson requests ─────────────────────────────────────
	// CRITICAL: session/new params must include mcpServers:[] (nil is rejected
	// with -32602). cwd must be an absolute path.
	initLine := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n"
	sessLine := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":%q,"mcpServers":[]}}`,
		workDir,
	) + "\n"

	if _, err := fmt.Fprint(stdinPipe, initLine); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if _, err := fmt.Fprint(stdinPipe, sessLine); err != nil {
		t.Fatalf("write session/new: %v", err)
	}

	// ── Step 5: read responses with a 10 s overall deadline ──────────────────
	type rpcResponse struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}

	responses := make(map[int]rpcResponse)
	scanDone := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var resp rpcResponse
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				// Skip non-JSON lines (there should be none, but be defensive).
				continue
			}
			if resp.ID != 0 {
				responses[resp.ID] = resp
			}
			if len(responses) >= 2 {
				break
			}
		}
		scanDone <- scanner.Err()
	}()

	select {
	case scanErr := <-scanDone:
		if scanErr != nil {
			t.Fatalf("read responses: %v\nstderr: %s", scanErr, stderrBuf.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for responses\nstderr: %s", stderrBuf.String())
	}

	// ── Step 6: assert initialize and session/new results ────────────────────
	initResp, ok := responses[1]
	if !ok {
		t.Fatalf("no response for initialize (id=1)\nstderr: %s", stderrBuf.String())
	}
	if initResp.Error != nil && string(initResp.Error) != "null" {
		t.Fatalf("initialize returned error: %s\nstderr: %s", initResp.Error, stderrBuf.String())
	}
	var initResult struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		t.Fatalf("unmarshal initialize result: %v\nresult raw: %s\nstderr: %s",
			err, initResp.Result, stderrBuf.String())
	}
	if initResult.ProtocolVersion != 1 {
		t.Errorf("initialize: protocolVersion = %d, want 1", initResult.ProtocolVersion)
	}

	sessResp, ok := responses[2]
	if !ok {
		t.Fatalf("no response for session/new (id=2)\nstderr: %s", stderrBuf.String())
	}
	if sessResp.Error != nil && string(sessResp.Error) != "null" {
		t.Fatalf("session/new returned error: %s\nstderr: %s", sessResp.Error, stderrBuf.String())
	}
	var sessResult struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(sessResp.Result, &sessResult); err != nil {
		t.Fatalf("unmarshal session/new result: %v\nresult raw: %s\nstderr: %s",
			err, sessResp.Result, stderrBuf.String())
	}
	if sessResult.SessionID == "" {
		t.Errorf("session/new: sessionId is empty\nstderr: %s", stderrBuf.String())
	}

	// ── Step 7: close stdin and assert clean exit ─────────────────────────────
	if err := stdinPipe.Close(); err != nil {
		t.Errorf("close stdin: %v", err)
	}

	exitDone := make(chan error, 1)
	go func() {
		exitDone <- proc.Wait()
	}()

	select {
	case waitErr := <-exitDone:
		if waitErr != nil {
			t.Errorf("process exit: %v\nstderr: %s", waitErr, stderrBuf.String())
		}
	case <-time.After(5 * time.Second):
		_ = proc.Process.Kill()
		t.Errorf("process did not exit cleanly within 5s\nstderr: %s", stderrBuf.String())
	}
}
