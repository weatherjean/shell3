# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-06-03

Initial release.

### Added

- Minimal Unix-composable coding agent in Go, configured by a single Lua file
  (`shell3.lua`) discovered via `--config/-c`, `./shell3.lua`, then
  `~/.shell3/shell3.lua`.
- OpenAI-compatible LLM adapter — works with OpenAI, Ollama, Groq, LM Studio,
  OpenRouter, and Codex (via the openai-oauth proxy).
- Lua config API: `shell3.model()`, `shell3.agent()`, `shell3.tool()`,
  `shell3.skill()`, tool gates, guards, and `shell3.env.secret()` for `.env`-backed
  secrets.
- Interactive TUI and headless/once mode with a structured JSONL audit log
  (`--out <path>`); see `docs/headless.md`.
- Subcommands: `doctor`, `docs`, and `widget` (`ask`/`pick`/`confirm`).
- First-run scaffolding of `~/.shell3/shell3.lua` and `~/.shell3/.env.example`.
- SQLite-backed memory/history, skill loading and indexing, background job
  tracking, and a `str-replace` edit tool ported from opencode (see `NOTICE.md`).
- `--version` flag, stamped from the latest git tag at build time.

[Unreleased]: https://github.com/weatherjean/shell3/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/weatherjean/shell3/releases/tag/v0.1.0
