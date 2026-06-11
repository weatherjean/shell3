# bash-first: file-backed skills + bash-template tools

Status: IMPLEMENTED on feat/bash-first.

Design for the next slice of the bash-first refactor (`feat/bash-first`). Goal:
**stop wrapping bash in Lua/tool indirection.** Two coupled changes — skills
become files the agent `cat`s (the `skill` tool goes away), and custom tools
become declarative bash-command templates (the Lua `handler` function, plus the
`shell3.bash`/`shell3.http`/`shell3.urlencode` helpers, go away).

Hard cut: bash-first is unmerged and single-user, so there is no deprecation
shim — the old shapes are removed, not aliased.

## Motivation

The bash-first creed (CLAUDE.md): the agent's verbs are `bash` and `edit_file`;
*everything else is a file it reads or a command it runs.* Two surfaces still
violate this:

1. **The `skill` tool.** Skills are surfaced two-stage: a `## Skills` index in
   the system prompt (name + description), with the full body fetched on demand
   via a dedicated `skill` tool (`tooldefs.go`, `dispatch.go`). But a skill body
   is just text — it should be a file the agent `cat`s, not a bespoke tool.
2. **Custom tools wrap bash in Lua.** `shell3.tool{ handler = function(args) … }`
   runs a Lua function that typically just shells out via `shell3.bash`/
   `shell3.http`. That's three layers (model → Lua VM → bash) for what is almost
   always "run this command with these arguments."

## 1. File-backed skills

### Lua surface
```lua
shell3.skill({
  name        = "history",
  description = "Query past sessions with read-only sqlite.",
  path        = "lib/skills/history.md",   -- relative to the config dir
})
```
- **`path` replaces `body` and `body_cmd` for skills.** `body`/`body_cmd` are
  removed from the skill schema. (Phase 9's `prompt_cmd` is unaffected: it stays
  on agents/subagents, whose prompts inject into the system prompt and are not
  cat-able.)
- `path` is resolved relative to the config dir (same base as `.env`/`lib`, the
  dir `luacfg.Load` already knows). The skill struct stores the resolved
  absolute path.
- The Lua call still returns a handle (`__skill` sentinel) so `skills = { history }`
  grants work unchanged.

### Validation (fail closed, at load)
At load/reload, for each declared skill: the resolved `path` must exist, be a
regular readable file, and be non-empty. Any failure fails `Load` with a clear
message naming the skill and path — mirroring the existing cross-reference and
`*_cmd` discipline. A broken skill path is caught at load, never at turn time.

### System-prompt index
`BuildPersonaFor` (`persona.go`) renders, when `SkillsActive()`:
```
## Skills
Read a skill's file with `cat` when it applies.
- history (/abs/path/to/lib/skills/history.md): Query past sessions with read-only sqlite.
- self-evolve (/abs/path/to/lib/skills/self_evolve.md): How to safely change your own config and reload.
```
The absolute path lets the agent `cat` it regardless of its working directory.

### Removals
- Delete `skillTool` (`tooldefs.go`) and stop appending it in `ToolDefs`.
- Delete the `name == "skill"` branch in `CallTool` (`dispatch.go`).
- `tools = { skill = false }` still sets `SkillsDisabled`; its only effect now is
  suppressing the `## Skills` index (there is no tool left to gate).

### Migration
- In-repo scaffold skills become a `.md` body file + a thin registration:
  `internal/scaffold/defaults/base/lib/skills/<name>.lua` →
  `shell3.skill({ name, description, path = "lib/skills/<name>.md" })` plus a
  sibling `<name>.md`. (The current `brainstorming`/`history` inline-body Lua
  modules are split this way.)
- The user's personal `~/.shell3/lib/skills/*.lua` (self_evolve, scheduling_jobs,
  poem_writing, brainstorming, browser) get the same split (done as a follow-up
  config edit, outside the repo).

## 2. Bash-template custom tools

### Lua surface
```lua
shell3.tool({
  name        = "brave_search",
  description = "Search the web via Brave; returns titles, URLs, snippets.",
  parameters  = {                       -- UNCHANGED: full JSON schema
    type = "object",
    properties = {
      query = { type = "string",  description = "The search query." },
      count = { type = "integer", description = "Results to return (default 10)." },
    },
    required = { "query" },
  },
  secrets  = { "BRAVE_API_KEY" },        -- exported into the command env
  command  = [[
    curl -sf -G "https://api.search.brave.com/res/v1/web/search" \
      -H "Accept: application/json" \
      -H "X-Subscription-Token: $BRAVE_API_KEY" \
      --data-urlencode "q=$query" --data "count=${count:-10}" \
    | jq -r '.web.results[]? | .title + "\n" + .url + "\n" + (.description // "") + "\n---"'
  ]],
  background = false,                     -- optional; default false
  timeout    = 20,                        -- optional; default = bash tool default
})
```
- **`handler` is removed**; `command` (a bash string) replaces it.
- `parameters` keeps its existing JSON-schema shape and conversion. The only new
  fields are `command` (required), `secrets` (optional list), `background`
  (optional bool), `timeout` (optional int).

### Parameter → environment rule
- The model's typed args are injected into the command's environment, **not**
  string-interpolated into the command text. Injection-safe: a value can never
  become shell syntax.
