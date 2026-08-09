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

// `shell3 health` is documented as THE config check, so a telegram block the
// front-end would refuse to start on must fail here rather than printing OK.
func TestHealthFailsOnIncompleteTelegramBlock(t *testing.T) {
	cases := map[string]string{
		"no token":       "telegram:\n  token: \"\"\n  chat_id: \"123456789\"\n",
		"no chat_id":     "telegram:\n  token: \"123:ABC\"\n  chat_id: \"\"\n",
		"bad chat_id":    "telegram:\n  token: \"123:ABC\"\n  chat_id: \"not-a-number\"\n",
		"both are blank": "telegram:\n  token: \"\"\n  chat_id: \"\"\n",
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := writeHealthTree(t, map[string]string{"shell3.yaml": healthYAML + block})
			out, err := runHealthAt(t, cfg)
			if err == nil {
				t.Fatalf("an unusable telegram block must fail health:\n%s", out)
			}
			if !strings.Contains(out, "telegram") {
				t.Fatalf("output should name the telegram problem:\n%s", out)
			}
			if strings.Contains(out, "OK") {
				t.Fatalf("failing health must not print OK:\n%s", out)
			}
		})
	}
}

func TestHealthOKWithCompleteTelegramBlock(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{
		"shell3.yaml": healthYAML + "telegram:\n  token: \"123:ABC\"\n  chat_id: \"123456789\"\n",
	})
	out, err := runHealthAt(t, cfg)
	if err != nil {
		t.Fatalf("a complete telegram block should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "telegram: chat 123456789") {
		t.Fatalf("output should report the wired chat:\n%s", out)
	}
}

// No telegram block at all is legitimate (an `shell3 ask`-only config), so it
// reports the consequence plainly, without failing.
func TestHealthReportsAbsentTelegramWithoutFailing(t *testing.T) {
	cfg := writeHealthTree(t, nil)
	out, err := runHealthAt(t, cfg)
	if err != nil {
		t.Fatalf("a config with no telegram block should still pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "telegram: absent") {
		t.Fatalf("output should say the telegram front-end is unwired:\n%s", out)
	}
}

// The telegram check runs LAST on purpose: a blank chat_id is a state `boot`
// itself writes ("fill it in later"), and failing early would hide the hook
// and MCP diagnostics — the expensive checks someone runs health FOR.
func TestHealthBrokenTelegramDoesNotHideHookDiagnostics(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{
		"shell3.yaml":        healthYAML + "telegram:\n  token: \"123:ABC\"\n  chat_id: \"\"\n",
		"hooks/tool-call.sh": "echo not-json\n",
	})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("a broken hook must fail health:\n%s", out)
	}
	if !strings.Contains(out, "hook") {
		t.Errorf("the hook diagnostic must print even with an unusable telegram block:\n%s", out)
	}
}
