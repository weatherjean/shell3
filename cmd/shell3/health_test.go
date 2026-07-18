//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const healthYAML = "models:\n  m: { base_url: \"http://x\", api_key: k, model: id }\n"
const healthAgent = "---\nmodel: m\n---\np\n"

// writeHealthTree writes a minimal loadable config tree (plus extra files)
// and returns the directory.
func writeHealthTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"shell3.yaml": healthYAML, "agent.md": healthAgent}
	for k, v := range extra {
		files[k] = v
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runHealthAt(t *testing.T, cfg string) (string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := runHealth(cmd, cfg)
	return buf.String(), err
}

func TestHealthOK(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{"skills/probe.md": "---\ndescription: a valid probe skill\n---\nbody\n"})
	out, err := runHealthAt(t, cfg)
	if err != nil {
		t.Fatalf("healthy config should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK") || !strings.Contains(out, "1 skills") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestHealthFailsOnSkippedSkill(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{"skills/probe.md": "no frontmatter here\n"})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("config with a skipped skill must fail health:\n%s", out)
	}
	if !strings.Contains(out, "probe.md") {
		t.Fatalf("output should name the skipped file:\n%s", out)
	}
	if strings.Contains(out, "OK") {
		t.Fatalf("failing health must not print OK:\n%s", out)
	}
}

func TestHealthFailsOnDownMCPServer(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{
		"shell3.yaml": healthYAML + "mcp:\n  dead: { command: [\"/nonexistent-mcp-server-xyz\"], timeout: 2 }\n",
		"agent.md":    "---\nmodel: m\nmcp: all\n---\np\n",
	})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("down MCP server must fail health:\n%s", out)
	}
	if !strings.Contains(out, "dead") {
		t.Fatalf("output should name the down server:\n%s", out)
	}
	if strings.Contains(out, "OK") {
		t.Fatalf("failing health must not print OK:\n%s", out)
	}
}

func TestHealthFailsOnLoadError(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{"shell3.yaml": "models: [broken\n"})
	if _, err := runHealthAt(t, cfg); err == nil {
		t.Fatal("broken yaml must fail health")
	}
}

func TestHealthFailsOnBrokenHook(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{"hooks/tool-call.sh": "echo not-json\n"})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("broken hook must fail health:\n%s", out)
	}
	if !strings.Contains(out, "hook") {
		t.Fatalf("output should name the hook:\n%s", out)
	}
}

func TestHealthOKWithStrictHook(t *testing.T) {
	// A hook that deliberately blocks everything is a valid (strict) gate.
	cfg := writeHealthTree(t, map[string]string{
		"hooks/tool-call.sh": `printf '{"block": true, "reason": "locked down"}'` + "\n",
	})
	out, err := runHealthAt(t, cfg)
	if err != nil {
		t.Fatalf("strict hook should pass health: %v\n%s", err, out)
	}
}
