# Telegram-First Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the Telegram host its own config location (`~/.shell3/telegram/shell3.lua`), a dedicated `shell3 boot --telegram` bootstrap that scaffolds it (token + chat id + dashboard), and a Telegram-tuned agent prompt.

**Architecture:** A telegram-only path resolver (`ResolveTelegramConfigPath`) that `cmd/shell3/telegram.go` calls before building the Runtime; a new scaffold template (`defaults/telegram/shell3.lua.tmpl`) rendered by `RenderTelegramConfig` from a `TelegramValues` struct; a `--telegram` branch in the existing `runBoot`. No engine changes; non-telegram resolution is untouched.

**Tech Stack:** Go, cobra (`cmd/shell3`), `text/template` + `embed` (`internal/scaffold`), `internal/agentsetup`, `internal/luacfg` (for test loads). No new dependencies.

**Source of truth:** `docs/dev/superpowers/specs/2026-06-11-telegram-setup-design.md`. Signatures below are copied verbatim from current code (verified 2026-06-11).

**Build approach:** Task 1 (resolver) and Task 3 (scaffold template) are independent — do them first (parallelizable). Task 2 (wire telegram.go) depends on Task 1. Task 4 (`boot --telegram`) depends on Task 3. Task 5 is the verification sweep. After each task: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`.

**Key verified internals (do not guess these):**
- `agentsetup.ResolveConfigPath(flag, cwd, homeDir string) (string, error)` (agentsetup.go:532) — flag wins; else `cwd/shell3.lua` if `fileExists`; else `homeDir/.shell3/shell3.lua` if `fileExists`; else error. `fileExists(p string) bool` is in the same file (agentsetup.go:547).
- `cmd/shell3/telegram.go:22-31`: `var configPath string` (the `--config` flag, defined near line 165); RunE does `cwd, _ := os.Getwd()` then `shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: configPath, WorkDir: cwd})`.
- `internal/scaffold/scaffold.go`: `//go:embed all:defaults/base` → `baseFS`, `const baseRoot = "defaults/base"`. `Values{Name, BaseURL, EnvKey, Model, Proxy string}`. `RenderBaseConfig(dir string, v Values, force bool) error` renders `baseRoot+"/shell3.lua.tmpl"` (template func `luaesc`→`luaEscape`) to `dir/shell3.lua`, then `fs.WalkDir(baseFS, baseRoot+"/lib", ...)` copying each file via `writeFile(path, content, 0644, force)`. Helpers `luaEscape`, `writeFile` already exist.
- `cmd/shell3/boot.go`: `bootFlags{url, model, name, key, proxy, braveKey string; force bool}`. `runBoot(f *bootFlags) error` computes `dir = ~/.shell3`, `cfgPath = dir/shell3.lua`, no-clobber guard, prompts via `value(flag, label, def string, in *bufio.Reader, tty, required bool) (string, error)`, `envKey := envKeyForName(name)`, `scaffold.RenderBaseConfig(...)`, then `mergeEnv(existing, [][2]string{...})` → write `dir/.env` at 0600, then `printBootSuccess(...)`. Helpers `value`, `envKeyForName`, `mergeEnv`, `printBootSuccess` exist.
- `internal/luacfg`: `luacfg.Load(path, workdir string) (*LoadedConfig, error)` loads `.env` from `workdir`. `(*LoadedConfig).Agents() []Agent` (Agent has `.Name`), `.Subagents() []Subagent`, `.Telegram() luacfg.TelegramConfig` (`{Token, ChatID, Agent, WorkDir string; Dashboard DashboardConfig}`; `DashboardConfig{Enabled bool; Addr, URL string}`), `.Close()`.
- Test pattern (boot_test.go:110-194): `t.Setenv("HOME", home)`; build `bootFlags`; `runBoot(f)`; stat files; read `.env`; `agentsetup.ResolveConfigPath`; `luacfg.Load(resolved, dir)` then `c.Agents()`.

---

## Task 1: `ResolveTelegramConfigPath` (telegram-only resolver)

**Files:**
- Modify: `internal/agentsetup/agentsetup.go` (add the function next to `ResolveConfigPath`, ~line 545)
- Modify: `internal/agentsetup/agentsetup_test.go` (add the test; create the file if it does not exist — check first)

The Telegram bot prefers its dedicated config, then the legacy global, then project-local. This ordering is **locked** by the spec — do not reorder.

- [ ] **Step 1: Write the failing test.** Append to `internal/agentsetup/agentsetup_test.go` (if the file does not exist, create it with `package agentsetup` and imports `os`, `path/filepath`, `testing`):

