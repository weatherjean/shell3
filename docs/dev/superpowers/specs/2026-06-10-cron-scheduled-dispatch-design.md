# Cron / scheduled dispatch

Date: 2026-06-10
Status: approved (design), pending implementation plan

## Goal

Run scheduled jobs in the always-on shell3 agent: on a cron schedule, dispatch
work to an **isolated subagent** that runs headless and **reports its result
back into the main session**, where the main agent processes it and pushes a
notification (e.g. via the Telegram front-end).

The insight: the Wake bus + subagent-result-delivery + `RunQueued` merged in the
agent-runtime work *are* the "dispatch work and report back" machinery. Cron is
just **a timer that injects work into that machinery.** The only new engine code
is a host-side entry point to spawn a subagent without a model turn.

Independent of Spec A (Telegram + dashboard); it shares the `Runtime` foundation
but the only thing it adds to the engine — `Session.Dispatch` — Spec A doesn't
need. Build A first, then B.

Same two principles as Spec A: **Lua is king** (jobs, schedules, agents,
workdirs, timeouts all declared in `shell3.lua`; Go only runs the ticker), and
**don't reinvent the wheel** (schedule parsing/firing uses the maintained
`github.com/robfig/cron`, not a hand-rolled timer wheel).

## Non-goals (v1)

- **An LLM "scheduler agent."** A heartbeat agent that *reasons* about what/when
  to run is overhead for fixed schedules — a cron expression does it for free.
  Revisit only if adaptive, context-driven scheduling is actually needed.
- **Per-job dedicated sessions.** Jobs report into the **main session** (chosen
  for v1). Job execution is isolated in a subagent; the *result* lands in the
  main thread so you see it inline.
- **Distributed/persistent scheduling.** In-process `robfig/cron`; jobs are
  re-armed from Lua config on each start. No missed-while-down catch-up.
- **Dynamic job editing at runtime.** Jobs are declared in `shell3.lua`; editing
  is "edit config, restart." (A `Dispatch`-backed `/run <job>` bot command for
  manual fire is a cheap optional add.)

## Architecture

```
   Lua  cron { jobs = { {schedule, prompt, agent, workdir}, ... } }
                         │ parsed by luacfg → []CronJob on config
                         ▼
   cmd/shell3 telegram (or any Runtime host) builds a Scheduler
                         │
                         ▼
   internal/cron.Scheduler  (robfig/cron)   one entry per job
        │  on tick:
        ▼
   mainSession.Dispatch(job.Agent, job.Prompt, opts)   ← NEW public method
        │  reuses existing private spawn() path
        ▼
   depth-1 subagent on the Runtime (isolated context, headless)
        │  on finish: deliverSubagentResult(...)  ← already exists
        ▼
   main session inbox  ──► Wake ──► main agent RunQueued turn ──► Telegram push
```

- **`cron { jobs = {…} }` block** in `shell3.lua`, parsed by `luacfg` into
  `[]CronJob{Schedule, Prompt, Agent, WorkDir, Notify}` on the loaded config
  (`Notify` defaults to `true` when the key is absent).
- **`internal/cron` package** (`Scheduler`): wraps `robfig/cron`, arms one entry
  per job, and on each tick calls `Session.Dispatch` on the main session. Owns
  start/stop; created and run by the Runtime host (the `shell3 telegram`
  subcommand, or any embedder).
- **`Session.Dispatch(agent, prompt string, opts DispatchOpts) (id string, err
  error)`** — the one new public method on `pkg/shell3`. A host-initiated entry
  to the existing subagent path: it constructs the same `chat.SpawnRequest` the
  model's `spawn_agent` tool produces and runs it through the existing
  `spawn()` / `deliverSubagentResult()` machinery. Enforces the same depth-1
  limit and agent-allowlist resolution; returns the subagent id.

### Why `Dispatch` is small and safe

Today subagents spawn only via the model calling `spawn_agent` inside a turn
(`chat.SpawnRequest` → `Session.spawn` → result delivered to the parent inbox via
`deliverSubagentResult`). `Dispatch` is the **host-side trigger for the identical
path** — no new spawn/lifecycle/delivery code, just a public function that builds
the request and calls the existing internals. It inherits the merged guarantees:
unique sub ids, `Close()` joins subagent goroutines, depth-limit-1, allowlist
resolution, result-to-parent-inbox + Wake.

### Configuration

```lua
cron = {
  jobs = {
    { schedule = "0 9 * * *",  agent = "explorer", notify = true,
      prompt  = "Summarize my open PRs and anything that needs review today." },
    { schedule = "@hourly",    agent = "code",     workdir = "/path/to/repo", notify = false,
      prompt  = "Run the test suite; if anything fails, summarize the failure." },
  },
}
```

`schedule` accepts standard cron expressions and `robfig/cron` macros
(`@hourly`, `@daily`, `@every 30m`). `agent` must resolve to a registered
subagent (validated at load, reusing the subagent-registry cross-ref check).
`workdir` is optional (defaults to the Runtime root); it rides the existing
per-session/per-spawn `WorkDir` plumbing so a job can run rooted in a repo.

### Notification policy (`notify`, default `true`)

Each job has a `notify` boolean (default `true`) deciding whether its result is
auto-sent to the main chat:

