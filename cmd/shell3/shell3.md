# shell3 documentation

shell3 is a minimal, Unix-composable coding agent. It runs LLM-powered sessions in your terminal using any OpenAI-compatible provider.

---

## Commands

### shell3 init
Scaffold `.shell3/config.yaml` in the current directory.

```
shell3 init
shell3 code --init   # also checks tool dependencies
```

### shell3 auth
Store provider credentials in `~/.shell3/credentials.yaml`.

```
shell3 auth
```

Prompts for: provider name, API key, base URL, default model.

### shell3 code
Interactive coding assistant. Has one tool: `bash`.

```
shell3 code
shell3 code --model gpt-4o
shell3 code --model "gpt-4o,gpt-4o-mini"   # multiple models, switch with /model
shell3 code --base-url http://localhost:11434/v1 --api-key "" --model llama3.2
```

**Slash commands inside a session:**

| Command  | Action                              |
|----------|-------------------------------------|
| `/`      | browse and pick a command           |
| `/model` | switch active model (if >1 configured) |
| `/clear` | reset conversation context          |
| `/usage` | show token usage from last turn     |
| `/help`  | list available commands             |

### shell3 run
One-shot agent run (non-interactive). Reads task from stdin or `--task`.

```
shell3 run --task "summarise TODO.md"
echo "fix lint errors" | shell3 run
```

### shell3 docs
Print this documentation.

```
shell3 docs
```

### shell3 destroy
Remove `.shell3/` from the current directory.

```
shell3 destroy
```

---

## Configuration

### Project config — `.shell3/config.yaml`

Created by `shell3 init` in the project directory.

```yaml
model: llama3.2          # preferred starting model for this project (single value)
provider: ollama         # preferred provider (must match a key in credentials.yaml)
default_personality: code
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_session_start: ""
  on_session_end: ""
  on_turn_start: ""
  on_turn_end: ""
  on_tool_call: ""
  on_tool_result: ""
  on_context_build: ""
  on_error: ""
```

### Global credentials — `~/.shell3/credentials.yaml`

```yaml
providers:
  ollama:
    api_key: ""
    base_url: "http://localhost:11434/v1"
    default_model: "llama3.2"
  openai:
    api_key: "sk-..."
    base_url: "https://api.openai.com/v1"
    default_model: "gpt-4o,gpt-4o-mini,o1-preview"  # comma-sep = switchable via /model
```

---

## Multiple models

Available models are defined globally in `~/.shell3/credentials.yaml` as a comma-separated `default_model`. The session starts on the first model (or the project's preferred model if set in `.shell3/config.yaml`). Use `/model` inside a session to switch.

```yaml
# ~/.shell3/credentials.yaml
providers:
  ollama cloud:
    default_model: "kimi-k2.6:cloud,glm-5.1:cloud,llama3.2"
```

```yaml
# .shell3/config.yaml — preferred starting model for this project
model: glm-5.1:cloud
provider: ollama cloud
```

`--model` flag overrides both:
```
./shell3 code --model "gpt-4o,gpt-4o-mini"
```

---

## Providers

Any OpenAI-compatible endpoint works. Common setups:

| Provider  | base_url                          | api_key        |
|-----------|-----------------------------------|----------------|
| OpenAI    | https://api.openai.com/v1         | sk-...         |
| Ollama    | http://localhost:11434/v1         | (empty)        |
| Groq      | https://api.groq.com/openai/v1    | gsk_...        |
| LM Studio | http://localhost:1234/v1          | (empty)        |