```go
func TestResolveTelegramConfigPath(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	tgDir := filepath.Join(home, ".shell3", "telegram")
	if err := os.MkdirAll(tgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tgCfg := filepath.Join(tgDir, "shell3.lua")
	globalCfg := filepath.Join(home, ".shell3", "shell3.lua")
	localCfg := filepath.Join(cwd, "shell3.lua")
	write := func(p string) {
		if err := os.WriteFile(p, []byte("-- x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Explicit flag always wins.
	if got, err := ResolveTelegramConfigPath("/explicit/x.lua", cwd, home); err != nil || got != "/explicit/x.lua" {
		t.Fatalf("flag: got %q err %v", got, err)
	}
	// Nothing exists yet -> error.
	if _, err := ResolveTelegramConfigPath("", cwd, home); err == nil {
		t.Fatal("expected error when no config exists")
	}
	// Only project-local exists -> it.
	write(localCfg)
	if got, _ := ResolveTelegramConfigPath("", cwd, home); got != localCfg {
		t.Fatalf("local: got %q want %q", got, localCfg)
	}
	// Global beats project-local.
	write(globalCfg)
	if got, _ := ResolveTelegramConfigPath("", cwd, home); got != globalCfg {
		t.Fatalf("global: got %q want %q", got, globalCfg)
	}
	// Telegram dir beats everything (except an explicit flag).
	write(tgCfg)
	if got, _ := ResolveTelegramConfigPath("", cwd, home); got != tgCfg {
		t.Fatalf("telegram: got %q want %q", got, tgCfg)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run TestResolveTelegramConfigPath -v`
Expected: FAIL — `ResolveTelegramConfigPath` undefined.

- [ ] **Step 3: Implement.** In `internal/agentsetup/agentsetup.go`, immediately after `ResolveConfigPath` (after line 545):

```go
// ResolveTelegramConfigPath returns the shell3.lua the Telegram host should load.
// Order (telegram-only; do not reorder): the explicit flag, else the dedicated
// telegram config ~/.shell3/telegram/shell3.lua, else the legacy global
// ~/.shell3/shell3.lua (so an existing setup keeps working), else a project-local
// ./shell3.lua. This deliberately differs from ResolveConfigPath, which the TUI
// and other front-ends keep using (project-local first).
func ResolveTelegramConfigPath(flag, cwd, homeDir string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	telegram := filepath.Join(homeDir, ".shell3", "telegram", "shell3.lua")
	if fileExists(telegram) {
		return telegram, nil
	}
	global := filepath.Join(homeDir, ".shell3", "shell3.lua")
	if fileExists(global) {
		return global, nil
	}
	local := filepath.Join(cwd, "shell3.lua")
	if fileExists(local) {
		return local, nil
	}
	return "", fmt.Errorf("no shell3.lua found — run 'shell3 boot --telegram' to create one (or pass --config)")
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/agentsetup/ -run TestResolveTelegramConfigPath -v`
Expected: PASS. Then `go build ./... && go vet ./internal/agentsetup/ && gofmt -l internal/agentsetup/`.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup/agentsetup.go internal/agentsetup/agentsetup_test.go
git commit -m "feat(agentsetup): ResolveTelegramConfigPath — telegram-dir-first resolution"
```

---

## Task 2: Wire `shell3 telegram` to the resolver

**Files:**
- Modify: `cmd/shell3/telegram.go` (resolve the path explicitly before `NewRuntime`)

`shell3 telegram` must resolve via the new order and pass the concrete path to the Runtime, so `Runtime.Reload`/`Runtime.ConfigPath` read the same file.

- [ ] **Step 1: Implement.** In `cmd/shell3/telegram.go` RunE, replace:

```go
				cwd, _ := os.Getwd()
				rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: configPath, WorkDir: cwd})
				if err != nil {
					return err
				}
```

with:

```go
				cwd, _ := os.Getwd()
				home, _ := os.UserHomeDir()
				resolved, err := agentsetup.ResolveTelegramConfigPath(configPath, cwd, home)
				if err != nil {
					return err
				}
				rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: resolved, WorkDir: cwd})
				if err != nil {
					return err
				}
