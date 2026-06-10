# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Until v1.0.0, minor versions may contain breaking changes.

## [Unreleased]

### Added

- Inbound media: `Session.SendParts` starts a turn with image/audio
  attachments, and `Interject` accepts the same parts for mid-turn delivery.
  `Part{Kind, Path, Data, MIME}` loads from disk or straight from in-memory
  bytes (Telegram photos and voice notes never touch disk), riding the same
  multimodal plumbing and size caps as `read_media` (10 MB images, 25 MB
  audio). Invalid SendParts attachments reject the turn with a single Error
  event; invalid Interject attachments are dropped with a bracketed note.
- Tool approval: Lua guards can return `{ action = "ask" }` to suspend a tool
  call for human approval. Front-ends answer via `Spec.Approve` /
  `SessionOpts.Approve` (Telegram buttons, webui dialogs); the TUI shows an
  inline `[approve? y/N]` prompt. No approver registered → fail closed.
  Requests and verdicts are recorded in the audit JSONL.
- Mid-turn steering: `Session.Interject` queues messages from any goroutine —
  injected into a running turn at the next round boundary as a
  `user interjected` reminder, or at the start of the next turn when idle.
  In the TUI you can now type while the agent works and press Enter to steer.
- `pkg/shell3.Runtime`: one shared build (config, store, MCP, log) hosting
  multiple named sessions with per-session agent, workdir, headless flag,
  and audit log. `Start`/`Run` are now thin single-session wrappers.
- `shell3 boot` interactive onboarding: writes a split-file base config
  (`shell3.lua` + `lib/` modules) and merges secrets into `~/.shell3/.env`.
- Embeddable library API at `pkg/shell3`: one-shot `Run`, persistent
  multi-turn `Session` (Send/Clear/Rollback/SwitchAgent/Prune/Snapshot/
  History), streaming typed events.
- Multi-agent configs: declare several agents in `shell3.lua`, switch with
  Tab or `/agent` keeping conversation history.
- Lua-defined custom tools, skills, and `on_tool_call` guard chains.
- MCP server support (stdio transport) with per-agent tool selection.
- Headless mode with `--out` JSONL audit logs for pipelines.
- `run_proxy`: auto-start a local proxy/shim command on first model use.
- Runtime enforcement of the session single-turn contract (`ErrBusy`).
- CI (gofmt/vet/race tests on Linux+macOS) and goreleaser release builds.

[Unreleased]: https://github.com/weatherjean/shell3/commits/main
