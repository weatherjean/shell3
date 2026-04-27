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
last_error.json
.env
`

const braveSearchTool = `name: brave_search
description: Web search via the Brave Search API. Returns top results as JSON. Set enabled to true after putting BRAVE_API_KEY in .shell3/.env.
enabled: false
secrets:
  - BRAVE_API_KEY
parameters:
  type: object
  properties:
    query:
      type: string
      description: Search query
    count:
      type: integer
      description: Result count (1-20)
      default: 5
  required: [query]
command: |
  curl -sG https://api.search.brave.com/res/v1/web/search \
    -H "X-Subscription-Token: $BRAVE_API_KEY" \
    -H "Accept: application/json" \
    --data-urlencode "q=$QUERY" \
    --data-urlencode "count=${COUNT:-5}"
timeout: 15s
`

const envExample = `# Copy this to .shell3/.env and fill in real values.
# .shell3/.env is gitignored. Do not commit secrets.
# Tighten file mode after copying: chmod 600 .shell3/.env
#
# BRAVE_API_KEY=your-key-here   # for tools/brave_search.yaml
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
{{- if .CoreMemories}}

## Core memories

{{range .CoreMemories}}- {{.Key}}: {{.Value}}
{{end}}
{{- end}}

## Tools

bash — execute shell commands to read files, search code, run tests, and make changes.

memory_upsert  — store, update, or delete a memory by key. Empty value deletes. Pass core=true to inject the memory into every future session prompt; omit core to preserve.
memory_query   — list or search memories. Omit query to list newest-first. Set core_only=true to filter.
history_query  — read past conversations. With a query: full-text search returning hits with session_id+chunk locators. Without: fetch one 25-turn chunk of one session (defaults to the latest COMPLETED session, chunk 1); response carries prev_session_id / next_session_id / total_chunks for navigation.

RULES:
- When told "remember X" → call memory_upsert immediately. Mark it core=true if it should persist across every session.
- When asked about memories or past context → call memory_query first. Never answer from training data.
- Never use bash to find or store memories.
- history_query searches and walks past conversations. Never use bash for chat history.
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

// checkCredentials verifies that at least one adapter instance is
// configured in homeDir.
func checkCredentials(homeDir string) error {
	if err := config.Migrate(homeDir); err != nil {
		return fmt.Errorf("migrate credentials: %w", err)
	}
	store, err := config.LoadCredStore(homeDir)
	if err != nil {
		return fmt.Errorf("run `shell3 auth` before `shell3 init`: %w", err)
	}
	if len(store.List()) == 0 {
		return fmt.Errorf("no adapter instances configured — run: shell3 auth")
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
		filepath.Join(shell3Dir, "tools"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("scaffold: mkdir %s: %w", d, err)
		}
	}

	files := map[string]string{
		filepath.Join(shell3Dir, ".gitignore"):                defaultGitignore,
		filepath.Join(shell3Dir, ".env.example"):              envExample,
		filepath.Join(shell3Dir, "personas", "base.md"):       codePersonaTemplate,
		filepath.Join(shell3Dir, "tools", "brave_search.yaml"): braveSearchTool,
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
