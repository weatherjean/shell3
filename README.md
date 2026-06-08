# ๑ï shell3 /'ʃɛli/

AI-powered shell assistant.

## Getting started

Works with any **OpenAI-compatible API endpoint** (OpenAI, Ollama, Groq, LM Studio, OpenRouter, …). Codex (ChatGPT subscription via OAuth) is supported via the third-party [openai-oauth](https://github.com/EvanZhouDev/openai-oauth) proxy, which exposes an OpenAI-compatible endpoint.

shell3 is configured by a single Lua file, `shell3.lua`. It's discovered in this order: the `--config/-c` flag, then `./shell3.lua`, then `~/.shell3/shell3.lua`. Secrets (provider API keys, tool tokens) live in a `.env` file beside the config and are read from Lua via `shell3.env.secret("KEY")`.

```sh
make build

# On first run, shell3 auto-creates ~/.shell3/shell3.lua and ~/.shell3/.env.example.
# (Or copy the canonical example yourself:)
#   cp internal/scaffold/defaults/shell3.lua ~/.shell3/shell3.lua

# Put your provider key in the .env beside the config, e.g.:
#   cp ~/.shell3/.env.example ~/.shell3/.env  &&  $EDITOR ~/.shell3/.env

shell3             # start a session
```

## Docs

The canonical, fully-commented example config is [internal/scaffold/defaults/shell3.lua](internal/scaffold/defaults/shell3.lua).

Secrets live in a plain `.env` file beside your `shell3.lua` (e.g. `~/.shell3/.env`), referenced from the config via `shell3.env.secret("KEY")`. There's no encryption — treat the file like any `~/.*rc` with credentials. Keep it out of version control.

## Removing a project's shell3 data

```bash
# Find the project UUID FIRST — it lives in .shell3/.ref.
cat .shell3/.ref   # prints the UUID

# Remove project state from global, then the project-local config.
rm -rf ~/.shell3/projects/<uuid>
rm -rf .shell3
```

## Headless / scripting

shell3 runs non-interactively for pipelines and automation: pass a message as an
argument (or on stdin) and stream a structured JSONL audit log with `--out`:

```sh
shell3 "summarize the diff" --out run.jsonl
```

The `--out` JSONL stream carries every turn event (assistant tokens, tool calls
and results, usage, and the terminal status) for downstream tooling.

## License

[MIT](LICENSE) © 2026 WeatherJean.

Portions of `internal/edittool` are a Go port of the str-replace edit tool from
[opencode](https://github.com/sst/opencode), used under its license; see the
package doc comment in [internal/edittool/replace.go](internal/edittool/replace.go)
for details.
