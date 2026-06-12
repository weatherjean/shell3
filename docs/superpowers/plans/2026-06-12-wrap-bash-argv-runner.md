# wrap_bash argv runner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `shell3.wrap_bash` return an argv table (list of strings) that is exec'd directly, so the one bash hook can swap the *runner* (docker/ssh/custom CLI), not just rewrite the command string.

**Architecture:** `LoadedConfig.WrapBash` changes its allow-result from a rewritten string to an argv `[]string`. A string return still maps to `{"bash","-c",str}` (back-compat); a table return is validated and exec'd verbatim; nil/false still blocks. The two exec primitives (`runBashCapture`, `bgjobs.Start`) take an argv instead of a literal `bash -c command`. Custom command-template tools keep deliberately bypassing `wrap_bash` and just pass the default `{"bash","-c",cmd}` argv.

**Tech Stack:** Go, gopher-lua (`github.com/yuin/gopher-lua`), existing test helpers in `internal/luacfg` and `internal/chat`.

**Scope note:** Custom command-template tools (`dispatchCustomTool`) intentionally bypass `wrap_bash` (the template is trusted author config; the model supplies only env values). They are NOT wrapped — an author who wants a sandbox bakes it into their own template. The runner applies only to the two seams that already honor `wrap_bash`: the **bash** tool and **bash_bg / subagents**.

---

## File map

- `internal/luacfg/lua_bash.go` — `WrapBash` returns `[]string`; add `luaStringList` validator. (core logic)
- `internal/luacfg/wrap_bash_test.go` — update existing assertions to argv; add table + fail-closed cases.
- `internal/chat/toolhandler.go` — two `WrapBash func(...)` type decls change return to `[]string`.
- `internal/chat/chat.go` — one `WrapBash func(...)` type decl changes return to `[]string`.
- `internal/agentsetup/agentsetup.go` — the wiring closure's return type changes to `[]string`.
- `internal/chat/handler_bash.go` — `runBashCapture` takes `argv []string`; `Execute` builds argv via the hook (or default) before calling it.
- `internal/chat/handler_bash_bg.go` — `Execute` builds argv via the hook; passes argv + display string to `bgjobs.Start`.
- `internal/bgjobs/bgjobs.go` — `Start` takes `argv []string` + `display string`; execs `argv[0] argv[1:]`.
- `internal/chat/tools.go` — custom-tool fg/bg calls pass `{"bash","-c",rt.Command}` + display.
- `internal/chat/handler_bash_wrap_test.go` — update fake `WrapBash` fns to the argv signature; add a runner-swap test.
- `internal/bgjobs/bgjobs_argv_test.go` — new: argv is exec'd in the background seam.
- `internal/scaffold/...` (the embedded `shell3.lua`) — document the three return shapes.
- Cookbook doc — sandbox recipe.

---

## Task 1: WrapBash returns argv (luacfg core)

**Files:**
- Modify: `internal/luacfg/lua_bash.go`
- Test: `internal/luacfg/wrap_bash_test.go`

This task is self-contained: `internal/luacfg` does not import `chat`/`agentsetup`, so `go test ./internal/luacfg/` compiles and runs green on its own even though the rest of the module won't build until Task 2.

- [ ] **Step 1: Update the existing tests to the argv signature (failing)**

In `internal/luacfg/wrap_bash_test.go`, add this helper at the bottom of the file:

```go
// argvEq reports whether got equals the want elements in order.
func argvEq(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
```

Replace the body of `TestWrapBashAllowPassthrough` (the part after `c := loadWrap(...)` and the `HasWrapBash` check) so the result is argv:

```go
	got, allowed, reason, err := c.WrapBash(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatalf("expected allowed, blocked with reason %q", reason)
	}
	if !argvEq(got, "bash", "-c", "echo hi") {
		t.Fatalf("passthrough argv wrong: %q", got)
	}
```

Replace the tail of `TestWrapBashRewrite`:

