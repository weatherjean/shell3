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

// writeHealthConfig writes a minimal loadable config with one skills dir and
// returns the shell3.lua path.
func writeHealthConfig(t *testing.T, skillBody string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "skills", "probe.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}
	lua := `
shell3.model("m", { base_url="http://x", api_key="k", model="id" })
shell3.agent({ name="code", model="m", prompt="p", skills={ "lib/skills" } })
`
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
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
	cfg := writeHealthConfig(t, "---\ndescription: a valid probe skill\n---\nbody\n")
	out, err := runHealthAt(t, cfg)
	if err != nil {
		t.Fatalf("healthy config should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK") || !strings.Contains(out, "1 skills") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestHealthFailsOnSkippedSkill(t *testing.T) {
	cfg := writeHealthConfig(t, "no frontmatter here\n")
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

func TestHealthFailsOnLoadError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte("this is not lua ("), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runHealthAt(t, p); err == nil {
		t.Fatal("broken lua must fail health")
	}
}