```

Add the import `"github.com/weatherjean/shell3/internal/agentsetup"` to `cmd/shell3/telegram.go` if not already present (check the import block).

- [ ] **Step 2: Build + vet**

Run: `go build ./... && go vet ./cmd/shell3/ && gofmt -l cmd/shell3/`
Expected: clean.

> Why no unit test here: `telegram.go`'s RunE starts the live bot (network), so it is not unit-tested. The resolver it calls is covered by Task 1; this step is a thin wiring change verified by build + the Task 5 manual check.

- [ ] **Step 3: Commit**

```bash
git add cmd/shell3/telegram.go
git commit -m "feat(telegram): resolve config via ResolveTelegramConfigPath (telegram dir first)"
```

---

## Task 3: Telegram scaffold template + `RenderTelegramConfig`

**Files:**
- Create: `internal/scaffold/defaults/telegram/shell3.lua.tmpl`
- Modify: `internal/scaffold/scaffold.go` (add `TelegramValues`, `RenderTelegramConfig`, the telegram embed)
- Modify: `internal/scaffold/scaffold_test.go` (add the render+load test)

- [ ] **Step 1: Create the telegram template** `internal/scaffold/defaults/telegram/shell3.lua.tmpl` with EXACTLY this content:

```
-- shell3.lua — Telegram host config written by `shell3 boot --telegram`.
-- Secrets (TELEGRAM_BOT_TOKEN and the model api key) live in .env beside this file.
-- Edit freely, then apply changes live with the `reload` tool (see the self-evolve skill).
-- Run it with:  shell3 telegram

local tools  = require("lib.tools")   -- { web_fetch, brave_search }
local guards = require("lib.guards")  -- { no_env_edit, confirm_destructive }

local self_evolve = shell3.skill({
  name = "self-evolve",
  description = "How to safely change your own shell3.lua config and apply it live with the reload + status tools. A failed reload keeps the old config, so self-editing is always safe.",
  body = [[
You can modify your own configuration and apply it live, without anyone
restarting the bot. A failed reload keeps the current config running, so you can
never brick yourself with a bad edit.

## Orient first
Call the `status` tool. It prints the absolute path of the `shell3.lua` you edit
(plus your active agent, the model, the working directory, and any cron jobs).
Edit that exact file. If it `require`s `lib/` modules, follow the require.

## The loop
1. Edit the file. Copy the shape of an existing block rather than writing from scratch.
2. Respect the cross-reference rules (validated on every reload; a violation rejects it):
   - every agent/subagent `model = "..."` must name a declared `shell3.model`;
   - every `shell3.cron` job's `agent = "..."` must name a declared SUBAGENT;
   - a skill or tool granted to an agent must be a declared handle.
3. Call the `reload` tool. It validates the whole file, then applies it after this
   turn ends. The result — success counts or the exact error — is delivered to chat.
4. If it failed: a Lua `[string "shell3.lua"]:42: ...` error is a typo at that line;
   an `unknown subagent "x"` / `unknown model "y"` error is a bad cross-reference.
   The old config keeps running until a reload succeeds, so just edit and retry.

## What survives a reload
- Conversation history is always kept. Active agent and /set params are restored
  when they still exist. MCP servers and model proxies restart (a brief pause).
]],
})

local scheduling_jobs = shell3.skill({
  name = "scheduling-jobs",
  description = "How to add, arm, and test recurring scheduled jobs (shell3.cron). A cron job dispatches a SUBAGENT on a schedule; arm it with reload and fire it on demand with /run.",
  body = [[
You can give yourself recurring background work with `shell3.cron`. A job fires on
a schedule and dispatches a SUBAGENT with a prompt; the result is posted to chat.

## The block
shell3.cron({
  jobs = {
    { name="nightly", schedule="0 9 * * *", agent="explorer",
      prompt="Summarize anything noteworthy.", notify=true },
  },
})

## Fields
- name: identifier for /run <name> and the dashboard.
- schedule: 5-field cron "min hour dom mon dow", or @hourly/@daily/@weekly,
  or @every 30s / @every 5m / @every 1h.
- agent: MUST be a declared shell3.subagent (e.g. explorer), NOT a top-level agent.
- prompt: the instruction handed to the subagent.
- workdir: optional working directory.
- notify: true posts the result to chat; false runs quietly (errors still post).

## Arming and testing
1. Edit shell3.lua to add/adjust the job.
2. Call the `reload` tool to validate and arm it (a bad schedule or unknown
   subagent is rejected; the old config keeps running).
3. Fire on demand with /run <name>; or wait for the schedule.
Removing a job and reloading disarms it.
]],
})

-- ---------------------------------------------------------------------------
-- Model
-- ---------------------------------------------------------------------------
shell3.model("{{.Name | luaesc}}", {
  base_url       = "{{.BaseURL | luaesc}}",
  api_key        = shell3.env.secret("{{.EnvKey}}"),  -- {{.EnvKey}} in .env
  model          = "{{.Model | luaesc}}",
  context_window = 128000,
{{if .Proxy}}  run_proxy      = "{{.Proxy | luaesc}}",
{{else}}  -- run_proxy   = "npx @some/codex-proxy --port 8787",
{{end}}})

