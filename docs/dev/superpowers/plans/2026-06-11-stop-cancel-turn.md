# `/stop` Cancels the Turn + Kills Its Work Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `/stop` actually work: cancel the in-flight Telegram turn, kill the processes that turn is running (synchronous `bash`/`node`, `bash_bg` jobs, and in-flight subagents), and immediately yield control so the bot is responsive again.

**Architecture:** Two layers. (1) **Reachability** — run each turn on its own goroutine so the `Run` loop keeps consuming Telegram updates and can process `/stop` mid-turn (today the loop is wedged inside `drainTurn`). (2) **Reach** — `/stop` cancels the turn context (which already SIGTERM/SIGKILLs synchronous `bash` process groups), then additionally kills tracked `bash_bg` jobs and cancels in-flight subagents.

**Tech Stack:** Go. `internal/telegram` (the bot loop + commands), `internal/bgjobs` (background-job kill), `pkg/shell3` (session-scoped subagent cancellation). No new dependencies.

**Source of truth:** the bug write-up in `TODO.md` and the verified internals below (read 2026-06-11 on `main`). Confirm signatures with a quick read before editing; earlier edits shift line numbers.

**Locked behavior (from the owner):** `/stop` kills *transient turn work* — the in-flight turn, its synchronous `bash`/`node` children, `bash_bg` jobs, and in-flight subagents — then yields. It does **NOT** kill intentionally-persistent infrastructure: the detached browser Chrome (the watched window, meant to persist across turns) or model proxies (`internal/modelproxy`, documented to outlive shell3). If that boundary ever needs to change, it's a one-line policy change in the `/stop` handler.

**Verified internals (do not guess):**
- `internal/telegram/bot.go`: `Bot` struct (`cancelTurn context.CancelFunc`, `sess *shell3.Session`, `reload`, no mutex, no busy flag). `Run` (lines ~79-92) is a serial `select` loop calling `handleMsg`. `handleMsg` (~95-144): for a normal message it does `turnCtx, cancel := context.WithCancel(ctx); b.cancelTurn = cancel; reply := b.drainTurn(b.sess.Send(turnCtx, text)); b.cancelTurn = nil; cancel(); ...` — **blocking** on `drainTurn`. A running turn is detected via `b.sess.HasQueuedInput()` → `b.sess.Interject(text)`.
- `internal/telegram/render.go`: `drainTurn(ch <-chan shell3.Event) string` ranges the channel until close.
- `internal/telegram/commands.go` (~63-69): `/stop` → `if c := b.cancelTurn; c != nil { c(); b.sendReply(ctx,"⏹ stopped"); return }` else "nothing running".
- `pkg/shell3/shell3.go`: `Session.Send(ctx, prompt) <-chan Event` runs the turn on a goroutine, honors `turnCtx`; `Session.busy`/`isBusy()`; `s.turnCancel`. `RunParts` threads `turnCtx` → `chat.RunTurn` → `executeToolCalls(ctx,...)` → handler `Execute(ctx,...)`.
- `internal/chat/handler_bash.go` (~38-68): `exec.CommandContext` + `SysProcAttr{Setpgid:true}` + `c.Cancel = syscall.Kill(-pid, SIGTERM)` + `WaitDelay`. **Synchronous bash is already killed on cancel — do not change it.**
- `internal/bgjobs/bgjobs.go`: `Job{ID, PID, Cmd, Log, Workdir, StartedAt}`; `bgjobs.Start` spawns `bash -c` with `Setpgid:true`, persists to `<workdir>/.shell3/bg.json`; `LoadRegistry` reads it. **No kill API exists.**
- `pkg/shell3/subagents.go` (~132-147): subagent runs on `runCtx := rt.baseContext()` (NOT the turn ctx), via `rt.trackSubagent(func(){ ... child.Send(runCtx, ...) ... })`. **Cancelling the turn does not touch subagents today.**

