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

// healthWiring is the `shell3:` block of the minimal health fixture; extra
// wiring lines (telegram, mcp) are appended by the callers that need them and
// re-indented into the comment fence by kitWithWiring.
const healthWiring = "models:\n  m: { base_url: \"http://x\", api_key: k, model: id }\n"

// kitWithWiring renders a kit whose `shell3:` block is the given YAML, plus
// one main agent and, when gate is non-empty, a gate function for it.
func kitWithWiring(wiring, gate string) string {
	var b strings.Builder
	b.WriteString("#---\n# shell3:\n")
	for _, line := range strings.Split(strings.TrimRight(wiring, "\n"), "\n") {
		b.WriteString("#   " + line + "\n")
	}
	b.WriteString("#---\n\n#---\n# agent: main\n# model: m\n#---\nmain_prompt() { cat <<'SHELL3_EOF'\np\nSHELL3_EOF\n}\n")
	if gate != "" {
		b.WriteString("\n#---\n# gate: [main]\n#---\nmain_gate() {\n" + gate + "\n}\n")
	}
	return b.String()
}

func writeHealthTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"shell3.sh": kitWithWiring(healthWiring, "")}
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
		"shell3.sh": kitWithWiring(healthWiring+"mcp:\n  dead: { command: [\"/nonexistent-mcp-server-xyz\"], timeout: 2 }\n", ""),
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
	cfg := writeHealthTree(t, map[string]string{"shell3.sh": "#---\n# shell3:\n#   models: [broken\n#---\n"})
	if _, err := runHealthAt(t, cfg); err == nil {
		t.Fatal("broken wiring yaml must fail health")
	}
}

func TestHealthFailsOnBrokenHook(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{"shell3.sh": kitWithWiring(healthWiring, "echo not-json")})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("broken hook must fail health:\n%s", out)
	}
	if !strings.Contains(out, "hook") {
		t.Fatalf("output should name the hook:\n%s", out)
	}
}

func TestHealthOKWithStrictHook(t *testing.T) {
	// A gate that deliberately blocks everything is a valid (strict) gate.
	cfg := writeHealthTree(t, map[string]string{
		"shell3.sh": kitWithWiring(healthWiring, `printf '{"block": true, "reason": "locked down"}'`),
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
			cfg := writeHealthTree(t, map[string]string{"shell3.sh": kitWithWiring(healthWiring+block, "")})
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
		"shell3.sh": kitWithWiring(healthWiring+"telegram:\n  token: \"123:ABC\"\n  chat_id: \"123456789\"\n", ""),
	})
	out, err := runHealthAt(t, cfg)
	if err != nil {
		t.Fatalf("a complete telegram block should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "telegram: home chat 123456789") {
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

func TestHealthBrokenTelegramDoesNotHideHookDiagnostics(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{
		"shell3.sh": kitWithWiring(healthWiring+"telegram:\n  token: \"123:ABC\"\n  chat_id: \"\"\n", "echo not-json"),
	})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("a broken hook must fail health:\n%s", out)
	}
	if !strings.Contains(out, "hook") {
		t.Errorf("the hook diagnostic must print even with an unusable telegram block:\n%s", out)
	}
}

const healthKit = `#---
# shell3:
#   models:
#     m1: {base_url: "http://x", api_key: k, model: mm}
#---

#---
# agent: main
# model: m1
#---
main_prompt() { cat <<'EOF2'
hi
EOF2
}

#---
# tool: sync-notion-recent
# description: sync recent Notion pages
#---
sync_notion() { echo synced; }

#---
# tool: needs-arg
# description: a tool a cron job could never satisfy
# params:
#   url: {type: string, required: true, description: homepage URL}
#---
needs_arg() { echo "$url"; }
`

// writeKitHealthTree writes a config dir whose kit is healthKit plus the
// given `cron:` declarations. Cron jobs are kit blocks, so a bad one is a
// kit.Parse error — health surfaces it because health parses the kit.
func writeKitHealthTree(t *testing.T, cronDecls string) string {
	t.Helper()
	return writeKitTree(t, healthKit+cronDecls)
}

func writeKitTree(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHealthFailsOnUnknownCronAgent(t *testing.T) {
	cfg := writeKitHealthTree(t, `
#---
# cron: rounds
# schedule: "@every 30m"
# agent: nobody
#---
cron_rounds() { cat <<'EOF2'
do the rounds
EOF2
}
`)
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("a cron job naming an undeclared agent must fail health:\n%s", out)
	}
	got := out + err.Error()
	if !strings.Contains(got, `"rounds"`) || !strings.Contains(got, "nobody") {
		t.Fatalf("output should name the job and the missing agent:\n%s", got)
	}
}

func TestHealthOKWithCronAgentJob(t *testing.T) {
	cfg := writeKitHealthTree(t, `
#---
# cron: rounds
# schedule: "@every 30m"
# agent: main
#---
cron_rounds() { cat <<'EOF2'
do the rounds
EOF2
}
`)
	if out, err := runHealthAt(t, cfg); err != nil {
		t.Fatalf("a cron job naming the main agent should pass health: %v\n%s", err, out)
	}
}

func TestHealthFailsOnCronToolJob(t *testing.T) {
	cfg := writeKitHealthTree(t, `
#---
# cron: sync
# schedule: "@every 30m"
# tool: sync-notion-recent
#---
`)
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("a cron tool: job must fail health:\n%s", out)
	}
	got := out + err.Error()
	if !strings.Contains(got, "agent turns only") {
		t.Fatalf("output should name the replacement:\n%s", got)
	}
}