- **Param names must be lowercase** `^[a-z][a-z0-9_]*$`, validated at load.
  Secrets are UPPERCASE by `.env` convention. Standard env vars are uppercase.
  → params, secrets, and ambient env never collide, and a param can never
  clobber `PATH`/`HOME`/`IFS`.
- Value encoding: string/number/bool → their string form; array/object → compact
  JSON. An omitted optional param is simply unset (`$query` empty, or
  `${count:-10}` supplies a default) — bash-native.

### Secrets
- `secrets = { "NAME", … }`: at call time each name is looked up in the loaded
  `.env` and exported into the command's environment. The secret value never
  appears in the command string, so it stays out of logs, the bash audit, and
  `wrap_bash`. An undeclared/missing secret fails the tool call with a clear
  error (not a silent empty value).

### Execution
- At call time the flow is **pure Go — no Lua VM** (the command is a fixed string
  resolved at load): build env = inherited process env + params + secrets, then
  run via the existing bash-tool runner (timeout, output cap, color forwarding).
- `background = false` (default): blocking. Returns the command's **stdout**
  (trimmed). On non-zero exit, returns an error-shaped string including the exit
  code and stderr so the model sees the failure.
- `background = true`: dispatched through the existing `bash_bg` + sink
  machinery. Returns a pid + log path immediately; a `bg_done` pointer
  notification arrives on its own when it exits (the agent reads the log).

### Security model
A custom tool is a **trusted, author-defined template**; the model supplies only
typed parameters (as env values), never command text. Therefore the command does
**not** pass through `wrap_bash` (which inspects command strings and could not
see env values anyway). Safety rests on the author writing a sound template —
consistent with bash-first "unsafe by default." This is documented at the
`shell3.tool` surface.

### Removals
- Delete `shell3.bash` (`luaBash`), `shell3.http.{request,get,post}`
  (`lua_http.go`), and `shell3.urlencode` (`luaURLEncode`) — handler-only helpers
  with no remaining caller. (`curl --data-urlencode` and `jq` replace them.)
- Keep `shell3.env.secret` (still used for model `api_key` and the telegram
  token) and `shell3.wrap_bash` (the bash safety hook).
- `CustomTool` loses its `handler *lua.LFunction`; gains `Command string`,
  `Secrets []string`, `Background bool`, `Timeout int`. The `CallTool`
  custom-tool path stops driving the VM.

## 3. Scaffold tool migration
- `brave_search` → the bash template above (`curl … | jq`,
  `secrets = { "BRAVE_API_KEY" }`).
- `web_fetch` → rewritten as `curl -sL "$url" | python3 -c '<strip tags +
  extract links>'` (python3 is near-universal on macOS/Linux). Slightly less
  polished than the old Lua tag-stripper; acceptable.

## Affected files (indicative)
- `internal/luacfg/luacfg.go` — `Skill` (`Body`/`BodyCmd` → `Path`), `CustomTool`
  fields; skill-path validation + command resolution in `Load`.
- `internal/luacfg/register.go` — `skillKeys` (`path`), `toolKeys`
  (`command`/`secrets`/`background`/`timeout`, drop `handler`), param-name
  validation.
- `internal/luacfg/tooldefs.go` — delete `skillTool`.
- `internal/luacfg/dispatch.go` — delete `skill` branch; rewrite custom-tool
  dispatch to env-injected bash exec (fg + bg).
- `internal/luacfg/persona.go` — `## Skills` index with paths.
- `internal/luacfg/lua_bash.go` — delete `luaBash`; keep `WrapBash`.
- `internal/luacfg/lua_http.go`, `lua_misc.go` (urlencode) — delete helpers.
- `internal/luacfg/register.go` (`registerShell3`) — unregister removed globals.
- Custom-tool background exec — reuse `internal/bgjobs` + sink wiring (likely a
  small bridge from luacfg/chat to the existing `bash_bg` path).
- `internal/scaffold/defaults/**` — split skills into `.md` + registration;
  rewrite `lib/tools.lua`; update `shell3.lua.tmpl` examples.
- Tests: luacfg (skill path validation, param-name rules, env injection, secret
  export, fg/bg dispatch, removed-helper absence), scaffold, chat custom-tool.
- Docs: `CLAUDE.md` (tool count / skill mechanism), `docs/cookbook/*`,
  the bash-first design doc.

## Testing
- Skill: `path` resolves + indexes with abs path; missing/empty/non-file path →
  `Load` error; `skill=false` suppresses the index; no `skill` tool def emitted;
  reload re-validates.
- Tool: param names rejected when not lowercase; params reach the command as env
  (incl. JSON for non-scalars, unset for omitted optionals); declared secret
  exported and absent from the command string; missing secret → call error;
  foreground returns stdout / surfaces non-zero exit; `background=true` returns a
  pid+log and emits a `bg_done` notification; removed helpers
  (`shell3.bash`/`http`/`urlencode`) are nil in the VM.
- Green: `go build ./...`, `go test ./...`, `gofmt -l .` empty, `go vet ./...`.

## Out of scope
- Per-call (model-chosen) background — `background` is a fixed tool property.
- Any change to the five core tools (`bash`, `edit_file`, `bash_bg`,
  `read_media`, `shell_interactive`) beyond deleting the conditional `skill` tool.
- Reintroducing an approval/guard flow.
