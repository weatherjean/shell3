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

	configPath := filepath.Join(dir, "shell3.lua")
	os.WriteFile(configPath, []byte(`shell3.model("ollama", {
  base_url = "http://localhost:11434/v1",
  api_key = "",
  model = "llama3.2",
  context_window = 131072,
})
shell3.agent({ name = "smoke", model = "ollama", prompt = "you are a test", tools = {} })
`), 0600)

	binary := "../shell3"
	if _, err := os.Stat(binary); err != nil {
		t.Skip("binary not built — run go build -o shell3 ./cmd/shell3/ first")
	}

	cmd := exec.Command(binary, "-c", configPath, "say hello in one word")
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
