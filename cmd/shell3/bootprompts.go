//go:build unix

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/weatherjean/shell3/internal/scaffold"
)

// `shell3 boot --prompts` refreshes the scaffold-owned skills of an EXISTING
// install without a re-boot: the kit, .env, cron and memory stay untouched,
// and every file that changes is backed up first under
// <configDir>/.backup/prompts-<ts>/. Skills the scaffold doesn't ship
// (user-authored ones) are never touched.

// runPromptRefresh implements boot --prompts against dir (the global config
// root in production; injectable for tests). now stamps the backup dir.
func runPromptRefresh(dir string, now time.Time) error {
	kitPath := filepath.Join(dir, "shell3.sh")
	if _, err := os.Stat(kitPath); err != nil {
		return fmt.Errorf("boot --prompts: no shell3.sh in %s — this refreshes an existing install; run a plain `shell3 boot` first", dir)
	}

	files, err := scaffold.PromptFiles(scaffold.Values{Name: "main"})
	if err != nil {
		return err
	}

	// PromptFiles ships only skills/ — the kit holds the wiring, every agent
	// and every tool in one hand-edited file, so there is no safe seam to
	// splice a new prompt into. Refreshing skills gives an upgrade the new
	// guidance without touching anything the operator wrote.

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
	fmt.Println("shell3.sh is yours and was not touched — compare it against the")
	fmt.Println("scaffold if you want the newer agent prompts too.")
	fmt.Println("apply with a reload (send /reload, or ask the agent to `reload`)")
	return nil
}
