# Internals

This is shell3's exhaustive implementation reference. It records the runtime
contracts, architectural rationale, and failure history that are too detailed
for the always-loaded [AGENTS.md](../AGENTS.md). Keep it in lockstep with the
code. Read the relevant section before changing a subsystem whose behavior is
described here; the public guides remain the source for user-facing behavior.
Unless a link says otherwise, source paths are relative to the repository root.

**Two principles decide what belongs in the binary.** Read them before
adding anything; most of what follows is their consequence.

**Do less, and do the necessary well.** What stays in the harness is what
ONLY the harness can do — the transport, the filesystem, the turn. Everything
else is a shell command, and shell3 already has bash. A feature that an agent
could have written for itself is not a feature; it is a decision taken away
from it, shipped as a binary nobody can edit.

**Let the agent do anything — but let it do the doing.** A capability handed
down cannot be inspected, changed, or stretched to the case nobody foresaw. A
capability the agent builds is one it understands, tests, and extends the
morning it needs a fourth format. So the harness teaches rather than
provides: a rule, one worked example, and the seam to hang the work on.

The corollary is a knife: when a built-in and a declared tool can both do the
job, the built-in is the one that has to justify itself. `read_media` could
not — it was an HTTP call any agent could make, welded into the binary, and
its removal (v0.25.0) is the shape every such removal should take. Perception
became a `see` tool the agent declares after asking its operator which model
to point it at; the harness kept only what it alone could do — saving the
attachment, and sending one back.

**Kit config.** The config is a **directory** (default `~/.shell3/`), and its
centre is ONE file: `shell3.sh`, the **kit**, parsed by `internal/kit` and
attached to `Parts` by `agentsetup.LoadKit`. A kit is three elements and
nothing else — declaration blocks (YAML inside a `#---` … `#---` comment
fence), function definitions (the implementation of the block above them), and
heredocs (prose bodies, always `<<'EOF'`). A block binds to the NEXT function
whatever its name, with a **binding ceiling**: it owns that function only if
the function appears before the next declaration block. The file is
definitions-only at the top level (`funcs.go` rejects anything else), which is
what makes loading a kit never execute it and running one tool just
`source <kit>; <fn>`.

Declaration kinds: `agent:` (prompt function under it; `model`, `workdir`,
`use:`, `context:`, and `description` which routes the task tool), `shared:`
(a named group of tools an agent imports via `use:`), `tool:` (`description`,
`params` — typed `string`/`int`/`bool`, reaching the shell function as
ENVIRONMENT VARIABLES, never `$1`), `test:` (harness in
`harness.go`: `tool`, `stub`, `assert_eq`, `assert_contains`, `fail`,
`$KIT_TMP`), `gate:`, `note:`, `command:`, `event:` and `cron:` (`schedule` +
exactly one of `agent:`/`tool:`, `report`/`workdir`), plus the `shell3:` wiring block (models,
telegram, mcp, `runs_keep_days`, `media_keep_days`, `review_model`,
`review_policy`; re-marshalled through the
existing YAML parser by `config.readWiring`; secrets as `env:KEY` from the
sibling `.env`). Scoping is POSITIONAL for `tool:`/`test:` — an
`agent:` or `shared:` block opens a scope — but `gate:`/`note:`/`event:` are
NAMED (`gate: [main, assistant]`), because one function usually governs several
agents and a copy per agent is how two rule sets drift apart. `command:` and
`cron:` are positional in form but scoped to NOTHING: a command is answered by
the front-end, not by a model, and a cron job names its own target agent, so
there is no agent for either to belong to. `cron:` is also the ONE block where
`agent:`/`tool:` are payload rather than a kind key (`decodeBlock` lifts those
two out of the kind scan when `cron:` is set — only those two, so a cron block
also naming `command:`/`gate:` is still refused as ambiguous), and
`schedule:`/`report:` outside a `cron:` block are a load error rather than a
silently ignored key, and the pre-`report:` spelling `direct:` is a load error
anywhere, naming its replacement rather than failing as an unknown field.

`command:` declares a `/verb` the front-end answers itself — no model turn, no
tokens. The function's stdout is the reply (empty posts nothing, so an
idempotent command with nothing to say stays silent; nonzero exit posts the
failure), and everything typed after the verb arrives as `$ARG`. Declared
commands append to `Bot.BotCommands()` — the built-ins plus the kit's, which is
what `shell3 telegram` registers as the `/` menu and what the console
transport sends in its hello — and `installKitCommands` (cmd/shell3/hostwiring.go) re-installs both the
bot's list and the runner on `/reload`, resolving `Parts` per call so a reload
cannot leave a runner sourcing the previous kit — DISPATCH follows a reload, but
Telegram's own `/` autocomplete menu is pushed once at startup
(`apiClient.SetCommands`), so a command added by a reload works when typed and
appears in the menu after a restart. A command named after a built-in is a
LOAD ERROR, not a silent shadow (`kit.ReservedCommands`, pinned against
`telegram.BotCommands()` by `internal/telegram/kitcommands_test.go`): built-ins
are matched first in `handleCommand`'s switch, so the declaration would never
fire.

`event:` subscribes a function to the session event stream and is the only hook
that OBSERVES — stdout ignored, cannot refuse, cannot rewrite, nothing to fail
closed on. `on:` is MANDATORY and names the kinds it receives (`kit.EventNames`,
pinned against `chat.EventKind.String()` by `internal/chat/kit_events_test.go`);
an unfiltered subscriber would fork a shell per streamed token, so the filter is
what makes the hook affordable rather than a convenience, and an unknown kind is
a load error. The seam is `chat.SessionOpts.OnEvent` (a second observer beside
`Sink`, called from `emit` and firing even with no sink installed), wired by
`agentsetup.SessionConfig`, which checks `lc.SubscribesTo` BEFORE rendering the
event to JSON — an unsubscribed kind must cost a map lookup and nothing else.
Delivery then hands off to `agentsetup.eventDispatcher`, one per `Parts` (lazy,
closed from the BuildParts closer stack): a bounded queue (`eventQueueDepth`
256) drained by ONE worker so a subscriber never races itself (the worker
checks `closed` on its OWN select before the two-way one: with both ready Go
picks randomly, so a backlog of slow events could keep winning the draw and
pay the full per-event timeout each time, stretching Close far past the drain
budget meant to bound it — measured at 25-45s against a 15s ceiling), and Post NEVER
blocks — a full queue drops the OLDEST pending event and counts it, because
gaps in an observer's view are recoverable and a stalled turn is not. Payload is
`agentsetup.eventPayload`: `event`/`agent`/`session`/`time` plus the kind's own
fields, with text and tool output capped at `eventTextCap` (4 KB) since the full
content is already in the runs store.

`shell3 health` treats the two families differently ON PURPOSE. `gate:`/`note:`
are DECISION functions whose whole contract is returning a verdict, so health
dry-runs them with a probe payload. `command:`/`event:` are ACTION functions, so
health only checks that each is DEFINED (`config.VerifyHooks` sources the kit
and runs `declare -F`): dry-running a command would post the message it exists
to post, every time someone typed `shell3 health`.

