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

Works with any **OpenAI-compatible API endpoint** (OpenAI, Ollama, Groq, LM Studio, OpenRouter, …) and **Codex** (ChatGPT subscription via OAuth).

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

Credentials are stored obfuscated (not encrypted) at `~/.shell3/credentials.shell3` — see `shell3 docs` for details and the threat model.

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
