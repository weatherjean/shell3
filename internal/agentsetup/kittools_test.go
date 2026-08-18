package agentsetup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/agentsetup"
)

// toolKit declares a tool under the employee's own scope (not the main
// agent's) — Parts.KitToolByName must still find it. A cron tool: job names
// no agent, so it has no per-agent Resolved set to search; it needs the
// whole-kit lookup this exercises end to end (Parts -> kit.Kit.ToolByName).
const toolKit = `#---
# shell3:
#   models:
#     m:
#       base_url: http://x/v1
#       api_key: env:K
#       model: m
#---

#---
# agent: main
# model: m
#---
main_prompt() { cat <<'EOF'
you are the agent
EOF
}

#---
# agent: worker
# model: m
#---
worker_prompt() { cat <<'EOF'
you do the work
EOF
}

#---
# tool: sync-notion-recent
# description: sync recent Notion pages
#---
worker_sync() { echo synced; }
`

func toolKitParts(t *testing.T) (*agentsetup.Parts, func()) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(toolKit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("K=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: dir, CWD: dir, HomeDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	return parts, cleanup
}

func TestKitToolByNameFindsToolFromAnyAgentScope(t *testing.T) {
	parts, cleanup := toolKitParts(t)
	defer cleanup()

	tl, ok := parts.KitToolByName("sync-notion-recent")
	if !ok || tl.Name != "sync-notion-recent" {
		t.Fatalf("KitToolByName = %+v, %v; want the worker-scoped tool, found with no agent named", tl, ok)
	}
	if _, ok := parts.KitToolByName("no-such-tool"); ok {
		t.Fatal("KitToolByName should report false for an undeclared name")
	}
}

func TestKitPathReturnsTheLoadedKitFile(t *testing.T) {
	parts, cleanup := toolKitParts(t)
	defer cleanup()

	got := parts.KitPath()
	if filepath.Base(got) != "shell3.sh" {
		t.Fatalf("KitPath = %q, want it to end in shell3.sh", got)
	}
}

// A markdown-only config (no shell3.sh) loads no kit at all, so
// KitToolByName must report false rather than panic on a nil kit.
func TestKitToolByNameWithNoKitLoaded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.yaml"), []byte("models:\n  m: { base_url: \"http://x\", api_key: k, model: id }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte("---\nmodel: m\n---\np\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: dir, CWD: dir, HomeDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	defer cleanup()

	if _, ok := parts.KitToolByName("anything"); ok {
		t.Fatal("KitToolByName should report false when no kit is loaded")
	}
}
