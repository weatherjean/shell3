---
name: testing
description: How to write and run tests for this Go codebase
---

# Testing Guide

## Running tests

- **All tests:** `go test ./...`
- **Single package:** `go test ./internal/store/`
- **With verbose output:** `go test -v ./internal/config/`
- **Run a specific test:** `go test -run TestStore_MemoryStoreAndSearch ./internal/store/`
- **With race detector:** `go test -race ./...`
- **Smoke test (needs Ollama):** `go test ./test/ -tags smoke -v`

Always run `go vet ./... && go test ./...` after any code change.

## Test packages

| Package | Path | What it tests |
|---------|------|--------------|
| `config` | `internal/config/` | Project config loading, credential validation, auth workflow |
| `store` | `internal/store/` | SQLite store: memory CRUD, history append/search, session lifecycle |
| `skills` | `internal/skills/` | Skill file loading from disk, frontmatter parsing, prompt section building |
| `scaffold` | `internal/scaffold/` | `shell3 init` — directory creation, config scaffolding, .gitignore |
| `hooks` | `internal/hooks/` | Hook runner: allow/block, context build hook, TTY release/restore |
| `output` | `internal/output/` | Plain and JSONL emitter output formatting |
| `personality` | `internal/personality/` | Personality builder: tool sets, system prompt, store tool toggling |
| `llm` | `internal/llm/` | Client construction smoke test |
| `chat` | `internal/chat/` | **No tests yet** — session loop, tool dispatch, turn parsing |
| `tui` | `internal/tui/` | **No tests yet** — Bubble Tea TUI model, rendering |
| smoke | `test/` | End-to-end: builds binary, runs `shell3 say`, checks output |

## Conventions

- Use `t.TempDir()` for all filesystem tests — never write to the real project tree or home dir.
- For store tests, open a DB in `t.TempDir()` and `defer st.Close()`.
- For config/scaffold tests, create a fake home dir with `.shell3/credentials.yaml` to avoid hitting the real `~/.shell3`.
- Hook tests write shell scripts to `t.TempDir()` with `os.WriteFile` and mark them executable (`0755`).
- Prefer table-driven tests for multi-case inputs (e.g. validation, personality tool sets).
- Test file-per-package: each package `internal/foo/` has `foo_test.go` in the same directory.
- Use `_test` package suffix (e.g. `package config_test`) so tests import the package from outside — this catches the public API surface.
- Error messages: lowercase, no trailing period (Go convention).

## What to test when editing code

- **Adding a new tool/function?** Write a test that exercises the public function with valid and invalid inputs.
- **Changing a config field?** Add a test case in `internal/config/config_test.go` that exercises the new field.
- **Changing store schema?** Add a test in `internal/store/store_test.go` that covers the new column/table.
- **Adding a new hook event?** Add a test in `internal/hooks/hooks_test.go` following the `TestHookAllow`/`TestHookBlock` pattern.
- **Adding a new personality type or tool toggle?** Add a test in `internal/personality/personality_test.go` that checks tool names.
- **Changing the TUI or chat loop?** These are harder to unit test — write a smoke test instead.

## Writing a smoke test

Smoke tests live in `test/` and use the `//go:build smoke` build tag. They require a built binary and a running provider (e.g. Ollama). Pattern:

```go
//go:build smoke

package test

import (
    "os/exec"
    "testing"
)

func TestSmoke_YourFeature(t *testing.T) {
    // Build if needed, write temp config, run shell3, check output
}
```

Run with: `go test ./test/ -tags smoke -v`

## Key test patterns

### Temp filesystem setup
```go
dir := t.TempDir()
shell3Dir := filepath.Join(dir, ".shell3")
os.MkdirAll(shell3Dir, 0755)
os.WriteFile(filepath.Join(shell3Dir, "config.yaml"), []byte(yaml), 0644)
```

### Fake credentials for tests needing auth
```go
homeDir := t.TempDir()
shell3Dir := filepath.Join(homeDir, ".shell3")
os.MkdirAll(shell3Dir, 0700)
os.WriteFile(filepath.Join(shell3Dir, "credentials.yaml"), []byte(credsYAML), 0600)
```

### Store open/close pattern
```go
st, err := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
if err != nil { t.Fatal(err) }
defer st.Close()
```

### Hook script pattern
```go
script := filepath.Join(t.TempDir(), "hook.sh")
os.WriteFile(script, []byte("#!/bin/bash\necho '{\"action\":\"allow\"}'"), 0755)
```
