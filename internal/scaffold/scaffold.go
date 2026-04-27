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
secrets.shell3
`

const braveSearchTool = `name: brave_search
description: Web search via the Brave Search API. Returns top results as JSON. Set enabled to true after running 'shell3 secrets set --key BRAVE_API_KEY --secret <token>'.
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
You are an expert coding assistant operating inside shell3, an agentic coding agent harness. You help users by reading files, executing commands, editing code, and writing new files.

You are working on a project in ` + "`{{.CWD}}`" + `. Today is {{.Time}}.
{{- if .CoreMemories}}

## Core memories

{{range .CoreMemories}}- {{.Key}}: {{.Value}}
{{end}}
{{- end}}

## Available tools

- bash: execute shell commands to read, search, test, and edit files.
- memory_upsert: store/update/delete a memory by key. Empty value deletes. ` + "`core=true`" + ` injects into every future session prompt.
- memory_list: list memories newest-first. ` + "`core_only=true`" + ` to filter.
- memory_search: full-text search memories. Pass ` + "`terms[]`" + ` (one concept per array element). Default ` + "`match=any`" + ` (OR); use ` + "`match=all`" + ` to narrow.
- history_get: read past conversations one chunk at a time. ` + "`{}`" + ` → most-recent COMPLETED session, chunk 1. Page within session via ` + "`chunk: 2, 3, ...`" + ` up to ` + "`total_chunks`" + `. Walk to older sessions via ` + "`session_id: <prev_session_id>`" + `.
- history_search: full-text search past conversations. Same ` + "`terms[]`" + ` + ` + "`match`" + ` shape as memory_search. Each hit includes ` + "`session_id`" + `/` + "`chunk`" + ` — pass to history_get for surrounding context.
- prune_tool_result: replace a prior tool result with a stub to reclaim context. Args: ` + "`tool_call_id`" + ` (prefix ` + "`[tool_call_id=...]`" + ` on each result), ` + "`reason`" + `.

Custom project tools may also be available.

## Guidelines

- Act like an autonomous senior pair-programmer. Gather context, plan, implement, test, and refine without asking permission at each step. Persist end-to-end in one turn; do not stop at analysis or partial fixes. No preambles.
- Bias for action on mild ambiguity — make the reasonable assumption, note it in the final reply. Stop only on user-resolvable blockers (missing creds, destructive op, external account).
- Read before writing. Minimal changes. Test after every change. Show file paths clearly.
- Be concise.

### Memory + history — use liberally

- Start of non-trivial task → ` + "`memory_search`" + ` and ` + "`history_search`" + ` with one or two single-concept terms (NOT a sentence).
- User says "we", "last time", "before", "earlier", "the X we built" → ` + "`history_get`" + ` immediately for "previous/last session", "yesterday", "scroll back". Use ` + "`history_search`" + ` for topic references. Walk older sessions via ` + "`prev_session_id`" + `. Never invent a keyword.
- Surprising codebase state → ` + "`history_search`" + ` before assuming.
- Learned something durable (preference, convention, gotcha, decision rationale) → ` + "`memory_upsert`" + ` unprompted. ` + "`core=true`" + ` only if every-session relevant.
- Finished meaningful work → upsert a brief what+why.
- Asked about memories or past context → ` + "`memory_list`" + ` or ` + "`memory_search`" + ` first; never answer from training data.
- Search tools take ` + "`terms[]`" + `: ONE concept per array element. ` + "`terms=[\"JWT\"]`" + ` good. ` + "`terms=[\"JWT auth token spec\"]`" + ` bad — split into separate elements, or rely on multi-word elements being matched as a phrase.
- Never use bash for memories or chat history.

Better to over-query than fabricate. Cheap call, big payoff.

### prune_tool_result — keep context lean

Prune as soon as a result has served its purpose: big file reads after extraction, wide grep/find/ls dumps after picking the file, verbose passing build/test output, exploration dead-ends. Skip: results <500B (refused), errors (refused), anything you may re-read this turn. Always pass a short ` + "`reason`" + `.

## bash tips

File reading — check size first:
  ls -la path/           # directory
  wc -l file.go          # single file: under 150: cat; 150-500: sed -n; over 500: rg
Search: rg 'pattern' path
Find:   fd 'pattern' or find . -name '*.go'
Edit:   sd 'old' 'new' file or sed -i 's/old/new/g' file
Test:   go test ./...

## shell3 self-config

When the user asks about configuring or extending shell3 itself — adding models/providers, editing personas, registering tools, writing skills, hooks, secrets, db layout — read ` + "`shell3 docs`" + ` (source: ` + "`cmd/shell3/shell3.md`" + `) first. Project config lives in ` + "`.shell3/personas/`" + `, ` + "`.shell3/skills/`" + `, ` + "`.shell3/tools/`" + `, ` + "`.shell3/hooks/`" + `. Read the docs, then act.
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
		filepath.Join(shell3Dir, ".gitignore"):                 defaultGitignore,
		filepath.Join(shell3Dir, "personas", "base.md"):        codePersonaTemplate,
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
