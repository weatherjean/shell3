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

Works with any **OpenAI-compatible API endpoint** (OpenAI, Ollama, Groq, LM Studio, OpenRouter, …) and **Anthropic** natively. Codex (ChatGPT subscription via OAuth) is supported via the third-party [openai-oauth](https://github.com/EvanZhouDev/openai-oauth) proxy — see `shell3 docs`.

```sh
make build
shell3 auth        # configure your provider
shell3             # start a session
```

## Docs

Full documentation is embedded in the binary:

```sh
shell3 docs
```

Or read the source: [cmd/shell3/shell3.md](cmd/shell3/shell3.md)

Credentials live in plain YAML at `~/.shell3/ai-do-not-read.auth.yaml`. Edit with `shell3 auth` (opens in `$EDITOR`). The filename prefix is a soft signal to AI agents — there's no encryption, so treat the file like any `~/.*rc` with credentials.

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