`gate:` runs before every tool call for the agents it names (stdin
`{"name","command","args","headless"}`; stdout `{}` to run,
`{"block":true,"reason":…}` to refuse, `{"review":true,"reason":…}` to
soft-deny (below), `{"command":…}`/`{"argv":[…]}` to
rewrite — bash tools only; nonzero exit, bad JSON or a 10s timeout fails
closed; there is NO ask verdict). `note:` rewrites a tool's result (stdin
`{"name","args","output"}`, stdout `{"output":…}`) and is advice, never a
refusal. An agent no `gate:` names runs UNGATED, with no fallback between
agents. `config.hookRef` is a (kit, function) pair installed by
`SetKitHooks`; running one sources the kit and calls the function, which is
safe precisely because a kit is definitions-only. The scaffold's gate ships
armed: three hard refusals, all irreversible and none
of them the work (machine destruction, credentials, stopping shell3), plus
two judgment calls demoted to
`review` (unread remote code, publishing); everything else runs, including
deletion. The gate does NOT protect itself, deliberately: it shares
shell3.sh with every agent prompt, which the agent edits as ordinary work, so
a write rule on that path would block the self-evolve loop and a narrower one
is theatre. The "do not edit the gate"
refusal text is advice only, and `hooks_test.go` PINS this by asserting
`echo x >> ./shell3.sh` is ALLOWED. A refusal comes in one of TWO shapes, and confusing them is a real
failure mode: `block()` carries the blanket policy ("no alternative command,
path, or tool… stop and tell the operator") and is for rules with no way
forward; `route()` names the sanctioned path instead and keeps escalation as a
fallback rather than the instruction. The credential rules are `route()`s,
because their whole remedy IS an alternative command — a script reading the one
key it needs at point of use. Under the blanket policy they forbade the very
thing they pointed at, and on 2026-08-25 an agent blocked from a Notion token
correctly stopped and asked its operator to paste database URLs by hand while
the sanctioned path was allowed the whole time. Pinned by
`TestScaffoldedGateRoutesInsteadOfDeadEnding`. It is a speed
bump by construction — the agent can rewrite it in two lines of Python — so
real protection is filesystem-level (a dedicated user, `chflags
uchg`/`chattr +i`, a container).

`{"review":true,"reason":…}` is the SOFT deny (mirrors Hermes' smart
approvals, guardian half only): `config` parses it to `ActionReview`,
`chat`'s gateBash resolves it through `ToolConfig.ReviewToolCall`, and
`internal/review.Reviewer` makes one guardian LLM call — command +
gate reason, unquoted `#` comments stripped, XML-delimited, operator
`review_policy` text in the SYSTEM channel only, one word back at temp 0
(`agentsetup.reviewMaxTokens` 1024, not 16, or reasoning models truncate and
every review fails closed). APPROVE runs the ORIGINAL command; DENY,
ESCALATE, garbage, transport error, or a 30s timeout all deny with a
stop-and-tell-the-operator message; 3 CONSECUTIVE denies per agent
(`review.breakerThreshold`, keyed by activeName, reset on approve) escalate
the deny text to a hard stop — text only, prompt-cache-invariant. Bash tools
only: a review verdict on a non-bash tool fails closed (gateNonBashTool),
as does review with no reviewer wired. The reviewer is a DEDICATED client
(`Parts.Reviewer()`, lazy sync.Once; `review_model` wiring key, default =
the main agent's model) — never the agent's own client, whose SetParams
state a reviewer override would corrupt. Verdict precedence: block > review
> argv > command. The reviewer reduces false blocks; it is NOT a containment
boundary — the OS is (docs/security.md).

`context:` is a list of paths re-read at every turn start (`RefreshPrompt`,
wired through `TurnConfig` into `assembleTurnContext`) so a long-lived
conversation sees current file contents, never a session-creation snapshot;
they resolve against the AGENT'S OWN workdir when it declares one, or the
config dir otherwise. Because the file is re-read every turn AND the agent is
handed `edit_file` to maintain it, an unbounded brain file is a cost loop the
agent itself drives and cannot see, so the body is CAPPED
(`config.MaxContextBytes`, 64 KB): over that, `elideMiddle` keeps the head and
the tail and replaces the middle with a marker naming the file and how to
`cat` the rest — head AND tail, because these files reliably drift into a
curated header plus an append-only tail, and a tail-only window would drop the
standing instructions the header exists to carry.
`config.ContextSizeWarnings` reports anything over 32 KB
(`WarnContextBytes`); `shell3 health` prints those and FAILS only on an
over-cap file, since elision is real content loss while mere size is advice —
and it checks kit agents against their own workdir, the path that previously
validated `context:` not at all. Compaction cannot help here (the system
prompt is re-rendered fresh every turn, so nothing it discards comes back
smaller): `chat.warnFixedOverhead` logs once per session when the system
prompt alone exceeds half of `compact_at`, naming the cause instead of leaving
a context-length rejection to be diagnosed later. Skills are FILES, not blocks — `skills/*.md` for the main
agent, `projects/<agent>/skills/*.md` for an employee — indexed into the prompt
by name, description and absolute path, and `cat`'d on demand. Cron jobs are
BLOCKS, not files. `shell3 tool check|run|test <kit>` is the author's loop.

The kit is the ONLY config format. A directory with no `shell3.sh` fails to
load, naming the file to create. There are no migration shims and no
fallback — `config.Load` lifts the kit's `shell3:` block through the strict
YAML parser (`config.readWiring`) and reads `.env`; everything
else — agents, tools, skills, cron — comes from `internal/kit` via
`agentsetup`. `context:` paths are validated by `shell3 health`, against each
agent's own workdir, not at load time.

**Bash-first.** The agent's verbs are `bash`, `bash_bg`, and `edit_file`. The
main agent is bash-first
by default: reading, listing, and searching are bash commands (`cat`/`sed -n`,
`ls`/`find`, `rg`), and a reflexive
`read_file`/`grep`/`write_file` call gets an unknown-tool error carrying a
bash-first redirect back to bash/edit_file. `read`, `list_files` and
`write_file` hit that same redirect, and `use: [read]` is a load error like
any other unknown built-in. `history` (opt-in via `use:`, on the scaffold's main agent by
default) recalls past conversations from the runs store: `{query}` is
ranked FTS5 search over user+assistant text across ALL sessions (tool
output is not indexed; a syntax-invalid query is retried as one quoted
phrase), `{session, around, limit}` reads the transcript around a hit;
read-only, store-nil-safe, handled by `chat.HistoryHandler`. Specialists are
subagents. A **subagent** is an **in-process background job** spawned via
the `task` tool (`{subagent_type, prompt, description}`; returns immediately); the
runtime (`internal/shell3` jobManager) runs it as a child-session goroutine
under a concurrency cap (`background.max_concurrent`, default 8) — no
subprocess, no inbox file, no fsnotify. `bash_bg` is a background shell
command on the same runtime; its full output also tees to
`runs/<session>/jobs/<id>.log` (1 MiB cap, janitor-swept). **Completion
delivery is mail** (`internal/shell3/completion.go`): every finished job —
bash_bg, subagent, follow-up, cron — becomes a `CompletionEvent` and routes
deterministically, no triage turn, no judge model. Delivery is
**restart-durable** (`internal/shell3/outbox.go`, the runs store's `outbox`
table): dispatch persists the event as an opaque-JSON "event" row before
routing and deletes it only after the front-end hand-off returns
(at-least-once — a crash inside the window duplicates the report at the
next boot, never loses it; do not "fix" the ordering), and every job writes
a PID-stamped "running" marker row at start, cleared when it finishes.
Delivery is also **outage-durable**: `CompletionHost.PostCompletion` is
synchronous and returns the send error, and a post the transport rejected
(Telegram outage) keeps its event row — the row id lands in the job
runtime's `undelivered` set, and `Runtime.RedeliverUndelivered` (a 5-minute
`wireHost` ticker, `redeliverEvery`) re-dispatches it until the send lands,
so a ⚠️/⏰/🔔 floor post survives an outage without waiting for a restart.
Redelivery re-runs the whole event, so an owner mailed alongside a failed
post can see the mail twice — at-least-once, same contract. Cron `tool:`
job posts are the exception (no event row exists — nothing dispatched);
their sends discard the error and the next idempotent tick re-posts.
`Runtime.RecoverCompletions` — run once at startup by the long-lived
front-ends (`wireHost`, after `SetCompletionHost`; never `ask`, matching the
janitors) — redelivers leftover event rows (note-tagged "recovered after a
shell3 restart") and reports dead-PID running markers as "was still running
when shell3 stopped; its result was lost" failures; a live-PID marker is a
concurrent process's job (an `ask` beside the bot) and is skipped, and an
`ask` killed mid-job deliberately leaves rows the next bot start surfaces —
a completion is never silently lost, whoever spawned it. Graceful shutdown
threads the same needle: `cancelAll` marks the jobs it kills
(`bgJob.shutdownCancel`), and the router's closing branch drops THOSE
events (their "context canceled" failure is manufactured by the restart —
the kept running marker is the honest boot-time report) while a real
completion that raced SIGTERM keeps its event row for redelivery. Failed: the ⚠️ floor
post always reaches the user, and a live owning session is additionally
mailed (woken) so the agent can react — but an ownerless failure (cron)
stops at the post, never burning a main-model turn per broken tick.
`report:` (`notify.ReportMode`; bash_bg arg, task arg, cron block) is the ONE
axis for what a finish does to the chat, and there is deliberately no second
flag beside it — "post it raw" and "you must answer" are two answers to one
question, and a pair of booleans could state both at once. `raw` (the old
`direct: true`, renamed with no shim — and `direct` is REFUSED at both layers,
not ignored: a load error in a kit, and a "the job was NOT started" tool error
from `handler_task.go`'s `directRemovedMsg`, because `json.Unmarshal` drops
unknown fields and a model still writing `direct: true` from a stale prompt or
its own conversation history would otherwise silently get `auto` — the very
failure this replaced): the raw result
posts straight to the user, and the owning session gets the notice queued
WITHOUT a wake — the next turn has it in context without spending one now.
`always` BINDS the report turn to answer the user: `CompletionEvent.mail()`
sets `Mail.Required` and carries `directText(ev)` as `Mail.Fallback`, and a
front-end whose turn posts nothing posts that instead (below). It never
re-asks the model — a second turn would re-run the judgement that just failed,
at full conversation context, to reach the same answer. A FAILED job drops the
bind whatever the spawner asked (the ⚠️ floor post already told the user, so
binding the turn would only duplicate it).
Before the default: a CLEAN completion whose whole tail is already the
`NO_REPLY` sentinel is never mailed for judgment — `internal/shell3/
completion.go`'s router drops it (`strings.TrimSpace(ev.Tail) != "" &&
strutil.IsNoReply(tail)`, checked before the owner/no-owner branch, after
Failed/raw are already handled above it, and skipped entirely under `always`:
the spawner said the user is waiting, so the job's own output does not get to
overrule that): a live owner gets the notice
queued WITHOUT a wake (same shape as `raw`, so the next turn has it in
context for free); an OWNERLESS one (cron, no live session) starts no fresh
turn at all — the `StartFreshTurn` fallback below is never reached. This is
what kills the cost a frequent idempotent cron tick used to buy just to read
"NO_REPLY" and answer "NO_REPLY": mailing it would spend a main-agent turn
at full conversation context for a report with nothing to judge. (An empty
tail is NOT this case — `strutil.IsNoReply("")` is true, but an empty
`ev.Tail` means "no output captured", the normal shape of a successful
`bash_bg` that prints nothing, and that must still wake its owner.)
Default: the completion is **mail to the agent** — `WakeOwner` queues+wakes
the owning session, or `StartFreshTurn` runs a fresh main-agent session
when none is live (cron, orphans). The completion reaches the model as a
TASK REPORT (`mailText` — labeled system-generated, explicitly "the user
has NOT seen this"); the report turn's reply posts to the user as an ✉️
update — one channel, no separate tool — unless the model replies
NO_REPLY (matched leniently: `strutil.IsNoReply`, any 4+-char tail fragment,
so a reasoning-split provider swallowing "NO" can't turn the sentinel into a
post). The spawner can pass `note: "…"` as context carried into the report.
A report is delivered at the **end** of the turn's context, never grafted
onto an earlier user message (`injectReminder` attaches to the LAST message
only when the user just spoke, otherwise it appends a fresh trailing user
carrier that later reminders coalesce onto) — the old backward walk filed the
newest mail above the assistant's own previous reply, burying the NO_REPLY
instruction it carries under a finished exchange. The full report stays
ephemeral (outbound copy only), but `chat.reportTrace` persists ONE line of
it into history — the first line of each notice, which is why `mailText`'s
opening line is a self-contained "TASK REPORT — <label> (clean|FAILED)"
summary. Without that trace an ✉️ update outlives its cause: the model has no
introspective access to a turn it can no longer see, so "why did you send
that?" gets answered by confabulation. The trace also keeps the transcript
alternating (a wake reply otherwise follows the previous assistant message
with no turn between).
Delivery lands through a front-end `CompletionHost`
(`Runtime.SetCompletionHost`: `PostCompletion` (⏰ for cron origins, 🔔
otherwise; threaded+anchored into the owning session's chat thread when one
is live), `WakeOwner` (its liveness check pairs with the bot's retire lock
so mail never lands in a closing session), `StartFreshTurn` (serialized
FIFO on the single-turn gate)); with no host installed (library/tests,
`shell3 ask`) the raw notice goes straight to the owning session — ask
deliberately stays in that mode so its verbose view sees everything.
Foreground `bash` is capped at 120s
(`timeout_seconds`) precisely because it blocks the turn — longer work
belongs in `bash_bg`. A subagent may run `bash_bg` jobs of
its own; a job that outlives the subagent's main turn keeps the child session
open ("lingering"), and each completion **resumes the subagent for a follow-up
turn** whose summary routes like any task report (capped at 5 follow-up
turns per subagent, after which — or after cancel/failure — the raw job
event routes instead, so a completion is never lost). `task_cancel <sub>`
cascades to the jobs the subagent started. `Runtime.Reload` never refuses
over running background work: it always proceeds — idle
front-end sessions swap onto the new
Parts in place, while a subagent child session or a still-running `bash_bg`
job keeps the Parts (store/MCP handles) it was built with; the old
generation's teardown is deferred ("parked") and runs once every such job
drains, or immediately if nothing is running. Only a busy front-end *turn*
still blocks a reload (`s.isBusy()`). Delegation is
**two levels deep, bounded at DISPATCH and never by hiding the tool**
(`shell3.maxDispatchDepth`): a root session — the conversation, a cron
parent, `ask` — dispatches at depth 1, that subagent may dispatch once more,
and a depth-2 agent's `task` call is refused with `depthRefusal`, an error the
model READS ("delegation stops at two levels and this job was NOT started …
report to your parent exactly what needs delegating and why … do NOT work
around this by calling a model API yourself"). The refusal is the point: an
ABSENT tool is an invitation to improvise, and the failure this replaced was
an employee hand-rolling a `urllib` client against a model API rather than
saying the work needed delegating. So EVERY agent with a peer to dispatch
carries the task family; main is never a target (it is the conversation, not
a worker) and neither is the caller itself, so a kit with exactly one employee
advertises nothing to it — an empty enum, not a hidden tool.
`bgJob.depth` is the field; `dispatchDepthLocked` derives it by walking
`owningSubagentLocked` from the spawning session, so the SAME agent runs at
depth 1 under main and depth 2 under a peer. The concurrency cap
(`background.max_concurrent`) is applied PER DEPTH (`runningCountAtDepth`),
because a lingering parent is still unfinished: counted globally, a full rank
of depth-1 agents would hold every slot and refuse every child they tried to
spawn — and a "cap reached" dead end is exactly what sends an agent back to
improvising. A `bash_bg` job shares its spawner's rank
(`spawnerDepthLocked`): it is that agent's own work, competing with its
siblings, not with its children. Delegation itself is **inferred**: the four
task-family tools (`task`, `task_list`, `task_status <id>`,
`task_cancel <id>`; ids like `sub1`/`bg1`) are advertised iff the kit declares
a peer — the `agent:` block IS the registration, there is no toggle and no
allowlist key.

Results climb the same ladder they went down. `finishSubagent` mirrors
`finishCommand`: a subagent whose parent session belongs to ANOTHER subagent
is injected into that parent (`injectNoticeNoWake`) and a follow-up driver
resumes it, rather than routed at the root. Without that branch a depth-2
result would reach the host as a completion whose owner is not the main
conversation, fall through `WakeOwner` to `StartFreshTurn`, and surface in the
user's chat while the depth-1 agent that asked for it never learned the
answer. When follow-ups are exhausted, poisoned, or the parent's child has
closed, the result still routes — at the PARENT'S own root, labeled "started
by subagent <id>", the same orphan shape `finishCommand` uses.

The dash's index lists running + finished jobs; `/superstop` kills them all
(`Session.KillAllForStop`: snapshot, mark each job `suppress`, cancel — the
suppressed flag makes `dispatchCompletion` DROP those jobs' events, so the
one superstop summary replaces N ⚠️ posts and owner mails; a normal
`KillJob` still routes). The
job-progress stream is `rt.JobEvents()` / `Session.JobEvents()`. Note `Session.Jobs()` reports the whole job runtime,
not one session's share — filter by `JobInfo.ParentID` for per-session
work. The shell is **unrestricted except by the gate**;
the opt-in gate is a **per-agent bash function**: the kit declares
`gate: [names]` and binds the function under it — no fallback, no chaining;
an agent no block names runs ungated, and a `gate:`/`note:` naming an agent
the kit does not declare is a load error. The function
runs before **every** tool (cwd = config dir, 10s timeout)
with JSON on stdin — `{"name", "command" (bash text for the two bash tools,
null otherwise), "args", "headless" (true when no human is attached —
subagents, cron)}` — and prints a verdict: empty/`{}`
(run) / `{"command": …}` (rewrite — bash tools only) / `{"argv": […]}`
(runner-swap — bash tools only; fails closed for non-bash) /
`{"review": true, "reason": …}` (soft deny → the LLM reviewer, see the kit
section above; bash tools only, fails closed unwired) /
`{"block": true, "reason": …}`. There is NO ask verdict: shell3 runs
unattended, where an ask is a denial with a delay — a legacy hook printing
`{"ask": …}` fails closed with a reason naming the removal, never silently
allows. Precedence when several keys are set: block > review > argv >
command.
Nonzero exit, malformed JSON, or timeout **fails closed**. Every verdict that
CHANGES what runs — block, rewrite, and both halves of a review — logs one
WARN line to the app log (`chat.logGateVerdict`, tool + command + reason,
each capped at `gateLogFieldCap` 300 bytes because a command line can carry a
secret the app log is not the place for); an ALLOW logs NOTHING, since the
gate runs before every tool call and logging passes would bury the refusals in
the noise they exist to stand out from. Without this the only record of a
refusal was the block text inside one transcript, so "has the gate ever fired?"
was unanswerable without reading every run. `applog.Logger` therefore lives on
`ToolConfig`, not `TurnConfig` (it is promoted through the embed, so every
`cfg.Log` caller is unchanged): the bash family self-gates inside its handler,
which only ever sees a ToolConfig. A `note:` function
can rewrite a tool's output (e.g. redact secrets): stdin
`{"name","args","output"}`, stdout `{"output": …}`; a failure here also fails
closed (output replaced by an error notice, never passed through
unredacted). **The scaffold's gate ships armed** (`internal/scaffold`,
covered by `internal/scaffold/hooks_test.go`, which sources the shipped kit
and drives its gate with real payloads): credential paths, system-path
writes, force-pushes, and self-termination are refused, and unread
remote code and publishing soft-deny to the reviewer; everything else runs.
The gate draws no line at model calls — a vision call and a drafting call are
the same `/v1/chat/completions` request, and no regex can tell convert from
decide — so that judgment moved out of the gate entirely and onto the daily
`harness-audit` cron (below), whose agent turn reads what a tool's body actually does.
The system-path rule judges the WRITE TARGET, per command SEGMENT
(`os_write`), because a command line is several commands. The previous rule
ANDed "a write verb appears somewhere" with "an OS path appears somewhere" and
so refused `mkdir -p ~/w && /usr/bin/python3 -m venv ~/w/v` for MODIFYING
/usr/bin when it only RUNS it — and counted a bare `>` as a write, making
`2>&1` enough to condemn any command that named a system path. That cost two
whole task attempts on 2026-08-25, each ending in a refusal that also says "do
not work around this", so the agent correctly stopped. Quotes are stripped
before matching: `rm -rf '/usr/bin'` and `> "/etc/hosts"` were both ALLOWED
before, one quote from a bypass. It never asks — shell3 mostly runs unattended,
where an ask parks the turn until it times out and denies anyway — and every
refusal instructs the model not to work around it but to raise it with the
operator (an employee's refusal tells it to stop and hand up to the main
agent instead).

`edit_file`'s file I/O lives in `internal/edittool` (plain direct-disk
functions); `bash` always hits disk directly. Skills are **dir-based**: every
flat `*.md` in `skills/` with a frontmatter `description:` (optional `name:`
defaults to the filename) is one skill. An invalid file is skipped with a
warning that `shell3 health` turns into a failure; an absent dir means no
skills. The agent reads a skill's body with `cat` (skills are indexed by
absolute path in the prompt under `## Skills` — there is no `skill` tool).
Custom tools ARE declarable — a `tool:` block plus the function under it (see
the kit section above). A tool may call a model to CONVERT between forms —
pixels, audio or PDF into text, text into speech or an image — but never to
DECIDE: no score, tier, ranking, draft or summary. For a decision, not
`curl`, not `urllib`, and not `shell3 ask --agent` either: the tool takes the
result as a PARAMETER and the agent writes it in its own turn (the
`lead-save`/`draft-save` shape). Swapping a hand-rolled client for
`shell3 ask --agent` inside a tool stops the key leaking but still leaves the
judgment where it does not belong. Only a standalone operator script, wired
to no tool, shells out to `shell3 ask --agent`.
What is NOT a tool is reusable glue with no model-facing
surface: that stays a wrapper script (canonically `~/.shell3/lib/bin/`) run
through bash, documented by the
scaffold's `scripting` skill; a script that needs a secret reads the one key
it needs from `.env` itself at point of use, so secrets never enter the
conversation or the agent environment. External tool servers come in over
**MCP** (`internal/mcp`, official go-sdk, tools only — stdio + streamable
HTTP, no OAuth/resources/prompts/SSE): the `mcp:` block in the kit wiring
(`command:` argv or `url:` + `headers:`; per-server `timeout`, `allow`/`deny`
tool filters), opted into per agent via the `agent:` block's `mcp: [name, …]`
or `mcp: all` (omitted = none; an opt-in naming an undeclared server is a
load error). Servers connect synchronously in BuildParts
(parallel, per-server timeout; down server = warning + tools absent, never a
build failure; the Manager's Close rides the Parts closer stack so /reload
reconnects fresh). Tools surface as `mcp_<server>_<tool>` in the opted
personas' tool lists and dispatch through the session HostTool path; calls
get one reconnect retry, then the error returns as tool-result text (never
fatal to a turn). The hook sees them like any tool (`name` prefixed,
`command` null). `shell3 health` connects and fails on any down server, and
dry-runs every hook script with a probe payload (script error = failure; a
deliberate block is fine); the dash index lists per-server state.
Context is host-managed via two token thresholds: `prune_at` cheaply stubs
old tool outputs (no LLM call), and `compact_at` triggers tail-preserving
compaction — summarizing the head while keeping recent turns verbatim. The
`prune_at` and `keep_recent` knobs are optional, defaulting to fractions of
`compact_at`; no model-driven prune/compact tools. A *forced* compaction
(`Session.Compact` / `chat.CompactStandalone`) skips the threshold and caps the
verbatim tail at the floor rather than the configured fraction — the automatic
tail is a slice of a large window, so a forced compaction sized that way would
refuse as "nothing to compact" across the whole range where anyone would ask
for one. It is a runtime seam with no front-end command bound to it: the bot
compacts automatically and the dash index reports usage.

**Telegram-first.** shell3 is a personal agent you reach in Telegram — one
chat or several, each holding its OWN conversation. `shell3 telegram` runs
everything (`internal/telegram`): the agent, the bot, and cron. The chat has
**no listener** — the process long-polls the Bot API outbound, so there is no
login and no tunnel; the ONE listener is the read-only web dash on
`127.0.0.1:<dash_port>` (see the commands section).

Authorization is per PERSON, never per room. Two gates run on the update loop
before anything happens (`handleMsg`), in this order: the SENDER must be
allowlisted, and in a GROUP the message must be ADDRESSED to the bot. The
sender check comes first — before the room is even resolved — so a stranger
never enrols a chat, never saves an attachment, and never costs a token,
whichever chat they are in; it also comes before the command branch, since
Telegram delivers `/commands` from every group member and a gate on the turn
path alone would let a stranger `/stop` a turn or `/new` a conversation away.
The trigger gate (`trigger.go`) accepts `/ask <message>`, an `@mention` of
this bot (case-insensitive, boundary-checked, so `@mybottom` is not `@mybot`)
or a REPLY to one of the bot's own messages; everything else in a group is
dropped before `saveAttachments`. A reply is decided from TELEGRAM's author
field (`Msg.ReplyToBot`, set from `ReplyToMessage.From.ID` against the cached
getMe id), not from what this process remembers sending: the in-memory ring
(`sentIDsCap` 200, kept as the fallback for the console transport,
which cannot attribute a replied-to message) is EMPTY after a restart, so a
group whose only live trigger was "reply to me" went deaf on every reboot
until someone @mentioned it again. `/ask` exists because privacy mode never
delivers a plain @mention: it opens a thread with no BotFather toggle and no
admin rights, and the bot's own answer is then a message to reply to. `/help`
explains all of this in-chat, adapting to whether it was typed in a group or
a DM. A DM needs no trigger. **This requires privacy mode OFF in
BotFather** (or the bot promoted admin): a privacy-mode bot is never delivered
a plain `@bot do X` text message at all — only `/cmd@thisbot`, replies to
itself, and inline — so supporting @mention triggers moves enforcement from
Telegram's servers into shell3's own gate, and the room's traffic reaches this
process to be discarded here. A command carrying ANOTHER bot's suffix
(`/stop@otherbot`) is ignored rather than obeyed, and an unknown verb in a
group is answered with silence (someone is talking to a different bot), not
with "unknown command".

Rooms are not declared and not listed: a chat becomes known the first time an
allowlisted person addresses the bot there (`Bot.conv`, created only after
both gates pass — `peekConv` is what routing uses before that, so chatter that
never reaches a turn leaves no phantom room in the registry, the inbox, or the
dash).

The `telegram:` block in the kit wiring is `token` (an `env:TELEGRAM_TOKEN`
reference like every other secret), `chat_id` (the **home chat**: where cron
results and ownerless completions land — NOT an access rule; absent, it falls
back to the DM of the first `allow_from` id, which only delivers once that
person has written to the bot, so `shell3 health` warns), `allow_from` (the
Telegram user ids allowed to DRIVE the agent — `internal/telegram/authz.go`;
decided on `Msg.SenderID`, populated by Telegram and unspoofable. A zero
sender — channel post, or a transport that cannot attribute — is never
allowed; a non-numeric entry fails startup), `max_concurrent_turns` (the
global turn cap, default 4), `workdir` (the agent's shell; default = the
config dir), and `chats:` (per-room settings — `id`, `use_description`,
`context:` — tuning only: declaring a chat neither authorizes nor enrols it).
Missing token, a non-numeric chat_id, or a GROUP chat_id with an empty
`allow_from` (the allowlist's owner fallback would then resolve to nobody, and
the bot would start healthy and ignore every human) refuses to start — naming
the field at fault, and `shell3 health` fails on the same checks (an absent
`telegram:` block is reported, not failed: an `ask`-only config is
legitimate). The transport is an
interface (`client.go`): `client_botapi.go` wraps go-telegram/bot,
`client_console.go` drives the same bot loop over stdin/stdout for
`shell3 telegram --console` (headless event testing, no credentials, no
network). `shell3 telegram --convo-log` writes the WIRE record — every message in and
out, JSONL, to `<config>/convo.jsonl` (rotated by `applog.OpenFile`, 2 MB × 3).
It exists because no other record is complete: the runs store holds what the
MODEL saw, so a HOST-answered reply (`❌ reload failed: …`, `✅ reloaded`, a
/dash link) and every ⚠️/⏰/🔔 completion post write no message row and no app
log line — a failed reload left NO trace on disk, and both of 2026-08-25's were
found only because the operator quoted them back to the bot. `Bot.SetConvoLog`
wraps `b.client` in `convoLogClient` (`convolog.go`), which EMBEDS `tgClient`
rather than listing its methods: a method added later still compiles, silently
unlogged, instead of breaking the build — accepted deliberately, with every
current method an explicit override and a test pinning the set. Inbound is
logged in `Updates`, BELOW both authorization gates, so a message dropped for a
stranger's sender id or for being unaddressed in a group still appears with the
sender and chat type that explain the drop — the one failure that otherwise
leaves no evidence anywhere. Sends log AFTER the call so a rejected post
carries its `err` and is distinguishable from one the user saw. Attachments are
described (file/bytes/mime), never embedded; `Typing` is not logged at all
(no content, fires on a timer); the progress bubble's `EditPlain` IS, and
dominates by line count on purpose — a bubble left behind after an error has no
other evidence. Off by default: it records every room the bot can see. The
startup banner and `SetCommands` go through the concrete API client before the
Bot exists and are NOT in it.
`pollhealth.go` records getUpdates/send failures into the app log
with throttled repeats and a recovery line, so a transport outage is visible
after the fact. An outage closes on either of two signals, because the
library only offers one and it is not enough: an inbound UPDATE (`ok`, from
`onUpdate`) or `pollQuietRecovery` (5 min) with no further error, swept by a
ticker (`watchHealth`). Without the sweep a quiet chat left an outage open in
the log indefinitely and dated its end at the next human message — a
recovered transport and a dead one read identically. The reported duration
ends at the LAST error, never at detection, and an intermittent fault
therefore logs as several short outage/recovery pairs rather than one long
one: noisier, and true. Outbound sends retry transient network failures on a short
backoff (`withSendRetry`, ~4.5s of patience) and never retry API
rejections. At startup the host registers the `/` command menu
(`BotCommands`), clears any menu button an older build left behind, and greets
the chat.

The turn model is **one conversation per chat**: each room holds ONE
long-lived session that every message in that room — bare or reply —
continues. All of a room's turn state lives on `conversation`
(`conversation.go`: session, anchors, queues, burst, turn slot), and `Bot`
keeps the registry plus the process-wide wiring; `b.mu` guards the registry,
`c.mu` the room, and the lock order is c.mu then b.mu, never the reverse. A
Telegram reply is a context hint (the quoted text is injected as a capped
blockquote, `withReplyContext`) and, in a group, a trigger — never a session
switch; `/new` resets only the room it was typed in (old conversation stays in
the dash's runs listing and the history index). Each room's current session id
persists in the runs store's `threads` table under its OWN surface,
`"<host>:<chatid>"` (`TelegramSurface`, `roomSurface` — the host prefix comes
from the front-end's own index, so two front-ends could never cross-resolve),
which is what makes a restart resume every room instead of merging them. The
bare `"telegram"` key an older build wrote is not read: an upgrade costs one
conversation reset, per the no-backwards-compat rule. Sessions never retire;
host-managed compaction keeps each room's context bounded.

Turns run CONCURRENTLY, one slot per room, under a global cap
(`telegram.max_concurrent_turns`, default 4; `claimTurn`/`freeTurn`). Sending
always succeeds — a message arriving while its room is busy, or while the cap
is full, queues (`mailQueue`) and the WHOLE backlog drains as one batch turn.
The cap is why every turn end runs `startNextWorkAll`, a sweep over EVERY
room rather than just the finishing one: a message queued because the cap was
full has no event of its own coming, and freeing a slot is the only signal it
will ever get. `/reload` swaps the Parts all rooms share, so it takes a global
latch (`beginReload`) and is refused while ANY room is mid-turn. All rooms
also share ONE working directory, so two rooms can run bash in the same tree
at once; the mitigation is the `status` tool's `rooms:` section plus a line in
the scaffold prompt, and it is ADVISORY — a check-then-act race remains, and
real isolation would be a per-room `workdir:` or a lock in the gate.

Each room gets its own **prompt brief** (`roombrief.go`), injected into that
room's system prompt through `SessionOpts.PromptSuffix` →
`chat.Config.PromptSuffix` → `renderSystemPrompt`. It is a CLOSURE, not a
string, for the same reason `RefreshPrompt` is: the prompt is re-rendered
every turn, so a group description edited mid-conversation lands on the next
turn instead of the next restart. Three layers by trust: the chat TITLE
(orientation); the group DESCRIPTION, delimited in `<group-description>` and
labelled as member-written context rather than instruction (a group ADMIN can
edit it and need not be allowlisted — accepted risk, the operator's call when
they hand out admin; `use_description: false` per room opts out; capped at
`briefDescriptionCap` 4 KB since it is fixed overhead on every turn there);
and operator-declared `context:` files read through the ordinary
`config.ResolveContextFiles` (64 KB cap, middle elision). NOTE, undocumented in the Bot API and
established live: Telegram serves a group's `description` only to a bot that
can see group info. A default-restricted bot ("has no access to messages")
gets the TITLE and an empty description; promoting the same bot to admin in
the same basic group returns it immediately (observed `description_bytes=0`
then `66` across a promotion, same chat, same code). The Bot API's own field
row says only "Description, for groups, supergroups and channel chats" and
mentions no rights requirement, so treat the log line as the diagnostic:
`refreshChatMeta` records title and description length exactly so a
silently-absent brief is distinguishable from a chat that has none — the two
have different fixes and neither is guessable from the prompt. The `status`
tool says the same thing where the agent will actually see it
(`briefState`): in your prompt (N bytes) / not visible — either none is set
or Telegram is withholding it from a bot that cannot see group info / off for
this room / not looked up yet.

Converting a group to a supergroup — a legitimate thing an operator may do
for unrelated reasons, and IRREVERSIBLE — CHANGES its chat id, which
`Bot.migrateRoom` handles: Telegram's `migrate_to_chat_id` service message
(no sender, so it is checked BEFORE the sender gate) carries the room's
session to the new id, re-persists its marker under the new surface, clears
the old one, and drops the cached metadata. Chat metadata is
cached (`briefRefresh` 15 min) and refreshed WITHOUT blocking the turn:
`brief()` runs inside prompt rendering, so a stale entry is served as-is
while one background `getChat` per room (`metaInflight`) refreshes it, and
only a room whose metadata is unknown ENTIRELY fetches synchronously — there
is nothing to serve then, and a room's first turn should know its own name.
Every lookup is bounded (`chatMetaLookupTimeout` 5s) and a failure keeps the
last known values without re-stamping the cache, so the next call retries
rather than serving a failure for the whole interval. `/reload` refreshes
every room synchronously (`refreshAllChatMeta`): it is off the turn path, and
an operator who just renamed a room expects the next turn to know it.

A TEXT message arriving mid-turn STEERS the running turnA TEXT message arriving mid-turn STEERS the running turn —
injected at the next round boundary via `chat.Session.Interject`
(`dispatchMail`); a steer landing after the final boundary is answered by
`startSteerCatchup`'s own POSTED turn (`chat.Session.HasSteer` /
`shell3.Session.HasQueuedSteer`), so it is never silently absorbed; media
messages queue instead — `Interject` only carries text, so an attachment
arriving mid-turn waits for the next turn rather than injecting a path
mid-flight. Inbound text
rides a 400ms debounce (`burstWindow`, `b.debounce` in tests) merging
Telegram's split-message fragments into one turn. `Bot.Inbox()` renders the
queued state of EVERY room — each room's pending messages plus waiting task
reports — with zero tokens, surfaced as the dash index's Inbox section beside
a Rooms table (`render.RoomsSectionHTML`, one row per live room linked to its
own transcript). During a user turn the bot renders tool activity as ONE
self-editing **progress bubble** (`progress.go`: posted silently on the
first ToolCall, edits throttled at 1.5s, last 6 lines shown, one-line
tool summaries) that is DELETED after a clean turn and kept as a
breadcrumb after an error; wake turns show no bubble. `DeleteMessage`
joins the tgClient surface (console renders `[delete #id]`). `postReply`
chunks the reply at 4000 **UTF-16 code units** (Telegram's real accounting —
emoji count double; `utf16Len`/`chunk` in render.go) on rune boundaries and
replies each chunk to the conversation's anchor, capped at `replyMaxChunks` (2) bubbles — a
longer reply posts its first chunk plus the full text as a `reply.md` document
— and records every sent message id so the anchor advances.
`drainTurn` treats only the FINAL assistant segment as the reply — text before
a tool call is progress narration — and errors always surface. Markdown is
converted for Telegram by `mdhtml`, which renders only the tag set Telegram
accepts and **escapes** everything else to literal text — raw HTML in the
source (`ast.RawHTML`/`ast.HTMLBlock`, i.e. any bare `<tag>` in prose) is
escaped, never dropped: those nodes carry their text in segments rather than
children, so falling through to a children-walking default silently deletes
them and cuts words out of the agent's reply mid-sentence.

**Commands are host-answered** (`commands.go`, no model call, zero tokens):
`/dash` (the dashboard URL with a fresh token; `/dash <text>` instead becomes
a normal agent turn pointed at the dash-exposing skill), `/stop` (cancel the
turn; background jobs keep running), `/superstop` (cancel the turn AND
`KillAllForStop` every job — one ⚠️ summary to the user, the same text
queued into the conversation via `NotifyTextNoWake`, per-job completion
posts suppressed; cron schedule stays armed), `/new` (start a fresh
conversation; refused mid-turn), `/run <job>`, `/btw <question>`, `/reload`,
`/quiet on|off` (persisted to
`~/.shell3/quiet_mode.json` by `QuietStore`: ⏰ cron and 🔔 completion posts
send with Telegram `disable_notification`, arriving without a ping; ✉️
updates are ALWAYS silent regardless of the toggle (an update is not a page);
replies to the user's own messages and ⚠️ failures always ring; the flag
rides a variadic
`SendOpt{Silent}` on the tgClient send methods, rendered by the console
transport as a 🔕 tag).
Beyond the built-ins, the kit's `command:` blocks are answered here too — the
`default:` branch of `handleCommand` consults them, so a built-in always wins.
There are NO view commands: `/status`, `/jobs`, `/job`, `/cancel`, `/runs`
and the `/run_N`/`/job_N`/`/cancel_N` taps all answer "unknown command", and
what they would show is the **web dash** (`internal/dash`): a read-only HTTP server
on `127.0.0.1:<dash_port>` (top-level wiring key, default 7333, 0 = no
listener; started by `wireHost` for telegram, Bot API AND --console, never
`ask`). Seven GET routes behind a `?t=` token gate, each threading the
request's own token into the links it renders — `/` (index:
`render.DashIndexHTML` over live closures + `Bot.Inbox()`, with the live
session linked to its own replay, each bash_bg job's id linked to its output
log, each cron row linked to its detail), `/runs`
(`render.RunsPageHTML`, 20/page), `/runs/<id>` (`render.RunReplayHTML`),
`/files` + `/file` (the read-only config-dir explorer,
`render.FilesListHTML`/`FileViewHTML`, rooted at `Sources.ConfigDir`),
`/joblog` (`render.JobLogHTML`, `?session=&id=`, the tail of a bash_bg job's
tee'd log), and `/cron` (`render.CronDetailHTML`, `?name=`) — everything
escaped, non-GET 405, bad token bare 403. The files explorer's security model
must not be "simplified": path traversal is clamped by a leading-slash
`Clean` AND an
`EvalSymlinks` + root-prefix check (a symlink cannot point out of the config
dir), and credential files (`.env`, `.env.*`) are listed
but reported REDACTED without their contents ever being read from disk; binary
and oversized (>256 KB) files are flagged, not dumped. Tokens:
`dash.TokenStore`, 32-byte hex, 1h TTL, several live at once, memory-only,
constant-time compare. `/dash` composes the reply from
`dash_url.txt` in the config dir (seeded `http://127.0.0.1:<port>` when
absent; the dash-exposing skill overwrites it with a tunnel base URL; junk
content falls back to localhost) + a fresh token (`dashMintURL`,
cmd/shell3/dashwiring.go).
`/reload` takes the turn slot, so it is refused rather than raced.

Three **host tools** ride the session decorator (`Runtime.SetSessionDecorator`,
re-applied by `Runtime.Reload`; `DecorateChatSession` skips headless subagent
children): `send_media_telegram` (push a local file to the chat as
photo/voice/audio/video/document, validating extension and size per kind, and
refusing `.env` and its dotenv siblings), `status`, and `reload` (records a pending reload and returns; the
host applies it at end-of-turn, since a mid-turn reload would tear down the
running turn).

**Completion delivery** is mail (see internal/shell3/completion.go above);
the bot is the `CompletionHost` (`bot.go`): `PostCompletion` posts
`⏰ <job>: …` for a cron origin (`report: raw` cron, ⚠️ floors) and `🔔 …`
otherwise, threaded onto the conversation's anchor; `WakeOwner` queues+wakes
iff the owner IS the current main conversation; `StartFreshTurn` is the
catch-all that queues the mail into the main conversation (creating it on
demand) — cron results, orphans, and jobs outliving a `/new` all land there,
so a completion is never lost. Both take a `shell3.Mail`, not a bare note,
because a `report: always` job binds the turn they start and the enforcement
needs the fallback text: a Required mail arms `conversation.pendingRequired`
BEFORE the queue+wake (the wake can be answered on another goroutine the
instant `NotifyText` returns, and a bind armed after that turn settled would
sit unanswered until the next one). A report turn's reply is the agent speaking
to the user: `runWakeTurn` posts it ✉️-prefixed via `postWakeReply` (ALWAYS
silent, a plain message — never a Telegram reply; strict final-segment — no
narration fallback; an exact repeat of the previous ✉️ is dropped,
`lastAgentMail`), and NO_REPLY/empty posts nothing; there is no mail_user tool
(removed: two exits meant the same answer could send twice). `postWakeReply`
returns whether ANYTHING reached the user, and that one bit is what
`settleRequired` acts on: silence posts each bound job's own result instead,
so a report the spawner marked as awaited can never end as nothing at all.
`finishPostedTurn` settles the same way (a user turn drains notices too), and
`/new` calls `flushRequired` — the session whose turn would have answered is
being detached. The two fields (`pendingRequired`, `turnRequired`) are split
because a notice never drains mid-turn: `takeSlotLocked` moves pending onto
the starting turn, so a report landing DURING a turn belongs to the next one
and cannot have its fallback posted over an answer nobody had a chance to
give. The wake is UPGRADED to
a POSTED turn (`runPostedQueuedTurn`) when the inbox holds user steering
(`HasQueuedSteer`), so a steer racing a turn's end still gets its answer;
text arriving DURING a wake turn queues rather than steering into it
(`turnQuiet`).

**Media**: a turn's attachments are saved to `~/.shell3/media/` as `tg-*`
(`attachments.go`) and their paths always go into the prompt
(`attachmentNote`, which names no tool — the harness cannot know whether one
exists). There is NO built-in perception: no `read_media`, no transcription,
captioning, TTS or image generation, and no `media:` config block. Perception
is a tool the operator or the agent DECLARES — the convert/decide rule in
`skills/using-llms.md` is what permits it: a tool may call a model to convert
between forms, never to decide. The agent sends a file back with
`send_media_telegram`. Nothing in `internal/llm` encodes a non-text content
part any more; the adapter renders text parts only. The media
dir and its startup janitor (`media_keep_days`) now live in
`internal/mediadir`, which owns `Dir()` and `Sweep()` independent of any
Telegram-specific code. Restriction policy is the hook script, not a tools
list.

An in-process cron scheduler (`internal/cron`, jobs are the kit's `cron:`
blocks — `kit.CronJob`, aliased as `shell3.CronJob`, reached through
`Parts.Cron()`; each job dispatches its declared agent — any agent the kit declares,
running in that agent's own `workdir:` when it has one — from a
hidden pinned "cron" parent session that is the dispatch parent + the jobs/runs
source but runs NO turns of its own and is never woken; a run's result is
a task report carrying the job name (`DispatchOpts.CronJob`) and the job's
prompt as context (`DispatchOpts.Note` — the agent knows what the job is FOR):
by default a fresh main-agent turn whose reply posts as an ✉️ update only
when warranted (NO_REPLY posts nothing), with `report: raw` a raw ⏰ post
costing no agent turn and `report: always` a turn bound to answer; a failed
run always surfaces as `⚠️ <job> failed: <error>` and spends no turn). Cron
runs AGENT TURNS ONLY: `agent:` is mandatory and a block naming the removed
`tool:` kind is a LOAD ERROR whose text names the replacement, the same shape
as the `direct:` removal and for the same reason — a stale kit must fail
loudly rather than arm nothing. The kind was deleted because a scheduled
shell call has no model in the loop to judge its result, which is exactly
where judgment leaks out of the turn layer and into a script nobody reviews;
it also bypassed `jobManager` entirely, so tool jobs were the one scheduled
work with NO concurrency cap. A job that mostly runs a tool still declares an
agent, and the agent calls the tool and decides what its result means. With
the kind gone the scheduler needs no `ToolRunner` and no post callback of its
own (`SetPost`/`wireCronPost` went with it — every completion now routes
through the job runtime), and `report:` is universally valid on a `cron:`
block. Targets resolve in `kit.Parse` alongside the `gate:` unknown-agent
check — an undeclared agent, a duplicate job name (`cron_status` keys on the
name), a malformed schedule — so all of them are
LOAD errors rather than a failed dispatch on the first tick. `shell3 health`
inherits every one of them by parsing the kit.

The scaffold ships ONE armed cron job, `harness-audit` (daily, `internal/
scaffold/defaults/base/shell3.sh.tmpl`), dispatching an `auditor` employee
whose whole job is finding work that escaped the turn layer: a model call
made outside a turn that DECIDES rather than CONVERTS, a secret read outside
`.env`, a `tool:` whose description promises a verdict rather than converting
between forms, a script over 200 lines under a tool. Its four checks
live in the CRON HEREDOC rather than in the agent prompt or a skill —
an agent cron job must bind a prompt function anyway, and that heredoc is
literally the text the turn receives. The auditor is an agent with
`use: [bash]` and NOT a `tool:` that greps and returns a verdict, which would
be the exact inversion it exists to catch; it reports and fixes nothing. A
clean run replies `NO_REPLY`, which the completion router drops before the
owner branch, so an ownerless daily audit starts no fresh main-agent turn and
costs one employee turn a day. It ships armed because the failure it catches
(a `urllib` client drafting prose inside a tool, 2026-08-20) was found by a
human reading a transcript four days after the skill warning against it had
already shipped — advice that is not checked is advice that does not hold.

Every session records `sessions.agent` (the agent that ran it) and
`sessions.cron_job` (the cron job that started it, `''` for a front-end or
task-tool session — see `runs.Meta.Agent`/`.CronJob`), which is what makes
"what did this job do" and "what did this job cost" answerable without
guessing from session duration. A session that trips host-managed
auto-compaction mid-run rolls onto a NEW session row (`chat.compactInto`);
that roll carries `Agent`/`ParentID`/`CronJob` forward onto the new row too
— dropping them there would silently reattribute a tool-heavy, many-round
job (exactly the kind likeliest to compact) to no job at all the moment it
compacted. `internal/cron` persists each job's run history restart-durably
in its OWN table, `cron_status(name PRIMARY KEY, json)` (`runs.CronStatusSave`/
`CronStatusLoadAll`, `internal/cron/store.go`) — not a row in `threads`,
because `runs.Sweep` prunes any `threads` row whose `session_id` doesn't
name an existing session (live or ended), and a job name or JSON blob in
that column would be
deleted by the very next startup's janitor pass before cron read it back.
A job's row describes the RUN, not its dispatch. `Dispatch` returns as soon
as the subagent is ACCEPTED, so the fire path writes only `LastRun`,
`Runs++` and `LastSubID` (persisted immediately — a late outcome matches on
that id across a restart), and counts a failure only for a dispatch
REJECTION, the one failure no completion will ever arrive for. The real
outcome comes back from the completion router: `Runtime.SetCronOutcomeHook`
(wired by `wireHost` through the same `currentSched()` closure a reload
swaps) delivers a `shell3.CronOutcome` to `Scheduler.RecordOutcome`, which
writes `LastOK`/`LastErr`/`LastMillis` and `Failures++`. Without it a job
that dispatched cleanly every night and failed its work every night read as
`runs=22 fail=0` with an 8 ms "ok" fronting a 7-minute run, and the honest
count existed only inside the report traces. `reportCronOutcome` runs FIRST
in `dispatchCompletion`, before the suppressed and closing returns, because
bookkeeping is not a chat post: `/superstop` collapsing N floor posts into
one summary, and the NO_REPLY drop that saves an idempotent tick a main-agent
turn, are both decisions about DELIVERY, and neither is a reason for the run
to vanish from its history. Three things it does NOT count: a follow-up turn
of a lingering cron subagent (the same run continuing — the outcome is its
main turn's), a `shutdownCancel`led job (its "context canceled" is
manufactured by the restart, which is also why its outbox row is dropped —
the kept running marker is the honest boot-time report), and a redelivery.
That last one is why `JobStatus.OutcomeRecorded` exists: delivery is
at-least-once, so an outage re-dispatches the same event every
`redeliverEvery` until the post lands and a leftover row replays at the next
boot — counting each pass would inflate exactly the number this exists to
make honest. `RecordOutcome` drops anything already recorded, anything whose
sub id is not the current run's (a straggler a later fire superseded; the
table keeps the latest run only), and any job name the kit no longer
declares. Between a fire and its outcome the verdict fields still hold the
PREVIOUS run's, deliberately: inventing one for work still in flight is the
defect, not the fix. In `wireHost` the hook is installed BEFORE
`RecoverCompletions` — a dead-PID marker recovered at boot is a cron run lost
mid-flight, an outcome its history must count, and an unwired hook drops it
silently.
`sessions.total_prompt_tokens`/`total_completion_tokens` is a cumulative
ledger distinct from `last_prompt_tokens` (a point-in-time context-fullness
gauge, overwritten each turn): it only grows, via `Store.AddUsage` after
every turn, and `Store.CronRollup` sums it grouped by `cron_job` over a
window to answer "what did this job cost this week" — the figure the dash's
Cron table prints per job. That figure is the job's DISPATCHED-RUN spend only:
`cron_job` is set on the dispatched child session but never on the
main-agent session that later reads the task report and answers it (a wake
turn can drain reports from several jobs plus user backlog at once, so
there is no honest per-job split of that turn's cost), and that report turn
is commonly the majority of a job's real cost — `render.cronCostSuffix`
labels the number "run" for this reason, not "total". A job with no rollup row
renders as NOTHING rather than as a zero — missing must never look like free.

Every turn's SYSTEM PROMPT is recorded too (`internal/runs/prompts.go`,
schema v10: `prompts(hash, text)` + `turn_prompts(session_id, seq, hash,
ts)`). Without it a stored conversation held what the model SAID and never
what it was TOLD, which makes the commonest question — "why did it think
that?" — unanswerable once the turn ended; shell3's prompt is assembled from
files that change under a live conversation (memory, `context:` files, the
skills index, a Telegram room's description), so "what was in the prompt at
10:33" is a real question. Storage is content-addressed and CHANGE-only: the
prompt is re-rendered every turn but changes rarely, so an untouched
conversation stores ONE body plus one tiny reference, and an identical prompt
in two sessions is stored once. `chat.recordTurnPrompt` writes it from
`assembleTurnContext`, best-effort (a failed write is logged, never fatal —
a debugging record must not cost the user an answer). It is deliberately NOT
in the FTS index (a 20 KB prompt would swamp every history query with matches
from the machinery), and `deleteSessions` drops a session's references then
collects bodies nothing points at any more. The dash's run replay folds each
version in at the message it took effect from. This is a WRITE-side record
only: it changes not one byte of what is sent, so it cannot affect prompt
caching — and nothing in the prompt varies per turn (`kitPrompt` is authored
body + skills index + `context:` files; the Environment block with the
session id is a standing reminder, not part of it), so the steady state is
byte-identical every turn and the provider's cache holds.

Sessions, messages, reminders, and every surface's current-conversation
marker live in **one SQLite database** (`internal/runs`, modernc.org/sqlite —
pure Go, no cgo): `.shell3_project/shell3.db`, with an FTS5 index over
user+assistant message text backing the `history` tool, a `ts` on every
message row (RFC3339Nano, the same `encTime` shape `sessions` uses, stamped
from the same clock reading as the session's `last_at` so a message can never
look newer than its session) so a question about a WINDOW is answerable
without inferring time from a session's own start and end, and the `outbox`
table (schema v9) holding
the restart-durable completion queue — like `cron_status` it is opaque JSON,
names no session id, and `runs.Sweep` never touches it. Job logs stay plain
files under `runs/<session>/jobs/<id>.log`. The schema is stamped with `PRAGMA
user_version`; a database whose stamp doesn't match the running binary is
**deleted and recreated empty**, with one loud stderr line — shell3 data is
disposable by design, so there are no migrations.

A **runs janitor** runs once at `shell3 telegram` startup
(never on `ask`): `runs_keep_days` (top-level wiring key, default 30,
`0` = keep forever) deletes sessions whose `last_at` is past the cutoff —
rows, FTS entries, thread entries, and job-log dirs together — plus empty
crash leftovers and orphaned `runs/<id>/` dirs (pre-database leftovers),
printing `janitor: removed N runs, M thread entries` (silent when both are
zero). The empty-trash rule spares dispatch parents (a session other rows
name as `parent_id` — the pinned cron parent is always message-less), and
stale `status='live'` rows past the grace hour flip to `ended` (nothing
from a previous process can still be live at startup; recent ones may be a
concurrent `ask`). SQL in `runs.Sweep`, on its own connection (the runtime's store is
already open by then — the sweep does not need it closed), before the bot
starts polling. A sibling **media janitor** (`internal/mediadir`) runs the
same start-time-only shape, gated by `media_keep_days` (top-level wiring
key, default 0 = keep forever, so this is opt-in): deletes
regular files in the media dir past the cutoff — chat uploads and anything
a wrapper script saved there, since none are distinguished from each other
by the sweep. A swept file's stored path in an old transcript no longer
resolves.

The scaffold ships a `self-knowledge` skill (`internal/scaffold/defaults/
base/skills/self-knowledge.md`, pinned by `internal/scaffold`'s test): the
agent runs inside a Go binary whose source is not on the machine, so every
question about its own runtime is either answerable from live state or not
answerable at all, and the failure mode without the skill is a confident
invention the user cannot check. It is introspection-first by design — a
table mapping questions to the command that answers them (`status`, `rg` over
the kit, the `history` tool, the `turn_prompts` table) — and states only the
invariants that cannot be introspected: one conversation per chat, the group
trigger rules, that unaddressed group messages never reach the model at all,
mail/completion routing, compaction, the gate. Observed live before it
existed: asked whether an @mention gives it the room's recent history, the
agent answered "I'd have to check the wiring", and a message sent 60 seconds
earlier in the same room was invisible to it — the right answer, reached by
hedging rather than by knowing.

`shell3 boot` scaffolds the config tree (an interactive form: model, context
budget, an optional proxy command, the Telegram bot token + chat id, and the
agent's workdir) and writes secrets to `~/.shell3/.env` (the token as
`TELEGRAM_TOKEN`, referenced from the rendered yaml as `env:TELEGRAM_TOKEN`;
both Telegram fields may be left blank and filled in later, and a non-numeric
chat id is rejected at the form). There is no vision question and no
`--vision` flag: perception is a tool the operator declares in their own kit
(see the `using-llms` skill), not something boot can wire in — there is no
`media` built-in to opt into. It installs **nothing** and exposes **nothing**:
the finale prints how to run the bot and points at `docs/deploying.md` (or the
agent) for service management — it only ever *prints*; running any of it is
the operator's. `--show` reprints that finale, rendered to the terminal's own
background. `--prompts` refreshes the scaffold's prompt files in an existing
install (scaffold-shipped `skills/`) after an upgrade — the kit itself is
hand-edited, so there is no safe seam to splice a prompt into and it is left
alone: `shell3.sh` (cron jobs included), `.env`, memory, and user-authored
skills are untouched; replaced files back up to `.backup/prompts-<ts>/`; one
set of files ships to every install now; a reload applies it
(`runPromptRefresh` in cmd/shell3/bootprompts.go, rendered by
`scaffold.PromptFiles`).
`shell3 ask` is the terminal front-end, and it has TWO faces. WITH a message
(argv, `-p`, `--agent`) it is the one-shot scriptable renderer
(`internal/cli`): full verbose output — every tool call/result, reasoning,
token usage — on stdout, `-p` for headless. WITHOUT one it opens the
full-screen chat UI (`internal/askui`), the terminal alternative to Telegram:
bubbletea + bubbles + lipgloss, always-live input (no modes), collapsible
tool/thinking blocks, glamour-rendered markdown replies, a light/dark palette
sensed from the terminal (`tea.RequestBackgroundColor`), and a footer carrying
model + `ctx: N%` + `bg: N` + the agent badge. Enter sends; enter DURING a
turn steers it (`Session.Interject`) rather than queueing a second turn;
ctrl+c stops the turn and only then arms the quit. The KEYBOARD's fold key is
all-or-nothing (`ctrl+o`: collapse what is open, else expand everything) and
has no per-block form, because the always-live input rules out plain keys and
a keyboard block cursor costs more than it returns — the MOUSE is how you
point at one block. The mouse is captured (`MouseModeCellMotion`: button,
wheel, and drag motion, not a report per pixel) and `mouse.go` drives every
gesture at once — wheel scrolls, drag selects with edge-scrolling past the
page, release copies, click folds ONE block (a click in the blank area under
a short transcript folds nothing: `eventLine` clamps a y past the last
content line so a DRAG still selects to the end, and reports that it clamped
so a CLICK there is ignored). Capturing it takes the
terminal's own click-drag selection away, which is precisely why the old
TUI's line-selection machinery IS ported back (`reverseContent` over an
ultraviolet cell grid, so the highlight survives the SGR resets glamour bakes
into colored content; a plain background style would be switched off
mid-line). Copy is WYSIWYG through the `excluded` mask that `renderBlocks`
returns: a line that is never highlighted — system reminders, the thinking
indicator, block separators — is never copied, and `selectedText` consults
the same mask. `copyToClipboard` writes BOTH transports (OSC 52 via
`tea.SetClipboard` for SSH, and atotto/clipboard for terminals without it,
e.g. Apple Terminal) because either alone leaves a common setup with nothing
in the clipboard. `ask` installs no
CompletionHost, so a finished background job's notice lands straight in the
session's inbox with a Wake: the plain path drains it in
`cli.FollowAskJobs`, and the UI drains it in `handleWake` — the two are the
same contract, and dropping either loses a subagent's result. The UI does not
own stderr, so it silences applog's WARN/ERROR mirror for as long as it holds
the alternate screen (`applog.MirrorSetter`, restored on a deferred call):
a gate refusal logged mid-frame otherwise paints raw text across the render
and stays until the next full redraw. The chat UI is also NEVER headless
(`askHeadless`) — it opens only once `requireTerminal` proves a terminal on
stdin AND stdout with a human typing, so `shell3 ask 2>log` must not mark a
live session headless the way an stderr-only test did. `ask` is
host-agnostic: it reads nothing from the `telegram:` block.

`ask` keeps its OWN conversation, and this is load-bearing now that both
front-ends run at once: `--resume` follows ask's thread marker (the `ask`
surface in the runs `threads` table, `askSurface` in cmd/shell3/ask.go). The
older `SessionOpts.ResumeLatest` — reattach to the newest session matching
workdir+configDir — was DELETED with this change rather than left unused:
"newest session here" is not a conversation identity, and with two front-ends
live it reattaches to whichever spoke last, which is exactly the bug. Every
front-end now resolves its own id from its own surface and passes `ResumeID`. A missing marker (first
run) or one pointing at a session the janitor swept resolves to a fresh
conversation rather than an error. The marker is written for a FRESH session
too — otherwise the first `--resume` after a fresh run has nothing to follow —
but NOT by `--agent`, which refuses `--resume` because it holds no
conversation and so must not leave a batch script's empty parent session as
the next `--resume` target.
`--agent <name>` is its scripting seam
(`cmd/shell3/askagent.go`): one headless turn of a kit-declared employee
via `Session.Dispatch`, printing ONLY the reply on stdout (diagnostics to
stderr), waiting for Done && !ChildOpen so a lingering bash_bg can't truncate
it, exiting nonzero on a failed or empty run. It exists so batch scripts stop
hand-rolling HTTP clients against the model — a shell-out inherits the real
adapter (reasoning split, thinkleak, truncation detection) and a tool-capable
turn, instead of reimplementing the parsing half. It refuses `--resume` (each
run is a fresh child session) and requires a message (it never opens the
chat UI).
`telegram`, `ask`, `boot`, `tool`, and `health` are the whole
command tree. The one bound listener is the read-only web dash on
`127.0.0.1:<dash_port>` (see the commands section); there is no command that
EXPOSES it beyond loopback (a tunnel is the dash-exposing skill's job) or
that supervises the process. There is NO bring-your-own front-end seam:
shell3 is Telegram-first and nothing else, and a second wire format would be
a second front-end contract to keep in lockstep for no user. The transports
are the Bot API and `--console` (`client_console.go`, the
credential-free way to drive the same bot loop over stdin/stdout by hand).
Message ids stay opaque strings end to end (the Telegram client stringifies
the API's ints) because the console transport numbers its own.

## IMPORTANT: Do Not Read Credential Files

Secrets and credentials (provider API keys, tool tokens) live in a plain
`.env` file beside the active `shell3.sh` (e.g. `~/.shell3/.env`),
referenced from the wiring as `env:KEY`. Never read, display, or include the
contents of any credential file in a response. This applies to all agents,
assistants, and automated tools.

- `.env` beside `shell3.sh` (e.g. `~/.shell3/.env`) — provider API keys,
  base URLs, tool secrets

## Project Layout

```
cmd/shell3/            cobra command tree: root (prints help) + telegram/ask/boot/tool/health subcommands
internal/agentsetup/   shared config assembly (BuildParts → chat.Config) used by every front-end
internal/config/       config-directory loader: lifts the kit's shell3: wiring through the strict YAML parser, reads .env, owns tool schemas, the skill scan, the context: resolver, and gate/note execution
internal/bootstrap/    first-run global + project setup
internal/kit/          the kit parser/executor: scan, decl, funcs, exec, harness, persona, cron (shell3.sh)
internal/scaffold/     embedded starter config tree (defaults/base: the kit template, skills/, lib/) + boot rendering
internal/adapter/openai/  OpenAI-compatible LLM adapter
internal/modelproxy/   run_proxy spawner (starts a model's proxy command on activation)
internal/paths/        global (~/.shell3/) + local (.shell3_project/) path resolution
internal/runs/         SQLite runs store (modernc.org/sqlite, pure Go): sessions/messages/reminders/threads + FTS5 index in .shell3_project/shell3.db; job logs stay files under runs/<id>/jobs/; sweep.go is the startup janitor
internal/edittool/     edit_file tool implementation (Go port of opencode's str-replace) + its direct-disk file I/O
internal/notify/       Notification type (bg_done / agent_done) shared by job runtime + chat
internal/mediadir/     resolves the media dir (<configDir>/media, $SHELL3_MEDIA_DIR overrides) and runs its startup janitor (media_keep_days); free of the unix build tag
internal/mcp/          MCP client (official go-sdk): Manager connects mcp: servers, lists tools, dispatches mcp_* calls
internal/mdpage/       self-contained HTML renderer for long Markdown replies delivered as files
internal/telegram/     the chat front-end: bot loop + transports (Bot API, console), turn slot, thread index, host commands + tools, completion delivery
internal/render/       HTML renderers for the web dash (DashIndexHTML, RunsPageHTML, RunReplayHTML) + shared formatting helpers
internal/dash/         the web dash: token store (1h TTL, constant-time) + read-only HTTP server on 127.0.0.1, wired by cmd/shell3/dashwiring.go
internal/cron/         robfig/cron scheduler dispatching subagent jobs on Session.Dispatch
internal/askui/        the shell3 ask chat UI: bubbletea model/view/keys/mouse, transcript blocks + selection, palette, markdown cache
internal/cli/          terminal front-end helpers: shell3 ask one-shot renderers, brand banner
internal/chat/         conversation loop, tools, events
internal/llm/          Provider/Streamer interfaces, request params, types (+ fakellm)
internal/persona/      runtime carrier for an agent's prompt/tools/params (data only)
internal/strutil/      rune-safe string truncation helpers (byte-cap + rune-count) shared by runtime and front-ends
internal/applog/       rotating app log
internal/review/       guardian LLM used by soft gate-review verdicts
internal/shell3/       session/runtime core consumed by the front-ends; jobs.go hosts the in-process job runtime (subagents + bash_bg); completion.go is the deterministic mail router (CompletionEvent/CompletionHost)
```

## Development

```bash
make build      # go build ./cmd/shell3
make install    # go install ./cmd/shell3
make lint       # gofmt + go vet + golangci-lint
go test ./...   # run all tests
```

`shell3 telegram --console` drives the whole bot loop over stdin/stdout with no
credentials and no network — the way to exercise the front-end by hand.

## AI artifacts are not committed

Design specs, implementation plans, and other AI-generated working notes are
**gitignored, never committed** — `docs/dev/*` (except its `README.md`),
`docs/superpowers/` and `docs/dev/superpowers/`. Keep them
local; the repo carries only shipped documentation (top-level `README.md`,
`docs/`, `docs/cookbook/`). If you generate a design/plan doc, leave it in
`docs/dev/` where the ignore rule keeps it out of commits.
