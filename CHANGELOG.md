# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Until v1.0.0, minor versions may contain breaking changes.

## [Unreleased]

### Added

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
