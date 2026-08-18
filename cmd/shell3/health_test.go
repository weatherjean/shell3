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

// healthKit is a minimal kit declaring the models wiring, one main agent, and
// one tool with a required param — enough to exercise cron tool: job
// validation without dragging in a full boot fixture.
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

// writeKitHealthTree writes a kit-only config (no shell3.yaml, no agent.md —
// a shell3.sh alone is a complete config) plus the given cron/*.md files.
func writeKitHealthTree(t *testing.T, cronFiles map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(healthKit), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, body := range cronFiles {
		p := filepath.Join(dir, "cron", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHealthOKWithValidCronToolJob(t *testing.T) {
	cfg := writeKitHealthTree(t, map[string]string{
		"sync.md": "---\nschedule: \"@every 30m\"\ntool: sync-notion-recent\n---\n",
	})
	out, err := runHealthAt(t, cfg)
	if err != nil {
		t.Fatalf("a valid cron tool job should pass health: %v\n%s", err, out)
	}
}

// Two agents may each legally declare a tool with the same name — the
// duplicate check in kit.Check is per-scope, not kit-wide. A cron `tool:`
// job names no agent, so Kit.ToolByName's first-match-wins pick would run
// whichever function happened to parse first, silently, at 3am. health must
// count matches and fail, naming both declaring scopes.
const healthKitDupTool = `#---
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
# tool: sync
# description: main's sync
#---
main_sync() { echo main; }

#---
# agent: helper
# model: m1
#---
helper_prompt() { cat <<'EOF2'
hi
EOF2
}

#---
# tool: sync
# description: helper's sync
#---
helper_sync() { echo helper; }
`

func TestHealthFailsOnAmbiguousCronTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(healthKitDupTool), 0o600); err != nil {
		t.Fatal(err)
	}
	cronDir := filepath.Join(dir, "cron")
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cronDir, "sync.md"), []byte("---\nschedule: \"@every 30m\"\ntool: sync\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runHealthAt(t, dir)
	if err == nil {
		t.Fatalf("a cron job naming an ambiguously-declared tool must fail health:\n%s", out)
	}
	if !strings.Contains(out, "sync.md") || !strings.Contains(out, `"sync"`) {
		t.Fatalf("output should name the job and the ambiguous tool:\n%s", out)
	}
	if !strings.Contains(out, "main") || !strings.Contains(out, "helper") {
		t.Fatalf("output should name BOTH declaring scopes so the operator can disambiguate:\n%s", out)
	}
}

func TestHealthFailsOnUnknownCronTool(t *testing.T) {
	cfg := writeKitHealthTree(t, map[string]string{
		"sync.md": "---\nschedule: \"@every 30m\"\ntool: no-such-tool\n---\n",
	})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("a cron job naming an undeclared tool must fail health:\n%s", out)
	}
	if !strings.Contains(out, "sync.md") || !strings.Contains(out, "no-such-tool") {
		t.Fatalf("output should name the job and the missing tool:\n%s", out)
	}
}

func TestHealthFailsOnCronToolWithRequiredParam(t *testing.T) {
	cfg := writeKitHealthTree(t, map[string]string{
		"sync.md": "---\nschedule: \"@every 30m\"\ntool: needs-arg\n---\n",
	})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("a cron job whose tool requires an argument must fail health (fireTool passes none):\n%s", out)
	}
	if !strings.Contains(out, "needs-arg") || !strings.Contains(out, "url") {
		t.Fatalf("output should name the tool and the required argument:\n%s", out)
	}
}

// A tool: cron job under a markdown config (no shell3.sh at all) has no kit
// to resolve against — it must fail health with a clear message, not panic
// or silently no-op at 3am. The refusal actually comes from config.Load
// itself (see TestLoadCronToolJobWithoutKitFails in internal/config), so
// health surfaces it as a load error rather than its own per-job check.
func TestHealthFailsOnCronToolWithNoKit(t *testing.T) {
	cfg := writeHealthTree(t, map[string]string{
		"cron/sync.md": "---\nschedule: \"@every 30m\"\ntool: sync-notion-recent\n---\n",
	})
	out, err := runHealthAt(t, cfg)
	if err == nil {
		t.Fatalf("a tool: cron job with no kit must fail health:\n%s", out)
	}
	if !strings.Contains(err.Error(), "sync.md") {
		t.Fatalf("error should name the offending job: %v", err)
	}
}