- **`notify = true`** — the result (success **or** error) is delivered into the
  main session → wake turn → front-end push (Telegram). The normal flow.
- **`notify = false`** — a **quiet background job**: on **success** nothing is
  delivered to the main session (no wake, no push) — but on **error/timeout the
  job still breaks the silence** and pushes, so a broken background job can never
  fail silently forever.

Either way the job is a subagent, so its full result + transcript is always
visible in the dashboard's **Subagents** tab (the per-subagent JSONL reader) —
`notify` only governs the *chat push*, never observability.

Mechanically `notify` rides on `DispatchOpts` (see below). `Dispatch` delivers
the result to the parent inbox when `notify || resultIsError`; otherwise it
runs + logs the subagent but skips the parent delivery (no Wake).

## Data flow

1. **Tick.** `robfig/cron` fires a job entry on its schedule.
2. **Dispatch.** The scheduler calls `mainSession.Dispatch(job.Agent,
   job.Prompt, {WorkDir: job.WorkDir, Label: "cron:<name>", Notify: job.Notify})`.
3. **Isolated run.** A depth-1 subagent runs headless in its own context (own
   JSONL stream), not touching the main thread while it works.
4. **Report back.** On finish, the result is labeled (`[cron:<name>] …`). It is
   dropped into the **main session inbox** when `notify` is true, or when the job
   errored/timed out regardless of `notify`; a successful `notify=false` job
   skips this step (transcript-only, no wake).
5. **Wake.** `WakeEvents` signals the host; if the main session is idle the host
   runs `RunQueued` — the main agent sees the cron result as a wake turn and can
   summarize/act on it.
6. **Notify.** The wake turn's output flows through the normal front-end path →
   Telegram push (Spec A). With no front-end, it's still in history/JSONL.

Concurrency: multiple jobs can fire close together; each is an independent
subagent (the merged unique-id + per-subagent goroutine model handles this). The
main session serializes the resulting wake turns at round boundaries, as it does
for model-spawned subagents today.

## Error handling & edge cases

- **Job agent not registered:** caught at config load (cross-ref validation),
  fail-fast with a clear error — never silently no-op.
- **Subagent error/timeout:** the failure is delivered as the job result text
  (`[cron:<name>] error: …`) so it still surfaces in the main thread, rather than
  vanishing. A per-job timeout (default, e.g. 10 min) bounds runaway jobs.
- **Overlapping fire of the same job:** v1 allows overlap (each tick = a fresh
  subagent). If this proves noisy, add `robfig/cron` skip-if-still-running
  wrapper later. Note the choice in code so it isn't a silent cap.
- **Main session busy when results land:** fine — the result queues in the inbox
  and the wake turn runs at the next round boundary (existing behavior).
- **Restart:** jobs re-arm from config; no catch-up for fires missed while down
  (documented non-goal).
- **SIGINT:** stop the scheduler before `rt.Close()`; in-flight subagents are
  joined by `Close()` as today.

## Testing

- **`Dispatch` (unit, `pkg/shell3`):** host-initiated dispatch spawns a subagent,
  result lands in the parent inbox, Wake fires — mirror the existing
  model-spawned subagent tests with `fakellm`. Assert depth-limit still enforced
  (a `Dispatch` from a subagent session is rejected).
- **`notify` gating (unit, `pkg/shell3`):** `notify=true` delivers result + wakes;
  `notify=false` success delivers nothing (no Wake); `notify=false` **error**
  still delivers + wakes (errors break the silence).
- **Scheduler (unit, `internal/cron`):** inject a fake clock / trigger entries
  manually; assert each tick calls `Dispatch` with the right agent/prompt/label;
  start/stop is clean (no goroutine leak).
- **Config (unit, `luacfg`):** `cron` block parses to `[]CronJob`; an unknown
  `agent` fails load; valid schedules accepted, malformed rejected.
- **Integration:** a job firing end-to-end on a fake clock delivers a labeled
  result into the main session and produces a wake turn (`fakellm`).
- `go test ./... && go test -race ./...` green before done.

## Implementation approach

Build with **Sonnet subagents in parallel, then verify/fix**. Disjoint units:
(1) `Session.Dispatch` + tests in `pkg/shell3` (the only engine change — do this
first, it gates the rest), (2) `luacfg` `cron` block + `[]CronJob` + cross-ref
validation, (3) `internal/cron.Scheduler` over `robfig/cron`, (4) wire the
scheduler into the `shell3 telegram` host + scaffold `cron` example + optional
`/run <job>` bot command. Dispatch a Sonnet subagent per unit with precise file
scope; the orchestrator then verifies with `go build ./...`, `go vet`,
`gofmt -l`, `go test -race ./...` and fixes integration gaps.

## Open questions for the implementation plan

- `DispatchOpts` shape: `{WorkDir, Label, Notify}` confirmed; consider adding
  `{Timeout}` per job vs a global default. `Notify` gates parent-inbox delivery
  on success (errors always deliver) — see Notification policy above.
- Default overlap policy (allow vs skip-if-running) — start with allow + a
  logged note; revisit if noisy.
- Whether to add the manual `/run <job>` bot command in v1 (cheap; reuses
  `Dispatch`) — lean yes, it makes jobs testable from the phone.
