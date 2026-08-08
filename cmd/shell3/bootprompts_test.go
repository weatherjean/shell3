//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// boot --prompts refreshes scaffold prompts in place: the agent.md BODY is
// replaced while its frontmatter (the install's wiring: model, tools,
// context) survives verbatim, replaced files are backed up, and skills the
// scaffold doesn't ship are never touched.
func TestPromptRefreshPreservesWiringAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	customFM := "---\nmodel: minmax\ntools: [bash, bash_bg, edit, media, history]\ncontext: [memory.md]\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte(customFM+"OLD BODY\n"), 0o644); err != nil {
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

	agent, err := os.ReadFile(filepath.Join(dir, "agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(agent)
	if !strings.HasPrefix(got, customFM) {
		t.Errorf("agent.md frontmatter must survive verbatim, got prefix:\n%s", got[:min(len(got), 200)])
	}
	if strings.Contains(got, "OLD BODY") {
		t.Error("agent.md body should have been replaced")
	}
	// The install has `media` in tools, so the vision variant must render.
	if !strings.Contains(got, "read_media") {
		t.Error("a media-tooled install should get the vision prompt variant")
	}
	// The refreshed body carries the new self-awareness guidance.
	if !strings.Contains(got, "`status` reports YOUR live condition") {
		t.Error("the refreshed body should be the current scaffold prompt")
	}

	// Backups: old agent.md and old cookbook.md, under one stamped dir.
	backup := filepath.Join(dir, ".backup", "prompts-20260808-120000")
	for _, rel := range []string{"agent.md", "skills/cookbook.md"} {
		b, err := os.ReadFile(filepath.Join(backup, rel))
		if err != nil {
			t.Fatalf("expected a backup of %s: %v", rel, err)
		}
		if rel == "agent.md" && !strings.Contains(string(b), "OLD BODY") {
			t.Error("the backup must hold the OLD content")
		}
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

// A non-vision install's scaffold frontmatter contains a `# media: …`
// COMMENT — the refresh must not read that as the media tool being wired and
// hand a text-only model the vision prompt.
func TestPromptRefreshNonVisionVariant(t *testing.T) {
	dir := t.TempDir()
	fm := "---\nmodel: main\n# media: read_media needs a multimodal model — add it if you switch.\ntools: [bash, bash_bg, edit, history]\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte(fm+"OLD BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPromptRefresh(dir, time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	agent, err := os.ReadFile(filepath.Join(dir, "agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(agent), "read_media` opens") {
		t.Error("a non-vision install must not get the vision prompt variant")
	}
	if !strings.HasPrefix(string(agent), fm) {
		t.Error("frontmatter must survive verbatim")
	}
}

func TestPromptRefreshRefusesFreshDir(t *testing.T) {
	if err := runPromptRefresh(t.TempDir(), time.Now()); err == nil {
		t.Fatal("an install with no agent.md must be refused (run plain boot instead)")
	}
}
