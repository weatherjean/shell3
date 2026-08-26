//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// boot --prompts refreshes the SHIPPED SKILLS of an existing install. The kit
// holds the wiring, every agent and every tool in one hand-edited file, so it
// is never rewritten; replaced skills are backed up, and skills the scaffold
// does not ship are never touched.
func TestPromptRefreshPreservesWiringAndBacksUp(t *testing.T) {
	dir := t.TempDir()

	kit := "#---\n# shell3:\n#   models: {m1: {base_url: \"http://x\", api_key: k, model: mm}}\n#---\n" +
		"#---\n# agent: main\n# use: [bash]\n#---\nmain_prompt() { cat <<'EOF2'\nMINE\nEOF2\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(kit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	userSkill := filepath.Join(dir, "skills", "my-own-thing.md")
	if err := os.WriteFile(userSkill, []byte("---\ndescription: mine\n---\nhands off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "cookbook.md"), []byte("stale scaffold skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	if err := runPromptRefresh(dir, now); err != nil {
		t.Fatal(err)
	}

	// The kit is hand-edited and must survive a prompt refresh untouched.
	kitAfter, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(kitAfter) != kit {
		t.Errorf("shell3.sh must not be touched by --prompts; got:\n%s", kitAfter)
	}

	// Backups: the replaced scaffold skill, under one stamped dir.
	backup := filepath.Join(dir, ".backup", "prompts-20260808-120000")
	b, err := os.ReadFile(filepath.Join(backup, "skills/cookbook.md"))
	if err != nil {
		t.Fatalf("expected a backup of skills/cookbook.md: %v", err)
	}
	if !strings.Contains(string(b), "stale scaffold skill") {
		t.Error("the backup must hold the OLD content")
	}

	// The user's own skill is untouched, the scaffold's is refreshed.
	if b, _ := os.ReadFile(userSkill); string(b) != "---\ndescription: mine\n---\nhands off\n" {
		t.Error("a user-authored skill must never be touched")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "skills", "cookbook.md")); string(b) == "stale scaffold skill" {
		t.Error("a scaffold skill should be refreshed")
	}

	// Idempotent: a second run changes nothing and creates no new backups.
	later := now.Add(time.Hour)
	if err := runPromptRefresh(dir, later); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".backup", "prompts-20260808-130000")); err == nil {
		t.Error("a no-change refresh must not create a backup dir")
	}
}

// --prompts refreshes scaffold-shipped skills only; the kit is hand-edited
// (wiring, every agent, every tool in one file) and must survive a refresh
// byte-for-byte, whatever it declares.
func TestPromptRefreshLeavesTheKitAlone(t *testing.T) {
	dir := t.TempDir()
	kit := "#---\n# shell3:\n#   models: {m1: {base_url: \"http://x\", api_key: k, model: mm}}\n#---\n" +
		"#---\n# agent: main\n#---\nmain_prompt() { cat <<'EOF2'\nMINE\nEOF2\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(kit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPromptRefresh(dir, time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != kit {
		t.Error("the kit must survive verbatim")
	}
}

func TestPromptRefreshRefusesFreshDir(t *testing.T) {
	if err := runPromptRefresh(t.TempDir(), time.Now()); err == nil {
		t.Fatal("an install with no kit must be refused (run plain boot instead)")
	}
}