-- ---------------------------------------------------------------------------
-- Subagent (cron jobs and spawn_agent dispatch this read-only investigator)
-- ---------------------------------------------------------------------------
local explorer = shell3.subagent({
  name        = "explorer",
  description = "Read-only investigation — locate where/how something is implemented, summarize a subsystem, or gather context across many files. No edits.",
  model       = "{{.Name | luaesc}}",
  prompt = [[
You are a focused explorer. Investigate using bash (rg, fd, cat, git log) and
report a concise, concrete answer with file:line references. You cannot edit
files. Decide and proceed; no human is available.
]],
  tools = { bash = true, history = true },
  on_tool_call = { guards.no_env_edit },
})

{{if .Chrome}}-- ---------------------------------------------------------------------------
-- Chrome DevTools MCP (browser automation; needs Node/npx). Started lazily on
-- first use. Add a `tools = {...}` allowlist to restrict which tools are exposed.
-- ---------------------------------------------------------------------------
local chrome = shell3.mcp({
  name    = "chrome",
  command = "npx",
  args    = { "-y", "chrome-devtools-mcp@latest", "--autoConnect", "--no-usage-statistics" },
})
{{end}}
-- ---------------------------------------------------------------------------
-- Agent (the single agent the Telegram bot runs; it spawns subagents)
-- ---------------------------------------------------------------------------
shell3.agent({
  name  = "code",
  model = "{{.Name | luaesc}}",
  prompt = [[
You are a capable assistant running as an always-on Telegram bot. You inspect,
edit, run, and schedule work on the host, and you talk to one person over chat.

## Communication (you talk over Telegram)
- Short, natural, human. No memo voice, no long preambles, no walls of text, no
  restating the question. One phone screen, not three.
- Lead with the answer or result; add detail only if asked.
- Plain text by default; light markdown (`code`, short bullets) when it helps.
  Avoid wide tables — they wrap badly on mobile.
- Don't paste huge output — write it to a file and send it with send_media_telegram.
- Emoji sparse, for warmth or a quick OK / warning.

## Autonomy (you run unattended)
- Actionable request -> act this turn; don't reply with a plan you could execute.
- Continue until done or genuinely blocked; vary your approach on weak results
  before giving up.
- Reversible, local actions: just do them. Irreversible, destructive, external,
  or privacy-sensitive actions: ask first, with one concise question.
- Ask at most one question, only when a single missing decision blocks safe
  progress; otherwise pick a sensible default and say what you assumed.
- Long jobs: send a one-line "on it", then keep going. Hand parallel or
  background work to a subagent with spawn_agent and DON'T poll — its result
  comes back to you automatically when it finishes.
- Final claims need evidence (you ran the test/build, read the file, saw the
  output) or a named blocker.

## Reshaping yourself
- `status` shows your config path and what's live.
- The self-evolve skill: edit shell3.lua, then call `reload` to apply it.
- The scheduling-jobs skill: add a shell3.cron job, then `reload` to arm it.

## Context hygiene
- Prune large successful tool outputs after extracting what you need; never prune
  errors or small results.
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
    subagents         = { explorer },
    custom            = { tools.web_fetch, tools.brave_search },
{{if .Chrome}}    mcp               = { chrome },
{{end}}  },
  skills = { self_evolve, scheduling_jobs },
  on_tool_call = { guards.no_env_edit, guards.confirm_destructive },
})

-- ---------------------------------------------------------------------------
-- Telegram host (run: shell3 telegram)
-- ---------------------------------------------------------------------------
shell3.telegram({
  token   = shell3.env.secret("TELEGRAM_BOT_TOKEN"),
  chat_id = "{{.ChatID | luaesc}}",
  agent   = "code",
  workdir = "{{.WorkDir | luaesc}}",
{{if .DashboardEnabled}}  dashboard = { enabled = true, addr = "{{.DashboardAddr | luaesc}}", url = "{{.DashboardURL | luaesc}}" },
{{else}}  dashboard = { enabled = false },
{{end}}})

-- Sample scheduled job (disarmed). Uncomment and `reload` to arm; /run <name> to test.
-- shell3.cron({
--   jobs = {
--     { name="daily", schedule="@daily", agent="explorer", notify=true,
--       prompt="Summarize anything noteworthy from the last day." },
--   },
-- })
```

- [ ] **Step 2: Write the failing test.** Append to `internal/scaffold/scaffold_test.go` (ensure imports include `path/filepath`, `os`, `testing`, and `github.com/weatherjean/shell3/internal/luacfg` — the file already loads configs via luacfg; reuse its imports):

```go
func TestRenderTelegramConfigLoads(t *testing.T) {
	dir := t.TempDir()
	if err := RenderTelegramConfig(dir, TelegramValues{
		Values:           Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m-1"},
		ChatID:           "123456",
		WorkDir:          dir,
		DashboardEnabled: true,
		DashboardAddr:    "127.0.0.1:8765",
		DashboardURL:     "https://h.ts.net/",
	}, false); err != nil {
		t.Fatal(err)
	}
	// lib modules copied + config written.
	for _, p := range []string{"shell3.lua", "lib/tools.lua", "lib/guards.lua"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	// Provide the token the config references, then load through luacfg.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TELEGRAM_BOT_TOKEN=tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("telegram config failed to load: %v", err)
	}
	defer c.Close()
	if a := c.Agents(); len(a) != 1 || a[0].Name != "code" {
		t.Errorf("agents = %v, want [code]", a)
	}
	if s := c.Subagents(); len(s) != 1 || s[0].Name != "explorer" {
		t.Errorf("subagents = %v, want [explorer]", s)
	}
	tg := c.Telegram()
	if tg.ChatID != "123456" || tg.Token != "tok" || tg.Agent != "code" {
		t.Errorf("telegram = %+v, want chat_id=123456 token=tok agent=code", tg)
	}
	if !tg.Dashboard.Enabled || tg.Dashboard.Addr != "127.0.0.1:8765" {
		t.Errorf("dashboard = %+v, want enabled 127.0.0.1:8765", tg.Dashboard)
	}
	if len(c.MCPServers) != 0 {
		t.Errorf("default render should declare no MCP servers, got %v", c.MCPServers)
	}
}

func TestRenderTelegramConfigChrome(t *testing.T) {
	dir := t.TempDir()
	if err := RenderTelegramConfig(dir, TelegramValues{
		Values:  Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m-1"},
		ChatID:  "1", WorkDir: dir, Chrome: true,
	}, false); err != nil {
		t.Fatal(err)
	}
	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("chrome config failed to load: %v", err)
	}
	defer c.Close()
	// luacfg records the MCP server spec; it does NOT spawn npx at load time.
	if _, ok := c.MCPServers["chrome"]; !ok {
		t.Errorf("Chrome:true should declare the chrome MCP server, got %v", c.MCPServers)
	}
}
```

> `c.MCPServers` is the exported `map[string]MCPServer` field on `luacfg.LoadedConfig`.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/scaffold/ -run TestRenderTelegramConfigLoads -v`
Expected: FAIL — `RenderTelegramConfig` / `TelegramValues` undefined.