```go
	if !argvEq(got, "bash", "-c", "echo SAFE") {
		t.Fatalf("expected bash -c rewrite argv, got %q", got)
	}
```

In `TestWrapBashBlockWithReason`, replace the final `got` assertion (a block now returns a nil argv, not the original string):

```go
	// A block returns a nil argv (the caller ignores it and surfaces reason).
	if got != nil {
		t.Fatalf("block should return nil argv, got %q", got)
	}
```

- [ ] **Step 2: Add new table + fail-closed tests (failing)**

Append these tests to `internal/luacfg/wrap_bash_test.go`:

```go
// TestWrapBashArgvTable: a table of strings is exec'd verbatim (runner swap).
func TestWrapBashArgvTable(t *testing.T) {
	c := loadWrap(t, `function(cmd) return {"zsh", "-c", cmd} end`)
	got, allowed, _, err := c.WrapBash(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("argv table should be allowed")
	}
	if !argvEq(got, "zsh", "-c", "echo hi") {
		t.Fatalf("argv table not passed through, got %q", got)
	}
}

// TestWrapBashArgvFailsClosed: malformed argv tables block (fail closed).
func TestWrapBashArgvFailsClosed(t *testing.T) {
	for _, body := range []string{
		`function(cmd) return {} end`,                 // empty list
		`function(cmd) return {"bash", "-c", 42} end`, // non-string element
		`function(cmd) return {foo="bar"} end`,        // map-style, no array part
	} {
		c := loadWrap(t, body)
		got, allowed, reason, err := c.WrapBash(context.Background(), "ls")
		if err != nil {
			t.Fatal(err)
		}
		if allowed {
			t.Fatalf("malformed argv %q must fail closed, got allowed", body)
		}
		if got != nil {
			t.Fatalf("blocked argv should be nil, got %q", got)
		}
		if !contains(reason, "wrap_bash error") {
			t.Fatalf("expected wrap_bash error reason for %q, got %q", body, reason)
		}
	}
}
```

Note: `TestWrapBashFailsClosedOnBadReturn` already includes `return {} end` and expects fail-closed — that stays correct (empty table is still rejected). Leave it unchanged except it compiles against the new signature (it uses `_` for the argv, so no edit needed).

- [ ] **Step 3: Run the tests to verify they fail to compile / fail**

Run: `go test ./internal/luacfg/ -run WrapBash -v`
Expected: compile error (`WrapBash` still returns `string`) or assertion failures — proves the new contract isn't implemented yet.

- [ ] **Step 4: Implement the argv return in `lua_bash.go`**

Replace the whole `WrapBash` method and add the `luaStringList` helper. The new method:

