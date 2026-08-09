//go:build unix

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/scaffold"
)

// `shell3 boot --prompts` refreshes the scaffold-owned prompt files of an
// EXISTING install — agent.md's body, agents/*, skills/*
// — without a re-boot: wiring stays untouched (shell3.yaml, .env, hooks,
// cron, projects, memory.md, and both files' frontmatter), and every file
// that changes is backed up first under <configDir>/.backup/prompts-<ts>/.
// Skills the scaffold doesn't ship (user-authored ones) are never touched.

// runPromptRefresh implements boot --prompts against dir (the global config
// root in production; injectable for tests). now stamps the backup dir.
func runPromptRefresh(dir string, now time.Time) error {
	agentPath := filepath.Join(dir, "agent.md")
	oldAgent, err := os.ReadFile(agentPath)
	if err != nil {
		return fmt.Errorf("boot --prompts: no agent.md in %s — this refreshes an existing install; run a plain `shell3 boot` first", dir)
	}

	// Vision decides which agent-prompt variant renders (the media tool
	// lines). The install's own frontmatter is the truth: boot wires vision
	// by putting `media` in the tools list. Matched on the tools: line
	// specifically — the NON-vision scaffold ships a `# media: …` comment
	// inside the frontmatter, so a substring test would see "media" in
	// exactly the installs that don't have the tool.
	fm, _, ok := splitFrontmatter(oldAgent)
	if !ok {
		return fmt.Errorf("boot --prompts: %s has no frontmatter to preserve — refusing to guess; fix the file or re-boot", agentPath)
	}
	vision := toolsMediaRe.MatchString(fm)

	files, err := scaffold.PromptFiles(scaffold.Values{Name: "main", Vision: vision})
	if err != nil {
		return err
	}

	backupDir := filepath.Join(dir, ".backup", "prompts-"+now.Format("20060102-150405"))
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	updated, added, kept := 0, 0, 0
	for _, rel := range rels {
		content := files[rel]
		target := filepath.Join(dir, rel)
		old, readErr := os.ReadFile(target)
		exists := readErr == nil

		// agent.md carries the install's wiring in its
		// frontmatter (model, tools, context files) — keep it verbatim and
		// take only the scaffold's new body.
		if exists && rel == "agent.md" {
			oldFM, _, okOld := splitFrontmatter(old)
			_, newBody, okNew := splitFrontmatter(content)
			if okOld && okNew {
				content = []byte(oldFM + newBody)
			}
		}

		switch {
		case exists && bytes.Equal(old, content):
			kept++
			continue
		case exists:
			backupPath := filepath.Join(backupDir, rel)
			if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
				return fmt.Errorf("boot --prompts: backup dir: %w", err)
			}
			if err := os.WriteFile(backupPath, old, 0o644); err != nil {
				return fmt.Errorf("boot --prompts: backup %s: %w", rel, err)
			}
			updated++
		default:
			added++
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("boot --prompts: %w", err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return fmt.Errorf("boot --prompts: write %s: %w", rel, err)
		}
		fmt.Printf("  %s %s\n", map[bool]string{true: "updated", false: "added  "}[exists], rel)
	}

	fmt.Printf("prompts refreshed: %d updated, %d added, %d already current\n", updated, added, kept)
	if updated > 0 {
		fmt.Printf("previous versions: %s\n", backupDir)
	}
	fmt.Println("apply with a reload (Status → Reload config, or ask the agent to `reload`)")
	return nil
}

// toolsMediaRe matches a frontmatter `tools:` list that includes the media
// tool — an uncommented line only.
var toolsMediaRe = regexp.MustCompile(`(?m)^tools:\s*\[[^\]]*\bmedia\b`)

// splitFrontmatter separates a leading `---\n…\n---\n` block (returned WITH
// its delimiters and trailing newline) from the body that follows. ok is
// false when the file doesn't open with a frontmatter fence.
func splitFrontmatter(b []byte) (frontmatter, body string, ok bool) {
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		return "", s, false
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", s, false
	}
	cut := len("---\n") + end + len("\n---\n")
	return s[:cut], s[cut:], true
}