- [ ] **Step 4: Implement** in `internal/scaffold/scaffold.go`. Add the telegram embed near the base one (after line 20):

```go
//go:embed all:defaults/telegram
var telegramFS embed.FS

const telegramRoot = "defaults/telegram"
```

Add the values type (after the `Values` struct, ~line 29):

```go
// TelegramValues are the substitutions for the templated telegram shell3.lua.
// It embeds Values (the model block) and adds the telegram host fields.
type TelegramValues struct {
	Values
	ChatID           string // numeric Telegram chat id (goes in the lua)
	WorkDir          string // agent working directory
	DashboardEnabled bool
	DashboardAddr    string // e.g. "127.0.0.1:8765"
	DashboardURL     string // public Mini App URL ("" if none)
	Chrome           bool   // declare the chrome DevTools MCP + grant it to the agent
}
```

Add the render function (after `RenderBaseConfig`, ~line 69):

```go
// RenderTelegramConfig writes the telegram config tree into dir: shell3.lua
// rendered from the embedded telegram template with v, plus the verbatim lib/
// modules reused from the base scaffold (tools, guards, and the rest). When force
// is false, existing files are left untouched (safe to re-run).
func RenderTelegramConfig(dir string, v TelegramValues, force bool) error {
	tmplBytes, err := telegramFS.ReadFile(telegramRoot + "/shell3.lua.tmpl")
	if err != nil {
		return fmt.Errorf("scaffold: read telegram template: %w", err)
	}
	t, err := template.New("shell3.lua").Funcs(template.FuncMap{"luaesc": luaEscape}).Parse(string(tmplBytes))
	if err != nil {
		return fmt.Errorf("scaffold: parse telegram template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, v); err != nil {
		return fmt.Errorf("scaffold: execute telegram template: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "shell3.lua"), buf.Bytes(), 0644, force); err != nil {
		return err
	}
	// Reuse the base lib/ modules (tools, guards, …) verbatim.
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
		return writeFile(filepath.Join(dir, rel), content, 0644, force)
	})
}
```

