# shell3

Minimal Unix-composable harness harness written in Go.

This replacement contract supersedes every legacy section below. Read the
relevant part of `docs/internals.md` before changing a subsystem. Keep code,
tests, and public documentation aligned.

## Replacement contract

- Configuration is one strict inert `shell3.lisp`; there is no shell-kit or
  compatibility parser.
- The attached main agent is an orchestrator. Its only core tools are `bash`,
  `bash_bg`, and `edit_file`.
- Multi-agent work belongs in checked `*.wrk.lisp` workflows that dispatch
  typed external runners. Workers are leaves and may not launch workflows.
- Bare `shell3` is the primary local line-oriented interface. Telegram is an
  optional remote-control adapter to the same runtime and adds only one
  file-send tool named `telegram`.
- One persisted shell3 host owns Lisp-declared schedules: either `shell3
  telegram` or headless `shell3 service`, never both for one project. Each fire
  is a durable wrkfile run; the host service manager keeps the chosen process
  alive across device restarts.

Use `rg` or `rg --files` for discovery, preserve unrelated dirty-tree work,
and use `apply_patch` for edits. Prefer a rule, worked example, and extension
seam over a new built-in. Never publish, release, force-push, or alter external
systems unless the user explicitly requests it.

Start implementation work on `feature/<slug>` or `fix/<slug>` directly from
`main`. After verification, squash the topic into one meaningful commit on
`main`, then delete the finished local branch and its remote branch if one
exists. Do not keep backup or release branches; use `main` history for reverts.
Commits must use the repository's configured human identity without harness,
model, generated-by, or co-author attribution.

Secrets enter only through the inherited process environment. Never read,
print, log, or commit credential values. External runners must not inherit
model credentials.
Do not commit build output, `.shell3_project/`, workflow state, transcripts,
job logs, or local agent artifacts.

Core invariants:

- Lisp parsing fails on unknown, duplicate, misplaced, contradictory, or
  unresolved forms.
- Prompt, memory, and skills live inside `shell3.lisp`; skill metadata enters
  the prompt and `shell3 config skill` retrieves a relevant body lazily.
- `boot` writes one complete kit and never overwrites. Reload validates a full
  generation before idle and future sessions adopt it; busy turns finish on
  their captured generation.
- One session has at most one active turn. Tool calls and results persist in
  provider-valid order, including cancellation and error paths.
- Foreground and background cancellation terminate process groups and bound
  inherited-pipe shutdown. shell3 is not an OS sandbox.
- Every background command completion is one passive `main` filesystem-inbox
  notice. Running markers are deleted only after that notice is durable;
  restart recovery is intentionally at-least-once. There is no model wake or
  direct completion-post path.
- `/stop` cancels a turn; `/superstop` additionally kills managed background
  commands and suppresses their manufactured inbox notices.
- Workflow inbox claims use atomic rename and acknowledge only after successful
  ledger recording. Main notices stay passive until the user asks the agent to
  use the inbox skill, fully read them, and archive them. Datagram wakeups are
  advisory; filesystem state is authoritative.
- Wrk runs snapshot and hash config and workflow data. A beat advances one
  dependency wave; writers run alone, readers may share `parallel`, and
  cancellation reaches the active process group.
- Schedule forms name only wrkfiles, required artifact-relative outputs,
  timeouts, notification routes, overlap policy, cron expressions, and IANA
  timezones. SQLite records running/done/failed invocations and output paths;
  lifecycle events also enter the rotating application log. Schedule changes
  require restarting their persistent owner.
- Message and chat IDs remain opaque strings across transports and storage.
- The SQLite store has one current base schema. A version mismatch discards
  the database and sidecars and creates it fresh; there are no migrations or
  compatibility backups. Filesystem inbox and wrk state remain separate.

Verification:

```sh
make build
go test ./...
make lint
```

Changes to parsing, workflow state, inbox claims, process cancellation,
completion persistence, storage, or transport delivery require focused success and
failure tests. `shell3 telegram --console` exercises the bot contract locally;
use `shell3 config check` and `shell3 wrk check|compile` for authoring.
