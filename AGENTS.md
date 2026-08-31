# shell3

Minimal Unix-composable personal agent written in Go.

This is the repository's standing instruction file for coding agents. Keep it
short enough to load in full. The exhaustive implementation contract and its
historical rationale live in [docs/internals.md](docs/internals.md); read the
relevant section there before changing a subsystem. User-facing behavior is
documented in [README.md](README.md) and the other files under `docs/`.

## Product principles

Two principles decide what belongs in the binary:

1. **Do less, and do the necessary well.** The harness owns only what the
   harness alone can do: transport, filesystem integration, and the turn.
   Anything an agent can build and inspect itself should remain a shell
   command, script, skill, or declared tool.
2. **Let the agent do anything, but let it do the doing.** Prefer a rule, one
   worked example, and an extension seam over a built-in capability that takes
   decisions away from the agent.

When a built-in and a declared tool can both solve a problem, the built-in has
to justify itself. Keep changes small, composable, and consistent with the
existing Unix-first design.

## Working agreement

- Read the surrounding code and the relevant part of `docs/internals.md`
  before designing a change. Preserve existing boundaries unless the task is
  explicitly to change one.
- Prefer existing packages and patterns. Add an abstraction only when it
  removes real complexity or protects a real invariant.
- Keep code, tests, `docs/internals.md`, and public documentation in lockstep.
  Public behavior belongs in the appropriate user guide, not only in the
  internals reference.
- Use `rg` or `rg --files` for discovery. Keep shell commands direct and
  readable.
- Preserve unrelated work in a dirty tree. Never revert or overwrite changes
  you did not make.
- Use ASCII by default. Add comments only where they explain a non-obvious
  contract or concurrency decision.

## Safety and repository hygiene

Secrets live in the `.env` beside an active `shell3.sh`. **Never read, print,
log, or include a credential file's contents in a response.** This includes
provider keys, tool tokens, `~/.ssh`, `~/.aws`, and similar stores. A script
that needs a secret reads only the required key at point of use.

`AGENTS.md` is the committed cross-agent instruction source. A local
`CLAUDE.md` may symlink to it for compatibility, but `CLAUDE.md` is ignored and
must not be force-added. AI-generated plans, design notes, and other working
artifacts belong under `docs/dev/`; that directory is local-only except for
its tracked `README.md`. Shipped documentation belongs in the top-level
`README.md`, `docs/*.md`, or `docs/cookbook/`.

Do not commit build outputs, runtime state, `.shell3_project/`, secrets, or
local agent artifacts. Do not publish, release, force-push, or alter external
systems unless the user explicitly requests it.

## Architectural invariants

These are the contracts most likely to be damaged by an apparently local
change. `docs/internals.md` contains the full detail.

### Kit and configuration

- The config is a directory centered on exactly one `shell3.sh` kit. There is
  no fallback config format or migration shim.
- A kit contains declaration blocks, function definitions, and literal
  heredocs. It is definitions-only at top level, so sourcing it never performs
  work.
- Parsing is strict. Ambiguous kinds, removed spellings, misplaced fields,
  unknown agents, and unknown built-ins are errors rather than ignored input.
- `tool:` and `test:` scope positionally under `agent:` or `shared:`.
  `gate:`, `note:`, and `event:` name their target agents. `command:` and
  `cron:` belong to no agent; a cron declaration names its own target.
- Context files are refreshed per turn, resolved against the agent's workdir,
  capped at 64 KB, and health-checked. Skills are files, not declaration
  blocks. Cron jobs are blocks, not files.

### Tools and policy

- shell3 is bash-first. The core model-facing verbs are `bash`, `bash_bg`, and
  `edit_file`; file reading, listing, and searching are shell commands.
- A custom tool may use a model to convert between forms, but never to decide,
  score, rank, draft, or summarize. Decisions stay in the agent turn; reusable
  non-tool glue stays in scripts.
- Gates run before every tool for explicitly named agents. There is no
  fallback gate and no `ask` verdict. Invalid output, timeout, or hook failure
  fails closed.