> The lib-copy loop is duplicated from `RenderBaseConfig` deliberately (the two render entry points stay independent and readable). If the reviewer prefers, extract a private `copyBaseLib(dir string, force bool) error` and call it from both — optional cleanup, not required.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/scaffold/ -run TestRenderTelegramConfigLoads -v`
Expected: PASS. Then `go build ./... && go vet ./internal/scaffold/ && gofmt -l internal/scaffold/` and `go test ./internal/scaffold/` (the whole package, to catch any embed/format regression).

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/
git commit -m "feat(scaffold): telegram template + RenderTelegramConfig (tuned prompt, telegram block)"
```

---

## Task 4: `boot --telegram`

**Files:**
- Modify: `cmd/shell3/boot.go` (add flags + a telegram branch in `runBoot`)
- Modify: `cmd/shell3/boot_test.go` (add the end-to-end test)

- [ ] **Step 1: Add the flags.** In `cmd/shell3/boot.go`, extend `bootFlags`:

```go
type bootFlags struct {
	url, model, name, key, proxy, braveKey string
	force                                  bool
	telegram                               bool
	tgToken, chatID, dashAddr, dashURL     string
	noDashboard                            bool
	chrome                                 bool
}
```

Register them in `newBootCommand` (after the existing flags, before `return cmd`):

```go
	cmd.Flags().BoolVar(&f.telegram, "telegram", false, "Scaffold a Telegram host config in ~/.shell3/telegram/")
	cmd.Flags().StringVar(&f.tgToken, "tg-token", "", "Telegram bot token (from @BotFather)")
	cmd.Flags().StringVar(&f.chatID, "chat-id", "", "Your numeric Telegram chat id")
	cmd.Flags().StringVar(&f.dashAddr, "dash-addr", "127.0.0.1:8765", "Dashboard listen address")
	cmd.Flags().StringVar(&f.dashURL, "dash-url", "", "Public Mini App URL for the dashboard (optional)")
	cmd.Flags().BoolVar(&f.noDashboard, "no-dashboard", false, "Disable the dashboard in the telegram config")
	cmd.Flags().BoolVar(&f.chrome, "chrome", false, "Enable the Chrome DevTools MCP (browser automation; needs Node/npx)")
```

- [ ] **Step 2: Branch `runBoot` for telegram.** In `cmd/shell3/boot.go`, replace the directory/path setup at the top of `runBoot` (lines 46-47):

```go
	dir := filepath.Join(home, ".shell3")
	cfgPath := filepath.Join(dir, "shell3.lua")
```

with:

```go
	dir := filepath.Join(home, ".shell3")
	if f.telegram {
		dir = filepath.Join(home, ".shell3", "telegram")
	}
	cfgPath := filepath.Join(dir, "shell3.lua")
```

Then, replace the render + success tail of `runBoot` (the block from `envKey := envKeyForName(name)` through `printBootSuccess(...)`, lines 88-110) with a branch. New code:

```go
	envKey := envKeyForName(name)

	envPairs := [][2]string{{envKey, key}, {"BRAVE_API_KEY", braveKey}}
	var chrome bool // visible to printTelegramBootSuccess below

	if f.telegram {
		token, err := value(f.tgToken, "Telegram bot token (from @BotFather)", "", in, tty, true)
		if err != nil {
			return err
		}
		chatID, err := value(f.chatID, "Your numeric Telegram chat id (message @userinfobot)", "", in, tty, true)
		if err != nil {
			return err
		}
		chrome = f.chrome
		if !chrome && tty {
			ans, err := value("", "Enable Chrome browser MCP (browser automation; needs Node/npx)? [y/N]", "n", in, tty, false)
			if err != nil {
				return err
			}
			chrome = strings.EqualFold(strings.TrimSpace(ans), "y") || strings.EqualFold(strings.TrimSpace(ans), "yes")
		}
		workDir := filepath.Join(dir, "workdir")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return fmt.Errorf("boot: mkdir workdir: %w", err)
		}
		if err := scaffold.RenderTelegramConfig(dir, scaffold.TelegramValues{
			Values:           scaffold.Values{Name: name, BaseURL: url, EnvKey: envKey, Model: model, Proxy: proxy},
			ChatID:           chatID,
			WorkDir:          workDir,
			DashboardEnabled: !f.noDashboard,
			DashboardAddr:    f.dashAddr,
			DashboardURL:     f.dashURL,
			Chrome:           chrome,
		}, f.force); err != nil {
			return err
		}
		envPairs = append(envPairs, [2]string{"TELEGRAM_BOT_TOKEN", token})
	} else {
		if err := scaffold.RenderBaseConfig(dir, scaffold.Values{
			Name: name, BaseURL: url, EnvKey: envKey, Model: model, Proxy: proxy,
		}, f.force); err != nil {
			return err
		}
	}

	envPath := filepath.Join(dir, ".env")
	existing, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("boot: read .env: %w", err)
	}
	merged := mergeEnv(string(existing), envPairs)
	if err := os.WriteFile(envPath, []byte(merged), 0600); err != nil {
		return fmt.Errorf("boot: write .env: %w", err)
	}

	if f.telegram {
		printTelegramBootSuccess(dir, cfgPath, envPath, chrome)
	} else {
		printBootSuccess(dir, cfgPath, envPath, proxy != "")
	}
	return nil
```