```go
// WrapBash runs the registered shell3.wrap_bash hook against cmd and returns the
// argv to exec, whether it is allowed, and an optional block reason. The hook may
// return: a string (run under `bash -c <string>`), a table of strings (the argv
// to exec directly — a runner swap, e.g. {"docker","exec","c","bash","-c",cmd}),
// or nil/false[, reason] to block. It locks the VM and honors ctx via
// L.SetContext. Callers skip WrapBash entirely when no hook is declared.
//
// FAIL CLOSED. wrap_bash is the ONLY bash safety surface, so a broken hook must
// block, not run: any Lua error, a wrong return type, an empty argv table, or a
// table with a non-string element returns allowed=false with a reason. On a
// block the returned argv is nil.
func (c *LoadedConfig) WrapBash(ctx context.Context, cmd string) (argv []string, allowed bool, reason string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.L.SetContext(ctx)
	if e := c.L.CallByParam(lua.P{Fn: c.wrapBash, NRet: 2, Protect: true}, lua.LString(cmd)); e != nil {
		// Fail closed: a hook that errors blocks rather than runs.
		return nil, false, "wrap_bash error: " + e.Error(), nil
	}
	// Two return values: the verdict (string | table | nil | false) and an
	// optional reason.
	r2 := c.L.Get(-1)
	r1 := c.L.Get(-2)
	c.L.Pop(2)
	switch v := r1.(type) {
	case lua.LString:
		// A string runs under bash -c (back-compat with the string-rewrite era).
		return []string{"bash", "-c", string(v)}, true, "", nil
	case *lua.LTable:
		// A list of strings is the argv to exec directly (the runner swap).
		list, ok := luaStringList(v)
		if !ok {
			return nil, false, "wrap_bash error: argv table must be a non-empty list of strings", nil
		}
		return list, true, "", nil
	case *lua.LNilType:
		// nil → block; second return (if a string) is the reason.
		return nil, false, optReason(r2), nil
	case lua.LBool:
		if bool(v) {
			// true is not a command — a hook must return the command string or an
			// argv table to allow. Fail closed on this misuse.
			return nil, false, "wrap_bash error: returned boolean true (return a command string or an argv table to allow, nil+reason to block)", nil
		}
		// false → block; second return (if a string) is the reason.
		return nil, false, optReason(r2), nil
	default:
		// Any other type (number, function, …) is a broken hook: fail closed.
		return nil, false, "wrap_bash error: hook returned " + r1.Type().String() + " (must return a command string, an argv table, or nil/false[+reason])", nil
	}
}

// luaStringList converts a Lua list table to []string. It returns ok=false when
// the table is empty or any element 1..N is not a string (a hole reads as nil,
// which is not a string, so it is rejected). This is fail-closed input
// validation for the wrap_bash argv shape — a map-style table has Len()==0 and
// is rejected as empty.
func luaStringList(t *lua.LTable) ([]string, bool) {
	n := t.Len()
	if n == 0 {
		return nil, false
	}
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		s, ok := t.RawGetInt(i).(lua.LString)
		if !ok {
			return nil, false
		}
		out = append(out, string(s))
	}
	return out, true
}
```

Leave `optReason` and `luaWrapBash` unchanged.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/luacfg/ -run WrapBash -v`
Expected: PASS (all `TestWrapBash*` green, including the new argv and fail-closed cases).

- [ ] **Step 6: Commit**

```bash
git add internal/luacfg/lua_bash.go internal/luacfg/wrap_bash_test.go
git commit -m "feat(bash-first): wrap_bash returns argv (runner swap), fail-closed validated"
```

---

## Task 2: Thread argv through the execution seams

**Files:**
- Modify: `internal/chat/toolhandler.go`, `internal/chat/chat.go`, `internal/agentsetup/agentsetup.go`, `internal/chat/handler_bash.go`, `internal/chat/handler_bash_bg.go`, `internal/bgjobs/bgjobs.go`, `internal/chat/tools.go`
- Test: `internal/chat/handler_bash_wrap_test.go`, `internal/bgjobs/bgjobs_argv_test.go` (create)

This is one compile unit — the `WrapBash` func-type change ripples across packages, so all signature edits land together and the module builds green only at the end of the task.

- [ ] **Step 1: Change the `WrapBash` func-type return to `[]string` in all three decls**

In `internal/chat/toolhandler.go`, both occurrences (around lines 73 and 132):

```go
	WrapBash func(ctx context.Context, cmd string) (argv []string, allowed bool, reason string, err error)
```

In `internal/chat/chat.go` (around line 122):

```go
	WrapBash func(ctx context.Context, cmd string) (argv []string, allowed bool, reason string, err error)
```

- [ ] **Step 2: Update the agentsetup wiring closure**

In `internal/agentsetup/agentsetup.go` (around line 298):

```go
	if p.lc.HasWrapBash() {
		cfg.WrapBash = func(ctx context.Context, cmd string) ([]string, bool, string, error) {
			return p.lc.WrapBash(ctx, cmd)
		}
	}