- Gate verdict precedence is block, review, argv, command. Rewrites and review
  apply only to bash tools. Notes may rewrite output but cannot refuse a call.
  Events only observe and must never block a turn.
- The shipped gate is a speed bump, not a security boundary. Hard isolation is
  an operating-system concern. Preserve the distinction between hard `block`
  refusals and `route` refusals that name the sanctioned alternative.

### Jobs, completion, and reload

- Subagents and background commands are in-process jobs. Delegation is two
  levels deep, enforced at dispatch, and concurrency is counted per depth.
  Results must climb back through the parent that requested them.
- Every background completion must remain observable. Completion delivery is
  restart- and outage-durable through the outbox and is intentionally
  at-least-once. Persist an event before routing it and delete it only after a
  successful front-end handoff; do not reverse that ordering.
- `report:` is the sole reporting axis (`auto`, `raw`, or `always`). Keep
  failure handling, owner mail, `NO_REPLY`, fallback posting, and report traces
  consistent with the contract in `docs/internals.md`.
- Graceful shutdown, cancellation, and `/superstop` have distinct suppression
  behavior. Do not turn manufactured shutdown cancellations into misleading
  failure posts.
- Reload never strands running work. Active jobs retain their original Parts
  generation until they drain; idle front-end sessions move to the new one.

### Front ends, media, and storage

- Telegram is the primary front end. It long-polls outbound; the read-only web
  dash binds loopback. The console transport drives the same bot contract.
- Commands are host-answered without a model turn. Event hooks observe the
  session stream asynchronously through a bounded, non-blocking dispatcher.
- Media perception and generation are bring-your-own declared tools. The
  harness saves attachments and sends files; it does not decide which model or
  provider performs perception.
- The runs store, outbox, running markers, and job logs form one durability
  contract. Schema mismatch is handled deliberately; never silently discard
  user history or a completion.
- Message and chat IDs remain opaque strings across transports.

## Project layout

```text
cmd/shell3/              Cobra command tree and front-end wiring
internal/agentsetup/     Shared Parts and session configuration
internal/adapter/openai/ OpenAI-compatible LLM adapter
internal/applog/         Rotating application log
internal/askui/          Interactive terminal UI
internal/bootstrap/      Global and project runtime-directory initialization
internal/chat/           Conversation loop, tools, events, and gate handling
internal/cli/            One-shot CLI rendering helpers
internal/config/         Config-directory loading and hook execution
internal/cron/           Cron scheduler
internal/dash/           Loopback read-only web dashboard
internal/edittool/       Direct-disk edit_file implementation
internal/kit/            Kit parser, executor, harness, and declarations
internal/llm/            Provider and streamer interfaces
internal/mcp/            MCP client and tool dispatch
internal/mdpage/         Self-contained HTML rendering for long Markdown replies
internal/mediadir/       Media path resolution and cleanup
internal/modelproxy/     Model proxy process management
internal/notify/         Shared completion notification types
internal/paths/          Global and local path resolution
internal/persona/        Agent prompt/tool/parameter carrier
internal/render/         Dashboard HTML renderers
internal/review/         Guardian LLM for soft gate-review verdicts
internal/runs/           SQLite history, outbox, markers, and janitors
internal/scaffold/       Embedded starter kit, skills, and scripts
internal/shell3/         Sessions, jobs, completion routing, and reload
internal/strutil/        Shared rune-safe truncation helpers
internal/telegram/       Bot loop, transports, authorization, and host tools
```

## Development and verification

```bash
make build      # go build ./cmd/shell3
make install    # go install ./cmd/shell3
make lint       # gofmt + go vet + golangci-lint
go test ./...   # full test suite
```

Run focused package tests while iterating, then broaden verification in
proportion to the change. Changes to parsing, configuration, gates, completion
routing, concurrency, storage, or front-end delivery require focused regression
tests for both success and failure paths. Keep strict rejection behavior pinned
when a removed or invalid input must not degrade silently.

`shell3 telegram --console` exercises the complete bot loop without credentials
or network access. `shell3 tool check|run|test <kit>` is the kit-authoring loop.