Add the telegram success printer at the bottom of `boot.go` (after `printBootSuccess`):

```go
func printTelegramBootSuccess(dir, cfgPath, envPath string, chrome bool) {
	fmt.Println()
	fmt.Println("shell3 Telegram host is configured.")
	fmt.Printf("  config:  %s\n", cfgPath)
	fmt.Printf("  modules: %s\n", filepath.Join(dir, "lib"))
	fmt.Printf("  secrets: %s  (TELEGRAM_BOT_TOKEN + model key — never commit this)\n", envPath)
	if chrome {
		fmt.Println("  chrome:  enabled — needs Node/npx; the MCP server starts on first use.")
	}
	fmt.Println()
	fmt.Println("Run:  shell3 telegram")
}
```

> `strings` is already imported by `boot.go`; the `[y/N]` parse uses
> `strings.EqualFold`/`strings.TrimSpace`. No new imports.

> Note: `mergeEnv` only appends a key it does not already have, and never writes an empty `TELEGRAM_BOT_TOKEN` placeholder comment (that special-case is `BRAVE_API_KEY` only), so a real token is written as `TELEGRAM_BOT_TOKEN=<token>`. `value(..., required=true)` guarantees the token/chat id are non-empty (prompted on TTY, or required via flag when non-TTY).

- [ ] **Step 3: Write the failing test.** Append to `cmd/shell3/boot_test.go` (it already imports `os`, `path/filepath`, `strings`, `testing`, `agentsetup`, `luacfg`):

```go
func TestBootTelegramEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	f := &bootFlags{
		url: "http://localhost:9999/v1", model: "test-model", name: "main",
		telegram: true, tgToken: "BOT:TOKEN", chatID: "424242",
		dashAddr: "127.0.0.1:8765", dashURL: "https://h.ts.net/", chrome: true,
	}
	if err := runBoot(f); err != nil {
		t.Fatalf("runBoot --telegram: %v", err)
	}

	dir := filepath.Join(home, ".shell3", "telegram")
	for _, p := range []string{"shell3.lua", "lib/tools.lua", "lib/guards.lua", ".env", "workdir"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	// .env carries the bot token + model key, 0600.
	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "TELEGRAM_BOT_TOKEN=BOT:TOKEN") {
		t.Errorf(".env missing bot token:\n%s", env)
	}
	if !strings.Contains(string(env), "MAIN_API_KEY=") {
		t.Errorf(".env missing model key:\n%s", env)
	}

	// The generated telegram config loads and reflects the prompted values.
	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("generated telegram config failed to load: %v", err)
	}
	defer c.Close()
	tg := c.Telegram()
	if tg.ChatID != "424242" || tg.Token != "BOT:TOKEN" || tg.Agent != "code" {
		t.Errorf("telegram = %+v, want chat_id=424242 token=BOT:TOKEN agent=code", tg)
	}
	if !tg.Dashboard.Enabled || tg.Dashboard.URL != "https://h.ts.net/" {
		t.Errorf("dashboard = %+v", tg.Dashboard)
	}
	if _, ok := c.MCPServers["chrome"]; !ok {
		t.Errorf("--chrome should declare the chrome MCP server, got %v", c.MCPServers)
	}

	// telegram-dir-first resolution finds it.
	cwd, _ := os.Getwd()
	resolved, err := agentsetup.ResolveTelegramConfigPath("", cwd, home)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(dir, "shell3.lua") {
		t.Errorf("resolved = %q, want telegram shell3.lua", resolved)
	}

	// A generic boot (no --telegram) still targets ~/.shell3, untouched.
	if err := runBoot(&bootFlags{url: "http://x/v1", model: "m", name: "main"}); err != nil {
		t.Fatalf("generic boot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".shell3", "shell3.lua")); err != nil {
		t.Errorf("generic boot did not write ~/.shell3/shell3.lua: %v", err)
	}
}
```

- [ ] **Step 4: Run to verify failure, then pass**

Run: `go test ./cmd/shell3/ -run TestBootTelegramEndToEnd -v`
Expected: FAIL first (before implementing Steps 1-2 — if you wrote the test before the impl), then PASS after. Also run the existing `go test ./cmd/shell3/ -run TestBootEndToEnd -v` to confirm the generic path still works.

- [ ] **Step 5: Build + vet + gofmt**