```

- [ ] **Step 3: Make `runBashCapture` take an argv**

In `internal/chat/handler_bash.go`, change the signature and the exec call. Replace the function header comment + signature and the `exec.CommandContext` line:

```go
// runBashCapture runs argv (argv[0] with argv[1:] as args) in workdir with
// extraEnv appended to os.Environ() (nil = inherit only), capturing combined
// stdout+stderr, honoring timeout + cancellation. It returns the elided output
// and the process exit code (124 on timeout, -1 on a start error). Shared by the
// bash tool and foreground command-template tools. argv must be non-empty.
func runBashCapture(ctx context.Context, argv []string, workdir string, extraEnv []string, timeout time.Duration) (string, int) {
	if len(argv) == 0 {
		return "error: empty command argv\n", -1
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(tctx, argv[0], argv[1:]...)
```

Leave the rest of the function body unchanged.

- [ ] **Step 4: Build argv in the bash tool's `Execute`**

In `internal/chat/handler_bash.go`, replace the `Execute` body from the `parseBashArgsFull` line through the `runBashCapture` call:

```go
	command, timeout := parseBashArgsFull(string(args))
	// shell3.wrap_bash: the only bash safety surface. Default argv runs the
	// command under bash -c; a declared hook may rewrite it, swap the runner
	// (argv table), or block. A nil hook means no wrapping (the unsafe default).
	argv := []string{"bash", "-c", command}
	if cfg.WrapBash != nil {
		a, allowed, reason, err := cfg.WrapBash(ctx, command)
		if err != nil {
			return "error: wrap_bash failed: " + err.Error(), nil
		}
		if !allowed {
			return "error: blocked by wrap_bash: " + reason, nil
		}
		argv = a
	}
	out, _ := runBashCapture(ctx, argv, cfg.WorkDir, nil, timeout)
	return out, nil
```

- [ ] **Step 5: Make `bgjobs.Start` take an argv + display string**

In `internal/bgjobs/bgjobs.go`, change `Start`'s signature, its empty-check, the exec call, and the two `Cmd:` assignments. Header:

```go
// Start spawns argv (argv[0] with argv[1:] as args) detached in workdir. display
// is the human-readable command recorded in bg.json and the sink notification
// (it may differ from argv when a wrapper swapped the runner). env is appended to
// the inherited environment; sinkPath/notifyOnExit drive the bg_done pointer.
func Start(argv []string, display, workdir string, env []string, sinkPath string, notifyOnExit bool) (Job, error) {
	if len(argv) == 0 {
		return Job{}, fmt.Errorf("argv is required")
	}
```

Change the exec construction (was `exec.Command("bash", "-c", command)`):

```go
	c := exec.Command(argv[0], argv[1:]...)
```

Change the sink notification `Cmd:` (was `Cmd: command,`):

```go
		Cmd:  display,
```

Change the `Job{...}` literal `Cmd:` (was `Cmd: command,`):

```go
		Cmd:       display,
```

- [ ] **Step 6: Build argv in the bash_bg `Execute`**

In `internal/chat/handler_bash_bg.go`, replace the `wrap_bash` block and the `bgjobs.Start` call. Replace from the `if cfg.WrapBash != nil {` block through the `bgjobs.Start(...)` line:

```go
	// shell3.wrap_bash applies to bash_bg too: rewrite, swap the runner, or
	// block before the command is backgrounded. Nil hook = no wrapping.
	argv := []string{"bash", "-c", p.Command}
	if cfg.WrapBash != nil {
		a, allowed, reason, err := cfg.WrapBash(ctx, p.Command)
		if err != nil {
			return "error: wrap_bash failed: " + err.Error(), nil
		}
		if !allowed {
			return "error: blocked by wrap_bash: " + reason, nil
		}
		argv = a
	}
	// Display the original command in bg.json/sink regardless of any runner swap.
	job, err := bgjobs.Start(argv, p.Command, wd, nil, cfg.SinkPath, notifyOnExit)
```

- [ ] **Step 7: Update the custom-tool bypass call sites**

In `internal/chat/tools.go`, the custom-template path keeps bypassing `wrap_bash` and just uses the default argv. Background call (around line 75):

```go
		job, err := bgjobs.Start([]string{"bash", "-c", rt.Command}, rt.Command, cfg.WorkDir, rt.Env, cfg.SinkPath, true)
```

Foreground call (around line 89):

```go
	out, code := runBashCapture(ctx, []string{"bash", "-c", rt.Command}, cfg.WorkDir, rt.Env, timeout)
```

- [ ] **Step 8: Update the chat wrap-test fakes to the argv signature**

In `internal/chat/handler_bash_wrap_test.go`, change the blocking fake (around line 22):

```go
		WrapBash: func(_ context.Context, cmd string) ([]string, bool, string, error) {
			return nil, false, "blocked", nil
		},
```

and the rewriting fake (around line 48) — note it now returns an argv:

```go
		WrapBash: func(_ context.Context, _ string) ([]string, bool, string, error) {
			return []string{"bash", "-c", "touch " + rewritten}, true, "", nil
		},
```

(If the blocking test asserts a specific reason string, keep that assertion; only the closure signature/return changed.)

- [ ] **Step 9: Add a runner-swap test proving argv is not re-parsed**

Append to `internal/chat/handler_bash_wrap_test.go`:

```go
// TestBashHandler_WrapBashSwapsRunner proves an argv table is exec'd
// positionally: the command lands as a single argv element, NOT re-parsed by an
// outer bash -c. A shell-metachar payload passed as $1 must print verbatim and
// must not execute.
func TestBashHandler_WrapBashSwapsRunner(t *testing.T) {
	payload := `a b "c"; echo PWNED`
	cfg := ToolConfig{
		WorkDir: t.TempDir(),
		WrapBash: func(_ context.Context, _ string) ([]string, bool, string, error) {
			// $0="_", $1=payload — printf echoes $1 with no re-parsing.
			return []string{"bash", "-c", `printf '%s' "$1"`, "_", payload}, true, "", nil
		},
	}
	out, err := BashHandler{}.Execute(context.Background(), "id", json.RawMessage(`{"command":"ignored"}`), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out != payload {
		t.Fatalf("argv not passed through verbatim: got %q want %q", out, payload)
	}
	if contains(out, "PWNED") {
		t.Fatalf("payload was re-parsed and executed: %q", out)
	}
}
```

Check the existing imports at the top of `handler_bash_wrap_test.go`; add `"encoding/json"` if absent. If a `contains` helper is not already in the `chat` test package, use `strings.Contains(out, "PWNED")` and add `"strings"` to the imports instead.

- [ ] **Step 10: Add a background argv test**

Create `internal/bgjobs/bgjobs_argv_test.go`:

```go
package bgjobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStartExecsArgv proves Start execs the given argv (not a literal bash -c
// command) and records the display string as Cmd.
func TestStartExecsArgv(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	argv := []string{"bash", "-c", "echo ok > " + marker}
	job, err := Start(argv, "display-cmd", dir, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if job.Cmd != "display-cmd" {
		t.Fatalf("Cmd should be the display string, got %q", job.Cmd)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && len(b) > 0 {
			return // argv ran
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("argv background job did not run (marker never written)")
}
```

Note: `Start` writes a log under `paths.BGLogDir()` and appends to `bg.json` in `dir`; that is acceptable in a temp dir. If `Start` requires `paths` to resolve a global dir and that fails under test, gate this test the same way existing `bgjobs` tests handle it (check for a sibling `*_test.go` in the package for the established pattern before adding new setup).

- [ ] **Step 11: Build and run the full suite**

Run: `go build ./... && go test ./...`
Expected: PASS across all packages (luacfg from Task 1, chat, bgjobs, agentsetup). No remaining references to the old string-returning `WrapBash` signature.

- [ ] **Step 12: Commit**

```bash
git add internal/chat internal/bgjobs internal/agentsetup
git commit -m "feat(bash-first): exec wrap_bash argv across bash/bash_bg seams"
```

---

## Task 3: Scaffold + cookbook docs

**Files:**
- Modify: the embedded scaffold `shell3.lua` in `internal/scaffold` (find the file with the `shell3.wrap_bash` comment block).
- Modify: the cookbook doc (find it under `docs/` — the one with existing "recipe" entries referenced in recent commits).

- [ ] **Step 1: Locate the scaffold wrap_bash block**

Run: `grep -rn "wrap_bash" internal/scaffold`
Expected: the embedded starter `.lua` (and possibly a `_test.go` asserting its contents).

- [ ] **Step 2: Document the three return shapes in the scaffold**

In the scaffold `shell3.lua`, update the `wrap_bash` comment block so it documents string / table / nil returns. Replace the existing example region with:

```lua
-- The hook receives the command string and returns one of:
--   * a string      → run it under `bash -c` (rewrite the command text)
--   * a table        → an argv list exec'd directly — swaps the RUNNER, not just
--                      the text. The command is one argv element, so nothing
--                      re-parses it. e.g. sandbox in docker:
--                        return {"docker","exec","mycontainer","bash","-c",cmd}
--                      or swap the shell: return {"zsh","-c",cmd}
--   * nil/false[,why] → block (optionally with a reason string)
-- A broken hook fails CLOSED (the command is blocked), never open.
shell3.wrap_bash(function(cmd) return cmd end)
```

If a scaffold `*_test.go` asserts the file's bytes/contents, update that assertion to match the new comment so `go test ./internal/scaffold/` stays green.

- [ ] **Step 3: Add the sandbox recipe to the cookbook**

Run: `grep -rln "recipe\|cookbook\|MiniMax" docs/` to find the cookbook file (a recent commit added a "model extra-params (MiniMax M3)" recipe to it). Append a section:

```markdown
## Recipe: sandbox bash in a container (wrap_bash argv)

`shell3.wrap_bash` may return an argv table instead of a string. The table is
exec'd directly, so the agent's command runs under a runner you choose — and the
command arrives as a single argv element (nothing re-quotes or re-parses it):

​```lua
shell3.wrap_bash(function(cmd)
  -- block first, if you like:
  if cmd:match("rm%s+%-rf%s+/") then return nil, "refusing rm -rf /" end
  -- then run everything inside a container:
  return {"docker", "exec", "mycontainer", "bash", "-c", cmd}
end)
​```

Swap `docker exec …` for `ssh host`, `firejail --quiet bash -c`, `zsh -c`, or your
own `yourcli run` wrapper. A string return still means "run under `bash -c`".

This applies to the `bash` and `bash_bg` tools (and subagents). Custom
command-template tools bypass `wrap_bash` by design — bake any sandbox into the
tool's own command template.
```

(Remove the zero-width spaces around the inner code fence — they are only here to keep this plan's fences from terminating early.)

- [ ] **Step 4: Build, test, and verify the scaffold still loads**

Run: `go test ./internal/scaffold/... && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold docs
git commit -m "docs(bash-first): document wrap_bash argv runner (scaffold + cookbook)"
```

---

## Self-review notes

- **Spec coverage:** argv return type (T1), all three return shapes incl. back-compat string (T1), fail-closed validation incl. empty/non-string/map (T1), both exec seams threaded (T2), custom-tool bypass preserved (T2 step 7 + scope note), runner-swap proof that cmd isn't re-parsed (T2 step 9), bg coverage parity (T2 step 10), scaffold + cookbook (T3). Spec's "open question" on `bgjobs.Start` is resolved here: argv for exec + a separate `display` string so `bg.json` stays readable.
- **Type consistency:** `WrapBash` returns `(argv []string, allowed bool, reason string, err error)` everywhere — luacfg method, the three func-type decls, and the agentsetup closure. `runBashCapture(ctx, argv []string, workdir string, extraEnv []string, timeout)`. `bgjobs.Start(argv []string, display, workdir string, env []string, sinkPath string, notifyOnExit bool)`.
- **Correction vs spec:** the spec said the runner must cover custom command-template tools "or sandboxing leaks." That was wrong — those tools deliberately bypass `wrap_bash` (trusted author template). This plan keeps the bypass and documents the workaround (author bakes the sandbox into the template). Coverage is the two model-facing seams only.
