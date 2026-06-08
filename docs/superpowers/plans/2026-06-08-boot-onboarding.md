# Boot Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace silent first-run auto-bootstrap of `shell3.lua` with an explicit `shell3 boot` command that writes a clean split-file base config, and preserve removed features in a repo cookbook.

**Architecture:** `EnsureGlobal` stops writing config; missing config fails with a `boot` redirect. A new `scaffold.RenderBaseConfig` renders an embedded `defaults/base/` tree (templated `shell3.lua` + verbatim `lib/` modules) into `~/.shell3/`. `cmd/shell3/boot.go` collects url/model/name/key (+ optional proxy & Brave key) via prompts or flags, renders the tree, and merges `.env`. Removed reference content moves to `docs/cookbook/`.

**Tech Stack:** Go (cobra, `embed`, `text/template`, `golang.org/x/term`), gopher-lua config.

Spec: `docs/superpowers/specs/2026-06-08-boot-onboarding-design.md`.

**Working branch:** `feat/boot-onboarding` (already created; the spec is committed there).

---

### Task 1: Stop auto-writing config; redirect to `boot`

**Files:**
- Modify: `internal/bootstrap/bootstrap.go:16-31` (`EnsureGlobal`), `:132-137` (`globalGitignoreAddition`)
- Modify: `internal/agentsetup/agentsetup.go:346` (error message)
- Test: `internal/bootstrap/bootstrap_test.go` (add case)

- [ ] **Step 1: Write the failing test**

Add to `internal/bootstrap/bootstrap_test.go`:

```go
func TestEnsureGlobalDoesNotWriteConfig(t *testing.T) {
	home := t.TempDir()
	g := paths.NewGlobal(home)
	if err := EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, "shell3.lua")); !os.IsNotExist(err) {
		t.Fatalf("EnsureGlobal must not write shell3.lua; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, ".env.example")); !os.IsNotExist(err) {
		t.Fatalf("EnsureGlobal must not write .env.example; stat err = %v", err)
	}
	// It must still create the dirs and gitignore.
	if _, err := os.Stat(g.Projects); err != nil {
		t.Fatalf("projects dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, ".gitignore")); err != nil {
		t.Fatalf("gitignore missing: %v", err)
	}
}
```

Ensure imports include `os`, `path/filepath`, and `github.com/weatherjean/shell3/internal/paths`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/ -run TestEnsureGlobalDoesNotWriteConfig -v`
Expected: FAIL — `shell3.lua` exists because `EnsureGlobal` still writes it.

- [ ] **Step 3: Remove the scaffold write from `EnsureGlobal`**

In `internal/bootstrap/bootstrap.go`, replace the body of `EnsureGlobal` so it no longer writes the starter config:

```go
// EnsureGlobal creates ~/.shell3/ (and its projects/ dir) and the global
// .gitignore if missing. It does NOT write any shell3.lua — config is created
// explicitly via `shell3 boot`.
func EnsureGlobal(g paths.Global) error {
	for _, dir := range []string{g.Root, g.Projects} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("bootstrap: mkdir %s: %w", dir, err)
		}
	}
	if err := ensureGlobalGitignore(g); err != nil {
		return fmt.Errorf("bootstrap: global gitignore: %w", err)
	}
	return nil
}
```

Remove the now-unused `scaffold` import from `bootstrap.go`'s import block.

- [ ] **Step 4: Add `.env` to the global gitignore addition**

In `internal/bootstrap/bootstrap.go`, update `globalGitignoreAddition` so a dotfiles-tracked `~/.shell3/` never commits the secrets file `boot` writes:

```go
const globalGitignoreAddition = `# shell3 — never commit these even in a dotfiles repo
ai-do-not-read.*
.env
shell3.log
shell3.log.*
projects/
`
```

- [ ] **Step 5: Update the no-config error message**

In `internal/agentsetup/agentsetup.go`, change the final return of `ResolveConfigPath` (line ~346):

```go
	return "", fmt.Errorf("no shell3.lua found — run 'shell3 boot' to create one (or pass --config)")
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/bootstrap/ ./internal/agentsetup/ -v`
Expected: PASS (the build will still reference `scaffold.WriteStarterConfig` nowhere; if any test referenced it, it is removed in Task 2).

Note: `go build ./...` will fail until Task 2 if the old `scaffold` package still embeds the deleted file — but at this point nothing is deleted yet, so the build stays green. Verify: `go build ./...`.

- [ ] **Step 7: Commit**

```bash
git add internal/bootstrap/ internal/agentsetup/
git commit -m "feat(bootstrap): stop auto-writing shell3.lua; redirect to 'shell3 boot'"
```

---

### Task 2: Embedded `defaults/base/` tree + `RenderBaseConfig`

**Files:**
- Create: `internal/scaffold/defaults/base/shell3.lua.tmpl`
- Create: `internal/scaffold/defaults/base/lib/tools.lua`
- Create: `internal/scaffold/defaults/base/lib/guards.lua`
- Create: `internal/scaffold/defaults/base/lib/skills/brainstorming.lua`
- Create: `internal/scaffold/defaults/base/lib/skills/subagents.lua`
- Rewrite: `internal/scaffold/scaffold.go`
- Test: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Create `lib/tools.lua`** (returns the two example tools)

Copy the **exact** `web_fetch` block (`internal/scaffold/defaults/shell3.lua:34-90`, the `local web_fetch = shell3.tool({ ... })` statement) and the **exact** `brave_search` block (`:93-131`) into the new file, changing the two `local NAME = ` declarations to plain locals and adding a `return` table at the end:

```lua
-- lib/tools.lua — example custom tools. Returned for require() in shell3.lua.
local web_fetch = shell3.tool({
  -- …paste the full web_fetch definition from the old reference (lines 34-90)…
})

