# shell3 /'ʃɛli/

AI-powered shell assistant.

```
       /\
      {.-}
     ;_.-'\
    {    _.}_
     \.-' /  `
      \  |    /
       \ |  ,/
        \|_/
```

## Getting started

Works with any **OpenAI-compatible API endpoint** (OpenAI, Ollama, Groq, LM Studio, OpenRouter, …). Codex (ChatGPT subscription via OAuth) is supported via the third-party [openai-oauth](https://github.com/EvanZhouDev/openai-oauth) proxy, which exposes an OpenAI-compatible endpoint — see `shell3 docs`.

shell3 is configured by a single Lua file, `shell3.lua`. It's discovered in this order: the `--config/-c` flag, then `./shell3.lua`, then `~/.shell3/shell3.lua`. Secrets (provider API keys, tool tokens) live in a `.env` file beside the config and are read from Lua via `shell3.env.secret("KEY")`.

```sh
make build

# On first run shell3 scaffolds ~/.shell3/shell3.lua and ~/.shell3/.env.example.
# (Or copy the canonical example yourself:)
#   cp internal/scaffold/defaults/shell3.lua ~/.shell3/shell3.lua

# Put your provider key in the .env beside the config, e.g.:
#   cp ~/.shell3/.env.example ~/.shell3/.env  &&  $EDITOR ~/.shell3/.env

shell3             # start a session
```

## Docs

Full documentation is embedded in the binary:

```sh
shell3 docs
```

Or read the source: [cmd/shell3/shell3.md](cmd/shell3/shell3.md). The canonical example config is [internal/scaffold/defaults/shell3.lua](internal/scaffold/defaults/shell3.lua).

Secrets live in a plain `.env` file beside your `shell3.lua` (e.g. `~/.shell3/.env`), referenced from the config via `shell3.env.secret("KEY")`. There's no encryption — treat the file like any `~/.*rc` with credentials. Keep it out of version control.

## Removing a project's shell3 data

```bash
# Remove project-local config
rm -rf .shell3

# Remove project state from global (find UUID first)
cat .shell3/.ref   # prints the UUID
rm -rf ~/.shell3/projects/<uuid>
```

## Credits

Shell ASCII art by jgs (Joan G. Stark).
