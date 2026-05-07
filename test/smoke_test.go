//go:build smoke

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run with: go test ./test/ -tags smoke -v
// Requires Ollama running locally with llama3.2.
func TestSmoke_InitAndRun(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()

	authDir := filepath.Join(homeDir, ".shell3")
	os.MkdirAll(authDir, 0700)
	os.WriteFile(filepath.Join(authDir, "ai-do-not-read.auth.yaml"), []byte(`
instances:
  - name: ollama
    type: openai
    base_url: http://localhost:11434/v1
    api_key: ""
    models:
      - id: llama3.2
        context_window: 131072
`), 0600)

	binary := "../shell3"
	if _, err := os.Stat(binary); err != nil {
		t.Skip("binary not built — run go build -o shell3 ./cmd/shell3/ first")
	}

	cmd := exec.Command(binary, "--provider", "ollama", "say hello in one word")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("shell3 failed: %v\noutput: %s", err, out)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Error("expected non-empty output")
	}
	t.Logf("output: %s", out)
}