local brave_search = shell3.tool({
  -- …paste the full brave_search definition from the old reference (lines 93-131)…
})

return { web_fetch = web_fetch, brave_search = brave_search }
```

Preserve every line of the two handlers verbatim (HTML stripping, link dedup, curl+jq). `brave_search` already returns a friendly error when `BRAVE_API_KEY` is empty (`:116`), so an empty placeholder key is safe.

- [ ] **Step 2: Create `lib/guards.lua`**

```lua
-- lib/guards.lua — on_tool_call guards. Returned for require() in shell3.lua.
local function no_env_edit(call)
  local tool   = call.tool or ""
  local params = call.params or {}
  if tool == "edit_file" then
    local path = tostring(params.file_path or "")
    if path:match("%.env$") then
      return { action = "block", reason = "editing .env files is not allowed; manage secrets manually" }
    end
  end
  return { action = "allow" }
end

return { no_env_edit = no_env_edit }
```

- [ ] **Step 3: Create `lib/skills/subagents.lua`** (verbatim port)

Copy the **exact** `shell3.skill({ ... })` block from `internal/scaffold/defaults/shell3.lua:490-572` (the `spawning-subagents` skill), converting `local skill_spawning_subagents = shell3.skill({` into a `return shell3.skill({`:

```lua
-- lib/skills/subagents.lua — the spawning-subagents skill. Returned for require().
return shell3.skill({
  name        = "spawning-subagents",
  description = "Delegate independent sub-tasks to fresh shell3 processes running in parallel via bash_bg.",
  body        = [[
-- …paste the full body from the old reference (lines 493-571), unchanged…
]],
})
```

- [ ] **Step 4: Create `lib/skills/brainstorming.lua`** (new content)

```lua
-- lib/skills/brainstorming.lua — design-first skill for the plan agent. Returned for require().
return shell3.skill({
  name        = "brainstorming",
  description = "Turn a rough idea into an agreed design through one-question-at-a-time dialogue, then write a saved design doc. Use before any non-trivial feature, behavior change, or new component.",
  body        = [[
---
name: brainstorming
description: Use before any non-trivial feature, behavior change, or new component. Turns a rough idea into an agreed design through one-question-at-a-time dialogue, then writes a saved design doc.
---

# Brainstorming ideas into designs

Turn a rough idea into a clear, agreed design through collaborative dialogue — then write it down. Do this before implementation for any non-trivial change.

## Hard gate

Do NOT write code, edit files, or start implementing until you have presented a design and the user has approved it. This applies to every task regardless of how simple it looks. "Too simple to need a design" is where unexamined assumptions cost the most. The design can be three sentences for a tiny change — but present it and get a yes first.

## Process

1. **Explore context first.** Read the relevant files, docs, and recent commits before asking anything. Ground your questions in what actually exists.
2. **Ask questions one at a time.** One question per message. Prefer multiple-choice when you can; open-ended is fine. Focus on purpose, constraints, and success criteria — not implementation trivia. Keep going until you understand what you are building.
3. **Scope check.** If the idea is really several independent subsystems, say so early and help split it into separate pieces, each with its own design. Do not refine details of something that should be decomposed first.
4. **Propose 2-3 approaches.** Lead with your recommendation and say why. Give the trade-offs honestly.
5. **Present the design in sections.** Scale each section to its complexity — a sentence or two when straightforward, more when nuanced. Cover architecture, components, data flow, error handling, and testing. Ask after each section whether it looks right before moving on.
6. **Design for isolation.** Break the system into small units, each with one clear purpose and a well-defined interface, understandable and testable on its own. For each unit you should be able to say what it does, how it is used, and what it depends on. When a file would grow large, that is a signal it is doing too much.
7. **Work with the grain of the codebase.** Follow existing patterns. Improve code you are already touching when it has problems that affect the work; do not go off on unrelated refactors.

## After approval

- Write the agreed design to `docs/specs/YYYY-MM-DD-<topic>.md` (or the project's conventional spec location). Be concrete: no "TBD", no contradictions, no requirement that could be read two ways.
- If the project is a git repo, commit the design doc.
- Then tell the user: the design is saved at the path; switch to the `code` agent (press Tab when idle, or use `/agent`) to implement it.

## Key principles

- One question at a time — do not overwhelm.
- YAGNI ruthlessly — cut features that are not needed.
- Always explore 2-3 approaches before settling.
- Validate incrementally — present, get approval, then move on.
- Be willing to go back when something does not fit.
]],
})
```

- [ ] **Step 5: Create `shell3.lua.tmpl`** (the templated main file)

```lua
-- shell3.lua — base config written by `shell3 boot`. Edit freely.
-- Secrets live in .env beside this file (never commit it).
-- Add tools, skills, MCP servers, or agents by editing — see the cookbook in
-- the shell3 repo under docs/cookbook/.

local tools  = require("lib.tools")            -- { web_fetch, brave_search }
local guards = require("lib.guards")           -- { no_env_edit }
local brainstorming = require("lib.skills.brainstorming")
local subagents     = require("lib.skills.subagents")

-- ---------------------------------------------------------------------------
-- Model
-- ---------------------------------------------------------------------------
shell3.model("{{.Name}}", {
  base_url       = "{{.BaseURL}}",
  api_key        = shell3.env.secret("{{.EnvKey}}"),  -- {{.EnvKey}} in .env
  model          = "{{.Model}}",
  context_window = 128000,
  -- reasoning   = "medium",   -- uncomment if your model supports reasoning effort
  -- run_proxy: shell command auto-started (detached, fire-and-forget) the first
  -- time an agent uses this model. Use it to bring up a local proxy in front of
  -- base_url — e.g. a Codex subscription fronted by `npx ...`, or opencode-go.
  -- Output goes to ./.shell3/proxy-{{.Name}}.log.
{{if .Proxy}}  run_proxy      = "{{.Proxy}}",
{{else}}  -- run_proxy   = "npx @some/codex-proxy --port 8787",
{{end}}})

-- ---------------------------------------------------------------------------
-- Agents
-- ---------------------------------------------------------------------------
shell3.agent({
  name  = "code",
  model = "{{.Name}}",
  prompt = [[
You are an expert coding assistant inside shell3. Work autonomously as a senior pair-programmer: inspect, edit, test, and summarize clearly.

## Default workflow
- Understand the request, inspect relevant files, make minimal changes, format, validate, then summarize.
- For non-trivial work, design first with the `plan` agent (Tab or `/agent`); implement once the design is agreed.
- Bias for action on mild ambiguity; ask only for user-resolvable blockers: missing credentials, destructive operations, external account access, or unclear handling of existing user work.
- Read before writing. Prefer targeted edits. Show file paths clearly.
- Format and validate with the project's standard tools before considering work complete.
- Commit only when explicitly asked; push only when explicitly asked. Be concise.

## Tools
- `bash` / `shell_interactive`: prefer `bash`; use `shell_interactive` only for truly interactive programs.
- `edit_file`: prefer over `bash` heredocs; empty `old_string` creates or overwrites the whole file.
- `bash_bg`: long or parallel background work.
- `web_fetch`: fetch a URL as plain text + links. `brave_search`: web search (needs BRAVE_API_KEY in .env).
- `prune_tool_result` / `compact_history`: keep context clean; never compact without explicit user approval.

## Skills
- `spawning-subagents`: read it and follow it when delegating an independent sub-task to a fresh parallel shell3 process.

## Context hygiene
- Prune large successful outputs after extracting what you need; never prune errors or small results.
- When context is above 50%, offer to prune or compact rather than proceeding silently.
]],
  tools = {
    bash              = true,
    bash_bg           = true,
    shell_interactive = true,
    edit              = true,
    history           = true,
    prune             = true,
    compact           = true,
    media             = true,
    custom            = { tools.web_fetch, tools.brave_search },
  },
  skills = { subagents },
  on_tool_call = { guards.no_env_edit },
})

shell3.agent({
  name  = "plan",
  model = "{{.Name}}",
  prompt = [[
You are the design partner inside shell3. You do NOT edit files — you investigate, ask, and design. Your job is to turn rough ideas into clear, agreed designs.

## How you work
- Lead with the `brainstorming` skill: read it and follow it for any non-trivial feature, behavior change, or new component.
- Explore the relevant code, docs, and recent commits before asking anything — ground every question in what exists.
- Ask questions one at a time, multiple-choice when you can. Focus on purpose, constraints, and success criteria.
- Propose 2-3 approaches with trade-offs and a recommendation. Present the design in sections and get approval as you go.
- End by writing a saved design doc, then tell the user to switch to the `code` agent (Tab or `/agent`) to implement.

## Tools
- `bash`: read-only investigation — `rg`, `fd`, `cat`, `git log`. You have no `edit_file`; do not try to change files.
- `web_fetch` / `brave_search`: pull in external references when a design needs them.
- `prune_tool_result` / `compact_history`: keep context clean; never compact without explicit user approval.

## Context hygiene
- Prune large successful outputs after extracting what you need; keep errors and small results.
- When context is above 50%, offer to prune or compact rather than proceeding silently.
]],
  tools = {
    bash    = true,
    bash_bg = false,
    edit    = false,
    history = true,
    prune   = true,
    compact = true,
    media   = true,
    custom  = { tools.web_fetch, tools.brave_search },
  },
  skills = { brainstorming },
  on_tool_call = { guards.no_env_edit },
})
```

- [ ] **Step 6: Write the failing scaffold test**

Replace `internal/scaffold/scaffold_test.go` (or create it) with:

```go
package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderBaseConfig(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "kimi-k2.6", Proxy: ""}
	if err := RenderBaseConfig(dir, v); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}

	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.lua"))
	if err != nil {
		t.Fatalf("read shell3.lua: %v", err)
	}
	for _, want := range []string{
		`shell3.model("main"`,
		`base_url       = "http://localhost:8787/v1"`,
		`shell3.env.secret("MAIN_API_KEY")`,
		`model          = "kimi-k2.6"`,
		`name  = "code"`,
		`name  = "plan"`,
		`-- run_proxy   = "npx`, // commented when no proxy given
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.lua missing %q", want)
		}
	}
	if strings.Contains(string(cfg), "{{") {
		t.Errorf("shell3.lua still contains an unrendered template delimiter")
	}

	// The static lib tree must be copied verbatim.
	for _, p := range []string{
		"lib/tools.lua", "lib/guards.lua",
		"lib/skills/brainstorming.lua", "lib/skills/subagents.lua",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestRenderBaseConfigWithProxy(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Proxy: "npx codex-proxy --port 8787"}
	if err := RenderBaseConfig(dir, v); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.lua"))
	if !strings.Contains(string(cfg), `run_proxy      = "npx codex-proxy --port 8787"`) {
		t.Errorf("proxy not wired into shell3.lua:\n%s", cfg)
	}
}

func TestRenderBaseConfigDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v); err != nil {
		t.Fatalf("first render: %v", err)
	}
	// Tamper, then re-render: writeIfAbsent must leave existing files untouched.
	cfgPath := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(cfgPath, []byte("-- user edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v); err != nil {
		t.Fatalf("second render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != "-- user edited\n" {
		t.Errorf("RenderBaseConfig clobbered an existing shell3.lua")
	}
}
```

- [ ] **Step 7: Run test to verify it fails**

Run: `go test ./internal/scaffold/ -v`
Expected: FAIL — `Values` / `RenderBaseConfig` undefined.

- [ ] **Step 8: Rewrite `scaffold.go`**

Replace `internal/scaffold/scaffold.go` entirely:

```go
// Package scaffold renders the split-file base shell3 configuration that
// `shell3 boot` writes for a new install.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed all:defaults/base
var baseFS embed.FS

const baseRoot = "defaults/base"

// Values are the user-supplied substitutions for the templated shell3.lua.
type Values struct {
	Name    string // model handle, e.g. "main"
	BaseURL string // OpenAI-compatible endpoint
	EnvKey  string // .env key holding the API key, e.g. "MAIN_API_KEY"
	Model   string // model tag/id
	Proxy   string // optional run_proxy command ("" => commented out)
}

// RenderBaseConfig writes the base config tree into dir: shell3.lua rendered
// from the embedded template with v, plus the verbatim lib/ modules. Existing
// files are never overwritten (writeIfAbsent), so it is safe to re-run.
func RenderBaseConfig(dir string, v Values) error {
	// 1. Render shell3.lua from the template.
	tmplBytes, err := baseFS.ReadFile(baseRoot + "/shell3.lua.tmpl")
	if err != nil {
		return fmt.Errorf("scaffold: read template: %w", err)
	}
	t, err := template.New("shell3.lua").Parse(string(tmplBytes))
	if err != nil {
		return fmt.Errorf("scaffold: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, v); err != nil {
		return fmt.Errorf("scaffold: execute template: %w", err)
	}
	if err := writeIfAbsent(filepath.Join(dir, "shell3.lua"), buf.Bytes(), 0644); err != nil {
		return err
	}

	// 2. Copy the static lib/ tree verbatim.
	return fs.WalkDir(baseFS, baseRoot+"/lib", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(baseRoot, p)
		if err != nil {
			return err
		}
		content, err := baseFS.ReadFile(p)
		if err != nil {
			return err
		}
		return writeIfAbsent(filepath.Join(dir, rel), content, 0644)
	})
}

func writeIfAbsent(path string, content []byte, mode fs.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("scaffold: stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, content, mode)
}
```

- [ ] **Step 9: Run tests to verify they pass**

Run: `go test ./internal/scaffold/ -v`
Expected: PASS (all three tests). Also run `go build ./...` — passes (old `defaults/shell3.lua` still embedded? No: the new `scaffold.go` no longer embeds it, but the file still exists on disk harmlessly; it is deleted in Task 5).

- [ ] **Step 10: Sanity-check the generated config loads**

Run:
```bash
TMP=$(mktemp -d)
cat > /tmp/render_check.go <<'EOF'
//go:build ignore
EOF
# Render via the test path instead: write a tiny throwaway main is overkill —
# instead verify by hand after Task 3's boot exists. For now just confirm the
# lua parses with luac is not available; rely on Task 3 end-to-end check.
echo "deferred to Task 3 end-to-end"
```
Expected: real load verification happens in Task 3 / Task 6.

- [ ] **Step 11: Commit**

```bash
git add internal/scaffold/
git commit -m "feat(scaffold): embedded split-file base config + RenderBaseConfig"
```

---

### Task 3: `shell3 boot` command

**Files:**
- Create: `cmd/shell3/boot.go`
- Modify: `cmd/shell3/main.go:24-27` (register subcommand)
- Test: `cmd/shell3/boot_test.go`

- [ ] **Step 1: Write the failing test for `.env` merge**

Create `cmd/shell3/boot_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestMergeEnvAddsMissingKeysOnly(t *testing.T) {
	existing := "FOO=bar\nMAIN_API_KEY=old\n"
	out := mergeEnv(existing, [][2]string{
		{"MAIN_API_KEY", "new"},   // already present -> must NOT change
		{"BRAVE_API_KEY", "xyz"},  // missing -> append
	})
	if !strings.Contains(out, "MAIN_API_KEY=old") {
		t.Errorf("must not overwrite existing key; got:\n%s", out)
	}
	if strings.Contains(out, "MAIN_API_KEY=new") {
		t.Errorf("must not append a duplicate for an existing key; got:\n%s", out)
	}
	if !strings.Contains(out, "BRAVE_API_KEY=xyz") {
		t.Errorf("must append missing key; got:\n%s", out)
	}
	if !strings.Contains(out, "FOO=bar") {
		t.Errorf("must preserve unrelated keys; got:\n%s", out)
	}
}

func TestMergeEnvFromEmpty(t *testing.T) {
	out := mergeEnv("", [][2]string{{"MAIN_API_KEY", "k"}, {"BRAVE_API_KEY", ""}})
	if !strings.Contains(out, "MAIN_API_KEY=k") || !strings.Contains(out, "BRAVE_API_KEY=") {
		t.Errorf("missing expected keys; got:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("env file must end with newline; got:\n%q", out)
	}
}

func TestEnvKeyForName(t *testing.T) {
	if got := envKeyForName("main"); got != "MAIN_API_KEY" {
		t.Errorf("envKeyForName(main) = %q, want MAIN_API_KEY", got)
	}
	if got := envKeyForName("kimi-k2"); got != "KIMI_K2_API_KEY" {
		t.Errorf("envKeyForName(kimi-k2) = %q, want KIMI_K2_API_KEY", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shell3/ -run 'TestMergeEnv|TestEnvKeyForName' -v`
Expected: FAIL — `mergeEnv` / `envKeyForName` undefined.

- [ ] **Step 3: Implement `boot.go`**

Create `cmd/shell3/boot.go`:

```go
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/scaffold"
)

type bootFlags struct {
	url, model, name, key, proxy, braveKey string
	force                                  bool
}

func newBootCommand() *cobra.Command {
	f := &bootFlags{}
	cmd := &cobra.Command{
		Use:   "boot",
		Short: "Create a shell3 config interactively (url, model, name, key)",
		RunE:  func(cmd *cobra.Command, args []string) error { return runBoot(f) },
	}
	cmd.Flags().StringVar(&f.url, "url", "", "Base URL (OpenAI-compatible endpoint)")
	cmd.Flags().StringVar(&f.model, "model", "", "Model tag/id")
	cmd.Flags().StringVar(&f.name, "name", "", "Handle for this model (default: main)")
	cmd.Flags().StringVar(&f.key, "key", "", "API key")
	cmd.Flags().StringVar(&f.proxy, "proxy", "", "Optional run_proxy command")
	cmd.Flags().StringVar(&f.braveKey, "brave-key", "", "Optional Brave Search API key")
	cmd.Flags().BoolVar(&f.force, "force", false, "Overwrite an existing ~/.shell3/shell3.lua")
	return cmd
}

func runBoot(f *bootFlags) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("boot: home dir: %w", err)
	}
	dir := filepath.Join(home, ".shell3")
	cfgPath := filepath.Join(dir, "shell3.lua")

	if _, err := os.Stat(cfgPath); err == nil && !f.force {
		return fmt.Errorf("boot: %s already exists — pass --force to overwrite", cfgPath)
	}

	in := bufio.NewReader(os.Stdin)
	tty := term.IsTerminal(int(os.Stdin.Fd()))

	url, err := value(f.url, "Base URL", "https://api.openai.com/v1", in, tty, false)
	if err != nil {
		return err
	}
	model, err := value(f.model, "Model tag", "", in, tty, true)
	if err != nil {
		return err
	}
	name, err := value(f.name, "Name (handle for this model)", "main", in, tty, false)
	if err != nil {
		return err
	}
	key, err := secret(f.key, "API key", in, tty, true)
	if err != nil {
		return err
	}

	if tty {
		fmt.Println()
		fmt.Println("Local proxy? Some endpoints are a proxy you launch yourself —")
		fmt.Println("e.g. a Codex subscription fronted by `npx ...`, or opencode-go.")
		fmt.Println("shell3 can auto-start it on activation (run_proxy).")
	}
	proxy, err := value(f.proxy, "Proxy command (blank to skip)", "", in, tty, false)
	if err != nil {
		return err
	}
	braveKey, err := secret(f.braveKey, "Brave Search key (blank to add later)", in, tty, false)
	if err != nil {
		return err
	}

	envKey := envKeyForName(name)

	if err := scaffold.RenderBaseConfig(dir, scaffold.Values{
		Name: name, BaseURL: url, EnvKey: envKey, Model: model, Proxy: proxy,
	}); err != nil {
		return err
	}

	// Merge .env: add the model key and a Brave placeholder if absent.
	envPath := filepath.Join(dir, ".env")
	existing, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("boot: read .env: %w", err)
	}
	merged := mergeEnv(string(existing), [][2]string{
		{envKey, key},
		{"BRAVE_API_KEY", braveKey},
	})
	if err := os.WriteFile(envPath, []byte(merged), 0600); err != nil {
		return fmt.Errorf("boot: write .env: %w", err)
	}

	printBootSuccess(dir, cfgPath, envPath, proxy != "")
	return nil
}

// envKeyForName derives the .env key for a model handle: uppercased, with any
// non-alphanumeric run collapsed to a single underscore, suffixed _API_KEY.
func envKeyForName(name string) string {
	s := nonAlnum.ReplaceAllString(strings.ToUpper(name), "_")
	s = strings.Trim(s, "_")
	return s + "_API_KEY"
}

var nonAlnum = regexp.MustCompile(`[^A-Z0-9]+`)

// mergeEnv appends each key=value from kv to existing only if the key is not
// already present as its own line. Existing values are never changed. The
// result always ends with a trailing newline.
func mergeEnv(existing string, kv [][2]string) string {
	have := map[string]bool{}
	for _, line := range strings.Split(existing, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, _, ok := strings.Cut(line, "="); ok {
			have[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = true
		}
	}
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	if existing == "" {
		b.WriteString("# shell3 secrets — never commit this file.\n")
	}
	for _, pair := range kv {
		if have[pair[0]] {
			continue
		}
		if pair[0] == "BRAVE_API_KEY" && pair[1] == "" {
			b.WriteString("# Brave Search API key — fill in to enable the brave_search tool.\n")
		}
		b.WriteString(pair[0] + "=" + pair[1] + "\n")
	}
	return b.String()
}

func printBootSuccess(dir, cfgPath, envPath string, proxyWired bool) {
	fmt.Println()
	fmt.Println("shell3 is configured.")
	fmt.Printf("  config:  %s\n", cfgPath)
	fmt.Printf("  modules: %s\n", filepath.Join(dir, "lib"))
	fmt.Printf("  secrets: %s  (never commit this)\n", envPath)
	if proxyWired {
		fmt.Println("  proxy:   run_proxy wired — shell3 starts it when the model is first used.")
	} else {
		fmt.Println("  proxy:   none. If your endpoint is a proxy you launch (e.g. a Codex")
		fmt.Println("           subscription via `npx ...`), add run_proxy to the model block.")
	}
	fmt.Println()
	fmt.Println("Edit shell3.lua (and lib/) to add tools, skills, MCP, or agents —")
	fmt.Println("recipes live in the shell3 repo under docs/cookbook/.")
	fmt.Println()
	fmt.Println(`Run:  shell3 "hello"`)
}

// value reads a config value: flag wins; else prompt (TTY) with optional
// default; errors when required and unavailable.
func value(flag, label, def string, in *bufio.Reader, tty, required bool) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if !tty {
		if required {
			return "", fmt.Errorf("boot: --%s required when stdin is not a terminal", strings.ToLower(strings.Fields(label)[0]))
		}
		return def, nil
	}
	prompt := label
	if def != "" {
		prompt += " [" + def + "]"
	}
	fmt.Printf("  %s: ", prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

// secret reads a value without echoing it.
func secret(flag, label string, in *bufio.Reader, tty, required bool) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if !tty {
		if required {
			return "", fmt.Errorf("boot: --%s required when stdin is not a terminal", strings.ToLower(strings.Fields(label)[0]))
		}
		return "", nil
	}
	fmt.Printf("  %s: ", label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
```

- [ ] **Step 4: Register the subcommand in `main.go`**

In `cmd/shell3/main.go`, after `root.Flags().AddFlagSet(runCmd.Flags())` (line 27), add:

```go
	root.AddCommand(newBootCommand())
```

- [ ] **Step 5: Run unit tests**

Run: `go test ./cmd/shell3/ -run 'TestMergeEnv|TestEnvKeyForName' -v`
Expected: PASS.

- [ ] **Step 6: Build and end-to-end smoke test in a temp HOME**

Run:
```bash
go build ./... && go build -o /tmp/shell3bin ./cmd/shell3
HOME=$(mktemp -d) /tmp/shell3bin boot \
  --url http://localhost:8787/v1 --model test-model --name main \
  --key sk-test --proxy "" --brave-key ""
```
Expected: success output; then verify the generated config parses by loading it (the model has a fake key/url so a real chat would fail at the network, but config load must succeed):
```bash
HOME=$HOME /tmp/shell3bin --config $HOME/.shell3/shell3.lua "noop" 2>&1 | head -20
```
Expected: it gets past config load (no Lua/parse error; any error should be network/auth, not "config:" or "no shell3.lua found"). Inspect `$HOME/.shell3/shell3.lua` and `$HOME/.shell3/.env` look right.

- [ ] **Step 7: Commit**

```bash
git add cmd/shell3/
git commit -m "feat(cmd): 'shell3 boot' interactive onboarding command"
```

---

### Task 4: Cookbook

**Files:**
- Create: `docs/cookbook/README.md`
- Create: `docs/cookbook/lib/skills/writing-plans.lua`, `executing-plans.lua`, `codebase-discovery.lua`, `web-search.lua`
- Create: `docs/cookbook/lib/mcp.lua`, `docs/cookbook/lib/guards.lua`, `docs/cookbook/lib/extra-agents.lua`, `docs/cookbook/lib/tools.lua`
- Create: `docs/cookbook/proxy.md`

(No automated tests — docs. The lua files are reference recipes, not loaded by the build.)

- [ ] **Step 1: Extract the four dropped skills as drop-in modules**

For each skill below, copy the **exact** `shell3.skill({ ... })` block from `internal/scaffold/defaults/shell3.lua` (which still exists until Task 5) into its own cookbook file, converting `local skill_X = shell3.skill({` into `return shell3.skill({`:

- `docs/cookbook/lib/skills/writing-plans.lua` ← lines 154-291 (`writing-plans`)
- `docs/cookbook/lib/skills/executing-plans.lua` ← lines 292-364 (`executing-plans`)
- `docs/cookbook/lib/skills/codebase-discovery.lua` ← lines 365-441 (`codebase-discovery`)
- `docs/cookbook/lib/skills/web-search.lua` ← lines 442-489 (`web-search`)

Each file starts with a one-line comment, e.g. `-- cookbook: drop into ~/.shell3/lib/skills/ then require("lib.skills.writing-plans")`.

- [ ] **Step 2: Create `docs/cookbook/lib/mcp.lua`**

```lua
-- cookbook: declaring an MCP server. Drop into ~/.shell3/lib/, require it in
-- shell3.lua, and add `mcp = { chrome }` to an agent's tools block.
local chrome = shell3.mcp({
  name    = "chrome",
  command = "npx",
  args    = { "-y", "chrome-devtools-mcp@latest", "--autoConnect", "--no-usage-statistics" },
  -- tools = { "navigate_page", "click", "take_snapshot" }, -- optional allowlist
})

return { chrome = chrome }
```

- [ ] **Step 3: Create `docs/cookbook/lib/guards.lua`** (extra guard recipes)

```lua
-- cookbook: extra on_tool_call guards. Drop into ~/.shell3/lib/, require, and add
-- to an agent's on_tool_call = { ... }.

-- Block obviously destructive bash.
local function block_destructive_bash(call)
  if (call.tool or "") == "bash" then
    local cmd = tostring((call.params or {}).command or "")
    if cmd:match("rm%s+%-rf%s+/") or cmd:match("git%s+push%s+.-%-%-force") then
      return { action = "block", reason = "destructive command blocked by guard" }
    end
  end
  return { action = "allow" }
end

return { block_destructive_bash = block_destructive_bash }
```

- [ ] **Step 4: Create `docs/cookbook/lib/tools.lua`** and `extra-agents.lua`

`docs/cookbook/lib/tools.lua` — a minimal custom-tool template:

```lua
-- cookbook: a custom tool template. Drop into ~/.shell3/lib/, require, add to an
-- agent's tools = { custom = { my_tool } }.
local my_tool = shell3.tool({
  name        = "my_tool",
  description = "What this tool does.",
  parameters  = {
    type = "object",
    properties = { arg = { type = "string", description = "An argument." } },
    required = { "arg" },
  },
  handler = function(args)
    return "you passed: " .. tostring(args.arg or "")
  end,
})

return { my_tool = my_tool }
```

`docs/cookbook/lib/extra-agents.lua`:

```lua
-- cookbook: a third agent. Declare additional agents in shell3.lua; switch with
-- Tab (when idle) or /agent. This file is illustrative — paste the block into
-- shell3.lua where `tools`, `guards`, and skills locals are in scope.
shell3.agent({
  name   = "review",
  model  = "main",
  prompt = [[ You review diffs for correctness and clarity. You do not edit. ]],
  tools  = { bash = true, edit = false, history = true, prune = true, compact = true },
})
```

- [ ] **Step 5: Create `docs/cookbook/proxy.md`**

```markdown
# run_proxy recipes

`run_proxy` is a shell command shell3 auto-starts (detached, fire-and-forget)
the first time an agent uses the model. Use it to bring up a local proxy in
front of `base_url`. Output goes to `./.shell3/proxy-<model>.log`. If a proxy is
already listening, the spawn just fails to bind and the first request decides.

## Codex subscription via npx

    run_proxy = "npx @some/codex-proxy --port 8787",
    base_url  = "http://localhost:8787/v1",

## opencode-go

    run_proxy = "opencode-go serve --port 8787",
    base_url  = "http://localhost:8787/v1",

## litellm

    run_proxy = "litellm --config ~/.shell3/litellm.yaml --port 8787",
    base_url  = "http://localhost:8787/v1",
```

- [ ] **Step 6: Create `docs/cookbook/README.md`** (index + usage)

```markdown
# shell3 cookbook

Drop-in recipes for features the base config (written by `shell3 boot`) leaves
out. Each `lib/...` file mirrors the base config's module layout: copy it into
`~/.shell3/lib/`, `require` it in `shell3.lua`, and wire it into an agent.

## Usage

    -- in ~/.shell3/shell3.lua
    local plans = require("lib.skills.writing-plans")
    local mcp   = require("lib.mcp")
    -- then, in an agent:
    --   skills = { plans },
    --   tools  = { mcp = { mcp.chrome } },

## Contents

- `lib/skills/writing-plans.lua` — planning/approval gate before non-trivial changes.
- `lib/skills/executing-plans.lua` — safe execution + git workflow after a plan.
- `lib/skills/codebase-discovery.lua` — navigating unfamiliar code.
- `lib/skills/web-search.lua` — guidance for web research.
- `lib/mcp.lua` — declaring an MCP server and attaching its tools.
- `lib/guards.lua` — extra on_tool_call guards (block destructive bash).
- `lib/tools.lua` — custom tool template.
- `lib/extra-agents.lua` — adding more agents.
- `proxy.md` — run_proxy recipes (Codex/npx, opencode-go, litellm).
```

- [ ] **Step 7: Commit**

```bash
git add docs/cookbook/
git commit -m "docs(cookbook): drop-in recipes for MCP, skills, guards, tools, proxy"
```

---

### Task 5: Remove the old reference; final verification

**Files:**
- Delete: `internal/scaffold/defaults/shell3.lua`, `internal/scaffold/defaults/env.example`

- [ ] **Step 1: Confirm nothing still references the deleted embeds**

Run: `grep -rn "defaults/shell3.lua\|defaults/env.example\|WriteStarterConfig\|defaultEnvExample\|defaultConfig" --include=*.go .`
Expected: no matches (all removed in Tasks 1-2). If any remain, fix them before deleting.

- [ ] **Step 2: Delete the old reference files**

```bash
git rm internal/scaffold/defaults/shell3.lua internal/scaffold/defaults/env.example
```

- [ ] **Step 3: Full build + test**

Run: `go build ./... && go test ./...`
Expected: PASS across all packages.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore(scaffold): drop legacy reference shell3.lua/env.example (moved to cookbook + base)"
```

---

### Task 6: Manual from-zero onboarding test (human-run)

This is run by the user (or with the user), not automated. It exercises the cold path.

- [ ] **Step 1: Back up the live home config (preserves keys)**

```bash
mv ~/.shell3 ~/.shell3.bak
```

- [ ] **Step 2: No config → expect the boot redirect**

```bash
make install   # or: go install ./cmd/shell3
shell3 "hi"
```
Expected: `no shell3.lua found — run 'shell3 boot' to create one (or pass --config)` — and NO `~/.shell3/shell3.lua` was conjured.

- [ ] **Step 3: Onboard from zero**

```bash
shell3 boot
```
Answer with the real values from the backup (`~/.shell3.bak/shell3.lua` for url/model, `~/.shell3.bak/.env` for the key — do not display the .env; just copy the value into the prompt). Optionally give the proxy command if one was used.

- [ ] **Step 4: Verify it works**

```bash
shell3 "say hello in one word"
```
Expected: a normal response — config loads, model answers.

- [ ] **Step 5: Restore (when done testing)**

```bash
rm -rf ~/.shell3 && mv ~/.shell3.bak ~/.shell3
```

---

## Self-Review

**Spec coverage:**
- Remove auto-write + redirect → Task 1. ✓
- `shell3 boot` prompts/flags/.env-merge/no-clobber → Task 3. ✓
- Split-file base config (model + code/plan agents + lib tree) → Task 2 (template) + Task 3 (rendered by boot). ✓
- web_fetch/brave_search examples → Task 2 (`lib/tools.lua`). ✓
- spawning-subagents on code → Task 2 + template skills. ✓
- brainstorming skill (ported, ends at design doc) on plan → Task 2 (`lib/skills/brainstorming.lua`) + template. ✓
- Cookbook (MCP + dropped skills + guards + proxy) → Task 4. ✓
- `.env` key `<NAME>_API_KEY`, Brave placeholder so load never fails → Task 3 (`envKeyForName`, `mergeEnv`) + brave_search empty-key handling. ✓
- Global gitignore covers `.env` → Task 1 Step 4. ✓
- From-zero test (move-aside) → Task 6. ✓

**Placeholder scan:** Verbatim ports cite exact source line ranges in a file that exists until Task 5; all new code/content is shown in full. No TBD/TODO.

**Type consistency:** `scaffold.Values{Name,BaseURL,EnvKey,Model,Proxy}` defined in Task 2 Step 8, used identically in Task 3 Step 3. `RenderBaseConfig(dir, Values)` signature consistent. `mergeEnv(string, [][2]string) string` and `envKeyForName(string) string` defined and called consistently in Task 3.

**Ambiguity:** `value`/`secret` prompt helpers handle flag-vs-prompt-vs-non-TTY explicitly; required-field errors are explicit.