**Build approach:** Task 1 (concurrent loop + reachable cancelling `/stop`) is the critical fix and resolves the brick on its own. Tasks 2 and 3 extend `/stop`'s reach. Task 4 is the sweep. After each: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`.

**Branch:** create `feat/stop-cancel-turn` off `main`.

---

## Task 1: Run the turn concurrently so `/stop` is reachable and cancels it

**Files:**
- Modify: `internal/telegram/bot.go` (turn on a goroutine; mutex-guarded turn state)
- Modify: `internal/telegram/commands.go` (`/stop` reads turn state under the lock)
- Test: `internal/telegram/bot_test.go` (or `commands_test.go` — match the existing harness)

The fix: `handleMsg` must NOT block the `Run` loop. Start the turn on a goroutine and return immediately so the next `select` iteration can read `/stop`.

- [ ] **Step 1: Add mutex-guarded turn state to `Bot`.** In `internal/telegram/bot.go`, add fields to the `Bot` struct:

```go
	mu         sync.Mutex         // guards cancelTurn + turnActive
	cancelTurn context.CancelFunc // non-nil while a turn runs
	turnActive bool               // true from turn start until its goroutine ends
```

(Replace the existing bare `cancelTurn context.CancelFunc` field with these three; ensure `sync` is imported.)

- [ ] **Step 2: Write the failing test.** Append to the telegram test file a test that proves a `/stop` arriving while a turn is in flight cancels it and the loop stays responsive. Use the existing fake `tgClient` + a fake/slow `shell3.Session` seam if one exists; otherwise drive `handleMsg` + `handleCommand` directly with a Send that blocks until its ctx is cancelled. Sketch (adapt to the real harness — read `commands_test.go` first):

```go
func TestStopCancelsInFlightTurn(t *testing.T) {
	b := newTestBot(t) // existing helper; fake client + session
	// A turn whose Send blocks until ctx is cancelled:
	started := make(chan struct{})
	b.sess = fakeSession(func(ctx context.Context) <-chan shell3.Event {
		ch := make(chan shell3.Event)
		go func() { close(started); <-ctx.Done(); close(ch) }()
		return ch
	})
	go b.handleMsg(context.Background(), Msg{ChatID: b.chatID, Text: "do work"})
	<-started // turn is running
	// /stop must be processable WITHOUT the turn having finished:
	b.handleCommand(context.Background(), Msg{ChatID: b.chatID, Text: "/stop"})
	// turn goroutine should unwind promptly:
	// assert b.turnActive becomes false and "stopped" was sent.
}
```

> If the bot has no `fakeSession` seam, this is the moment to add a tiny interface around the two `Session` methods the bot uses (`Send`, `Interject`, `HasQueuedInput`) so it's testable — keep it minimal and follow existing patterns.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/telegram/ -run TestStopCancelsInFlightTurn -v`
Expected: FAIL (today `/stop` can't be processed while `handleMsg` blocks; or the test won't even compile until the seam exists).

- [ ] **Step 4: Make the turn concurrent.** Rewrite the normal-message tail of `handleMsg` so it launches the turn on a goroutine and returns. Replace:

```go
	if b.sess.HasQueuedInput() {
		b.sess.Interject(text)
		return
	}
	stopTyping := b.keepTyping(ctx)
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	reply := b.drainTurn(b.sess.Send(turnCtx, text))
	b.cancelTurn = nil
	cancel()
	stopTyping()
	b.sendReply(ctx, reply)
	b.applyPendingReload(ctx)
```

with:

```go
	b.mu.Lock()
	if b.turnActive {
		b.mu.Unlock()
		b.sess.Interject(text) // steer the running turn; never blocks
		return
	}
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	b.turnActive = true
	b.mu.Unlock()

	stopTyping := b.keepTyping(ctx)
	ch := b.sess.Send(turnCtx, text)
	go func() {
		reply := b.drainTurn(ch)
		stopTyping()
		b.mu.Lock()
		b.cancelTurn = nil
		b.turnActive = false
		b.mu.Unlock()
		cancel()
		b.sendReply(ctx, reply)
		b.applyPendingReload(ctx)
	}()
```

