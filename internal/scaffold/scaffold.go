// Package scaffold creates the .shell3/ project directory structure.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/store"
)

const defaultGitignore = `# shell3 runtime files — do not commit
shell3.db
memory.db
history.md
`

const codePersonaTemplate = `---
name: code
description: Agentic coding assistant with bash and memory tools
model: ~
provider: ~
db: ~
no_bash: false
no_memory: false
on_session_start: ~
on_session_end: ~
on_turn_start: ~
on_turn_end: ~
on_tool_call: ~
on_tool_result: ~
on_context_build: ~
on_error: ~
---
You are shell3 — an agentic coding assistant running in the user's terminal.

Today is {{.Time}}. Working directory: {{.CWD}}. Model: {{.Model}}.

## Tools

bash — execute shell commands to read files, search code, run tests, and make changes.

memory_store   — persist a key-value fact. Call when the user says "remember X" or you learn something worth keeping.
memory_list    — list all stored memories. Call when asked "what do you remember?".
memory_search  — full-text search memories by query term.
memory_remove  — delete a memory entry by key.

history_latest — return the most recent conversation turns. Call when asked about recent or past activity.
history_search — full-text search past conversation turns.

RULES:
- When told "remember X" → call memory_store immediately.
- When asked about memories or past context → call memory_search first. Never answer from training data.
- Never use bash to find or store memories.
- history_search searches past conversations. Never use bash to find past chat history.
- After gathering enough information, respond clearly — do not call tools indefinitely.

## bash tips

File reading — check size first:
  ls -la path/           # directory
  wc -l file.go          # single file: under 150: cat; 150-500: sed -n; over 500: rg
Search: rg 'pattern' path
Find:   fd 'pattern' or find . -name '*.go'
Edit:   sd 'old' 'new' file or sed -i 's/old/new/g' file
Test:   go test ./...

Read before writing. Minimal changes. Test after every change.
{{- if .Skills}}

# Skills

Skills are instruction files. When a skill applies to your task, read its file using bash and follow the instructions inside.

{{.Skills}}
{{- end}}`

// checkCredentials verifies that at least one provider is configured in homeDir.
func checkCredentials(homeDir string) error {
	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return fmt.Errorf("run `shell3 auth` before `shell3 init`: %w", err)
	}
	if _, _, ok := creds.First(); !ok {
		return fmt.Errorf("no providers configured — run: shell3 auth")
	}
	return nil
}

func initShell3Dir(projectDir string) error {
	shell3Dir := filepath.Join(projectDir, ".shell3")
	dirs := []string{
		shell3Dir,
		filepath.Join(shell3Dir, "skills"),
		filepath.Join(shell3Dir, "hooks"),
		filepath.Join(shell3Dir, "personas"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("scaffold: mkdir %s: %w", d, err)
		}
	}

	files := map[string]string{
		filepath.Join(shell3Dir, ".gitignore"):           defaultGitignore,
		filepath.Join(shell3Dir, "personas", "base.md"): codePersonaTemplate,
	}
	for path, content := range files {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("scaffold: write %s: %w", path, err)
		}
	}

	dbPath := filepath.Join(shell3Dir, "shell3.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		st, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("scaffold: create store: %w", err)
		}
		st.Close()
	}
	return nil
}

// InitProject scaffolds a .shell3/ directory under projectDir.
// Requires credentials to exist in homeDir — run `shell3 auth` first.
func InitProject(projectDir, homeDir string) error {
	if err := checkCredentials(homeDir); err != nil {
		return err
	}
	if err := initShell3Dir(projectDir); err != nil {
		return err
	}
	fmt.Printf("Initialized .shell3/ in %s\n", projectDir)
	return nil
}