Run: `go build ./... && go vet ./cmd/shell3/ && gofmt -l cmd/shell3/`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/shell3/boot.go cmd/shell3/boot_test.go
git commit -m "feat(boot): boot --telegram — scaffold ~/.shell3/telegram with token, chat id, dashboard"
```

---

## Task 5: Verification sweep + docs

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `docs/dev/superpowers/specs/2026-06-11-telegram-setup-design.md` (status → implemented)

- [ ] **Step 1: Full sweep** (orchestrator):

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`
Expected: all clean/green.

- [ ] **Step 2: Manual smoke (no live bot).** Confirm the telegram bootstrap renders + loads against a throwaway HOME without touching the user's real `~/.shell3`:

```bash
HOME=$(mktemp -d) go run ./cmd/shell3 boot --telegram \
  --url http://localhost:9999/v1 --model test --name main \
  --tg-token TESTTOKEN --chat-id 123 --dash-url https://example.ts.net/
```
Expected: prints "shell3 Telegram host is configured." and the new paths under the temp HOME. (Do NOT run `shell3 telegram` against it — there is no real bot token.)

- [ ] **Step 3: CHANGELOG + spec status.** Add a `CHANGELOG.md` entry under `## [Unreleased] → ### Added`: telegram-first setup — `shell3 boot --telegram` scaffolds `~/.shell3/telegram/` (config + `.env` + tuned prompt, optional Chrome MCP via `--chrome`/prompt), and `shell3 telegram` resolves telegram-dir-first (`--config → ~/.shell3/telegram → ~/.shell3 → ./`). Update the spec header to `Status: implemented (2026-06-11)`.

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md docs/dev/superpowers/specs/2026-06-11-telegram-setup-design.md
git commit -m "docs(telegram-setup): changelog + spec status implemented"
```

---

## Self-Review

**Spec coverage:**
- Telegram-only resolver, order flag→telegram→global→local → Task 1 (`ResolveTelegramConfigPath`) + test. ✓
- `shell3 telegram` uses it → Task 2. ✓
- `boot --telegram` writes `~/.shell3/telegram/` + own `.env` (token only in `.env`, chat_id in lua) → Task 4. ✓
- Telegram template with tuned Communication + Autonomy prompt, explorer subagent, telegram block, self-evolve + scheduling-jobs skills, commented cron → Task 3 (template) + 4 (wiring). ✓
- `.env` beside the lua (telegram dir) → automatic via `luacfg.Load(path, dir)`; asserted in Tasks 3 & 4 tests. ✓
- No migration (fresh scaffold); fallback keeps old config working → Task 1 ordering test (global fallback) + Task 4 (generic boot untouched). ✓
- Chrome MCP opt-in (default off): `--chrome` flag + `[y/N]` prompt → Task 4; `{{if .Chrome}}` MCP block + agent `mcp = { chrome }` → Task 3 template; `TelegramValues.Chrome` → Task 3. Tested: `TestRenderTelegramConfigChrome` (declared) + default render asserts no MCP + boot e2e asserts `c.MCPServers["chrome"]`. luacfg records the spec without spawning `npx`, so tests don't need Node. ✓
- Tests: resolver ordering, boot --telegram e2e, telegram template loads → Tasks 1/3/4. ✓
- Non-goals (TUI resolution unchanged, no multi-bot) → nothing in the plan touches `ResolveConfigPath` or adds multi-bot. ✓

**Placeholder scan:** The telegram template body is given in full (Task 3 Step 1). The lib-copy duplication and the optional `copyBaseLib` extraction are flagged as optional cleanup, not placeholders. No TBD/TODO.

**Type consistency:** `TelegramValues` embeds `Values` and is defined in Task 3, constructed identically in Task 3's test and Task 4's `runBoot`/test. `RenderTelegramConfig(dir, TelegramValues, force)` defined in Task 3, called in Task 4. `ResolveTelegramConfigPath(flag, cwd, homeDir)` defined in Task 1, called in Task 2 and asserted in Tasks 1 & 4. `bootFlags` new fields (Task 4 Step 1) are read in Task 4 Step 2. `luacfg` accessors (`Agents()`, `Subagents()`, `Telegram()` with `.ChatID/.Token/.Agent/.Dashboard`) match the verified internals.

**Template gotcha for the implementer:** the telegram template uses Go `text/template` `{{if}}` blocks on their own lines (mirroring the base template's `run_proxy` pattern at base shell3.lua.tmpl:75-77). Keep the `{{if .Proxy}}…{{else}}…{{end}}` and `{{if .DashboardEnabled}}…{{else}}…{{end}}` exactly as written so the rendered Lua stays valid; the Task 3 load-test catches a broken render.
