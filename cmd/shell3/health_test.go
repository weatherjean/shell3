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

// A healthy config is one that can actually be served, so the fixture carries a
// web password like a real one does (see TestHealthFailsWithoutAWebPassword).
const healthYAML = "models:\n  m: { base_url: \"http://x\", api_key: k, model: id }\n" +
	"web: { password: sixteen-characters-long }\n"
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

func TestHealthListsProject(t *testing.T) {
	work := t.TempDir()
	cfg := writeHealthTree(t, map[string]string{
		"projects/site/project.md":       "---\ndescription: the site\nworkdir: " + work + "\n---\nBrief.\n",
		"projects/site/manager.md":       "---\ndescription: manages the site\n---\nYou are the site manager.\n",
		"projects/site/skills/deploy.md": "---\ndescription: deploys the site\n---\nRun make deploy.\n",
	})
	out, err := runHealthAt(t, cfg)
	if err != nil {
		t.Fatalf("config with a valid project should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "project: site") {
		t.Fatalf("output should name the project:\n%s", out)
	}
	if !strings.Contains(out, work) {
		t.Fatalf("output should show the workdir:\n%s", out)
	}
	if !strings.Contains(out, "model m") || !strings.Contains(out, "1 skills") {
		t.Fatalf("output should show manager model and skill count:\n%s", out)
	}
	if !strings.Contains(out, "OK") {
		t.Fatalf("valid project must still print OK:\n%s", out)
	}
}

func TestHealthFailsOnProjectSkill(t *testing.T) {
	work := t.TempDir()
	cfg := writeHealthTree(t, map[string]string{
		"projects/site/project.md":    "---\ndescription: the site\nworkdir: " + work + "\n---\nBrief.\n",
		"projects/site/manager.md":    "---\ndescription: manages the site\n---\nbody\n",
		"projects/site/skills/bad.md": "no frontmatter here\n",
	})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("project with a skipped skill must fail health:\n%s", out)
	}
	if !strings.Contains(out, "bad.md") {
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

// A totp_secret that cannot mint a code is a guaranteed lockout: the login
// screen would ask for a code no authenticator can produce. health is where
// that surfaces, not the login screen.
func TestHealthFailsOnACorruptTOTPSecret(t *testing.T) {
	dir := writeHealthTree(t, map[string]string{
		"shell3.yaml": healthYAML[:strings.LastIndex(healthYAML, "}")] +
			", totp_secret: \"not!base32@at#all\" }\n",
	})

	out, err := runHealthAt(t, dir)
	if err == nil {
		t.Fatalf("health passed a totp_secret that locks the operator out:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "totp") {
		t.Errorf("failure does not name the secret:\n%s\n%v", out, err)
	}
}

func TestHealthOKWithAValidTOTPSecret(t *testing.T) {
	secret, _, err := newTOTPEnrolment("health-test")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeHealthTree(t, map[string]string{
		"shell3.yaml": healthYAML[:strings.LastIndex(healthYAML, "}")] +
			", totp_secret: \"" + secret + "\" }\n",
	})

	out, err := runHealthAt(t, dir)
	if err != nil {
		t.Fatalf("a valid secret should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "password + TOTP") {
		t.Errorf("output should report both factors:\n%s", out)
	}
}

// health is the strict view of a config, and a config with no web password
// cannot be served at all — `shell3 serve` refuses it. Reporting that here is
// the difference between finding out now and finding out when you try to start.
func TestHealthFailsWithoutAWebPassword(t *testing.T) {
	dir := writeHealthTree(t, map[string]string{
		"shell3.yaml": "models:\n  m: { base_url: \"http://x\", api_key: k, model: id }\n",
	})

	out, err := runHealthAt(t, dir)
	if err == nil {
		t.Fatalf("health passed a config that cannot be served; output:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "web.password") {
		t.Errorf("failure does not name the missing key:\n%s\n%v", out, err)
	}
}