Now `handleMsg` returns immediately; the `Run` loop reads the next update (including `/stop`) while the turn goroutine drains.

- [ ] **Step 5: Make `/stop` read turn state under the lock.** In `internal/telegram/commands.go`, replace the `/stop` case:

```go
	case "/stop":
		b.mu.Lock()
		c := b.cancelTurn
		b.mu.Unlock()
		if c != nil {
			c() // cancels turnCtx → synchronous bash/node process groups get SIGTERM→SIGKILL
			b.sendReply(ctx, "⏹ stopped")
			return
		}
		b.sendReply(ctx, "nothing running")
```

- [ ] **Step 6: Run to verify pass + race**

Run: `go test ./internal/telegram/ -run TestStopCancelsInFlightTurn -race -v` then `go test -race ./internal/telegram/`
Expected: PASS, no race. (The mutex must cover every `cancelTurn`/`turnActive` access.)

- [ ] **Step 7: Build + vet + gofmt**

Run: `go build ./... && go vet ./internal/telegram/ && gofmt -l internal/telegram/`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/telegram/bot.go internal/telegram/commands.go internal/telegram/*_test.go
git commit -m "fix(telegram): run turns concurrently so /stop is reachable and cancels the in-flight turn"
```

---

## Task 2: `/stop` kills `bash_bg` background jobs

**Files:**
- Modify: `internal/bgjobs/bgjobs.go` (add a `KillAll`)
- Modify: `internal/telegram/commands.go` (call it from `/stop`)
- Test: `internal/bgjobs/bgjobs_test.go`

Background jobs are spawned with their own process group (`Setpgid:true`) and recorded in `<workdir>/.shell3/bg.json`, but there is no kill path. Add one and call it from `/stop`.

- [ ] **Step 1: Write the failing test.** Append to `internal/bgjobs/bgjobs_test.go`:

```go
func TestKillAll(t *testing.T) {
	dir := t.TempDir()
	// Start a long sleeper as a tracked bg job.
	job, err := Start(dir, "sleep 60")
	if err != nil {
		t.Fatal(err)
	}
	// It's alive.
	if syscall.Kill(job.PID, 0) != nil {
		t.Fatalf("job %d not alive after Start", job.PID)
	}
	n, err := KillAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("KillAll reported %d killed, want >=1", n)
	}
	// Give the OS a beat, then confirm it's gone.
	time.Sleep(200 * time.Millisecond)
	if syscall.Kill(job.PID, 0) == nil {
		t.Errorf("job %d still alive after KillAll", job.PID)
	}
	// Registry is pruned.
	if jobs, _ := LoadRegistry(dir); len(jobs) != 0 {
		t.Errorf("registry not pruned: %v", jobs)
	}
}
```

> Confirm the real `Start` signature (it may return `(Job, error)` or `(*Job, error)`) and `LoadRegistry`'s shape by reading `bgjobs.go`; adapt the test to match.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bgjobs/ -run TestKillAll -v`
Expected: FAIL — `KillAll` undefined.

- [ ] **Step 3: Implement `KillAll`.** Add to `internal/bgjobs/bgjobs.go`:

```go
// KillAll terminates every tracked background job for workdir and clears the
// registry. Each job runs in its own process group (Setpgid at Start), so we
// signal the whole group (-pid) with SIGKILL. Already-dead PIDs are skipped.
// Returns the number of live jobs signalled.
func KillAll(workdir string) (int, error) {
	jobs, err := LoadRegistry(workdir)
	if err != nil {
		return 0, err
	}
	killed := 0
	for _, j := range jobs {
		if j.PID <= 0 {
			continue
		}
		// kill -0 to check liveness; then kill the process group.
		if syscall.Kill(j.PID, 0) != nil {
			continue // already gone
		}
		if err := syscall.Kill(-j.PID, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	// Clear the registry (best-effort; the file lives under <workdir>/.shell3/bg.json).
	_ = clearRegistry(workdir)
	return killed, nil
}
```

> If a registry-clearing helper does not already exist, add a small `clearRegistry(workdir)` that truncates/removes `<workdir>/.shell3/bg.json` (mirror however `appendJob` computes the path). `syscall` must be imported.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/bgjobs/ -run TestKillAll -v`
Expected: PASS.

- [ ] **Step 5: Call it from `/stop`.** In `internal/telegram/commands.go`, extend the `/stop` case so after cancelling the turn it also kills bg jobs. The bot knows its workdir (`b.workDir`). Update the success branch:

```go
	case "/stop":
		b.mu.Lock()
		c := b.cancelTurn
		b.mu.Unlock()
		killed, _ := bgjobs.KillAll(b.workDir)
		if c != nil {
			c()
			msg := "⏹ stopped"
			if killed > 0 {
				msg += fmt.Sprintf(" — killed %d background job(s)", killed)
			}
			b.sendReply(ctx, msg)
			return
		}
		if killed > 0 {
			b.sendReply(ctx, fmt.Sprintf("⏹ no turn running — killed %d background job(s)", killed))
			return
		}
		b.sendReply(ctx, "nothing running")
```

Add the `bgjobs` + `fmt` imports if missing.

- [ ] **Step 6: Build + test + commit**

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./internal/bgjobs/ ./internal/telegram/`

```bash
git add internal/bgjobs/bgjobs.go internal/bgjobs/bgjobs_test.go internal/telegram/commands.go
git commit -m "feat(telegram,bgjobs): /stop kills tracked bash_bg jobs (process-group SIGKILL)"
```

---

## Task 3: `/stop` cancels in-flight subagents

**Files:**
- Modify: `pkg/shell3/subagents.go`, `pkg/shell3/shell3.go` (session-scoped subagent cancellation)
- Modify: `internal/telegram/commands.go` (call it from `/stop`)
- Test: `pkg/shell3/subagents_test.go` (or wherever subagent tests live)

Today subagents run on `rt.baseContext()` so they outlive the spawning turn (their result arrives after the turn ends). `/stop` should cancel them. The fix: derive each subagent's context from a **session-scoped** cancelable context (so subagents still outlive a single *turn*, but a session-level `Interrupt` can cancel them), and expose a method `/stop` can call.

READ FIRST: `pkg/shell3/subagents.go` (the `runCtx := rt.baseContext()` + `rt.trackSubagent` block) and the `Session`/`Runtime` structs in `shell3.go` to get exact field/method names before editing.

- [ ] **Step 1: Add a session-scoped subagent context + cancel.** On the `Session` (in `shell3.go`), add a context that parents subagent runs and an `Interrupt` method. Design:
  - When subagents are dispatched, parent their `runCtx` off a per-session `subCtx` (created lazily from `rt.baseContext()` and stored with its cancel on the Session, guarded by `s.mu`) instead of directly off `rt.baseContext()`.
  - Add `func (s *Session) CancelSubagents()` that cancels the current `subCtx` (and resets it so future spawns get a fresh one). This kills in-flight subagents without closing the Runtime.

- [ ] **Step 2: Write the failing test.** In the subagent test file, dispatch a subagent whose task blocks on its context, call `Session.CancelSubagents()`, and assert the subagent goroutine returns (e.g. via a tracked done channel or `rt`'s subagent wait). Use the existing fake LLM (`internal/llm/fakellm`) and subagent test patterns already in the package — read a current subagent test to copy the harness.

```go
func TestCancelSubagentsStopsInFlight(t *testing.T) {
	// ... set up Runtime + Session with a fake subagent whose Send blocks on ctx ...
	// spawn the subagent (via the spawn_agent tool path or the internal dispatch)
	// confirm it's running, then:
	s.CancelSubagents()
	// assert the subagent goroutine observed cancellation and exited promptly.
}
```

- [ ] **Step 3: Run to verify failure**, then implement Steps 1's wiring until it passes.

Run: `go test ./pkg/shell3/ -run TestCancelSubagentsStopsInFlight -race -v`
Expected: FAIL → (after implementing) PASS.

- [ ] **Step 4: Call it from `/stop`.** In `internal/telegram/commands.go`, in the `/stop` case after cancelling the turn, add `b.sess.CancelSubagents()`. Fold any cancelled-subagent count into the reply if `CancelSubagents` returns one (optional; a plain call is fine).

- [ ] **Step 5: Build + test + commit**

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./pkg/shell3/ ./internal/telegram/`

```bash
git add pkg/shell3/subagents.go pkg/shell3/shell3.go pkg/shell3/*_test.go internal/telegram/commands.go
git commit -m "feat(shell3,telegram): /stop cancels in-flight subagents (session-scoped subagent context)"
```

---

## Task 4: Verification sweep + docs

**Files:**
- Modify: `TODO.md` (mark the item resolved), `CHANGELOG.md`

- [ ] **Step 1: Full sweep**

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`
Expected: all green.

- [ ] **Step 2: Manual smoke (optional, live).** Against a throwaway HOME telegram config (mirror the pattern other plans use; never the real `~/.shell3`): start a turn that runs `bash` `sleep 120`, send `/stop`, confirm the bot replies "⏹ stopped" promptly and the `sleep` process is gone (`pgrep sleep`).

- [ ] **Step 3: Docs.** In `TODO.md`, remove the `/stop` section (or mark it Done with the commit refs). Add a `CHANGELOG.md` `### Fixed` entry: "`/stop` now cancels the in-flight turn, kills its synchronous `bash`/`node` children and `bash_bg` jobs, and cancels in-flight subagents — and works mid-turn (turns run concurrently so the message loop stays responsive). Persistent browser windows and model proxies are intentionally left running."

- [ ] **Step 4: Commit**

```bash
git add TODO.md CHANGELOG.md
git commit -m "docs(stop): changelog + close the /stop deadlock TODO"
```

---

## Self-Review

**Spec coverage:**
- `/stop` reachable mid-turn (the brick) → Task 1 (concurrent turn goroutine + mutex). ✓
- `/stop` cancels the turn + kills synchronous bash/node → Task 1 cancels `turnCtx`; existing `handler_bash.go` process-group kill does the rest (unchanged). ✓
- Kill `bash_bg` jobs → Task 2 (`bgjobs.KillAll`, process-group SIGKILL, registry pruned). ✓
- Cancel in-flight subagents → Task 3 (session-scoped subagent ctx + `CancelSubagents`). ✓
- Yield turn / bot responsive → Task 1: `handleMsg` returns immediately; loop reads next update. ✓
- Persistent browser + model proxies left alive → explicit non-goal; nothing in the plan kills detached Chrome or `internal/modelproxy`. ✓

**Placeholder scan:** Tasks 1-2 have complete code. Task 3 gives the concrete design + the exact files/anchors to read and a concrete `subCtx`/`CancelSubagents` approach — it instructs reading `subagents.go`/`shell3.go` for exact field names rather than guessing internals not captured here (this plan is a fresh-session handoff; the implementer reads the code). No TBD/TODO.

**Type/consistency:** `b.mu`/`b.cancelTurn`/`b.turnActive` introduced in Task 1 are read in Task 1's `/stop` and reused in Task 2/3's `/stop`. `bgjobs.KillAll(workdir) (int, error)` defined in Task 2, called in Task 2 Step 5. `Session.CancelSubagents()` defined in Task 3, called in Task 3 Step 4. `b.workDir` already exists on `Bot` (verified). The bash process-group kill is explicitly NOT modified.

**Risk note:** Task 1 changes the bot from serial to concurrent-turn. The single-turn invariant is preserved by `turnActive` (a second normal message → `Interject`, never a second `Send`). The `Session` itself also rejects overlapping `Send` with `ErrBusy` as a backstop. Run all telegram tests with `-race`.
