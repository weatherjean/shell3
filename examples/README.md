# shell3 examples

This directory contains copyable examples for shell3 configuration.

These files are examples, not automatically loaded from this directory. To use one, copy it into your shell3 config directory, usually `~/.shell3/`, or into a project's `.shell3/` directory when you want project-local behavior.

The default bootstrap package that new users receive lives in `internal/scaffold/defaults/`. This `examples/` directory is for readable, copyable reference configs and may include the same files or variants.

## Tools

Example tools live in [`tools/`](tools/).

- [`tools/brave_search.yaml`](tools/brave_search.yaml): Brave Search API tool with two modes:
  - `mode=search` for concise SERP-style results.
  - `mode=context` for Brave LLM Context grounding snippets.
- [`tools/web_fetch.yaml`](tools/web_fetch.yaml): fetch a URL, list links, and extract readable text.

To enable `brave_search`, configure a Brave Search API key:

```bash
shell3 secrets set --key BRAVE_API_KEY --secret <token>
```

Then copy the tool file into `~/.shell3/tools/` or `.shell3/tools/` and set `enabled: true` if needed.

## Skills

Example skills live in [`skills/`](skills/).

- [`skills/web-search.md`](skills/web-search.md): guidance for using Brave Search and `web_fetch` with small default limits.
- [`skills/codebase-discovery.md`](skills/codebase-discovery.md): fast local codebase navigation.
- [`skills/writing-plans.md`](skills/writing-plans.md): planning workflow before non-trivial changes.
- [`skills/executing-plans.md`](skills/executing-plans.md): safe execution, validation, and git workflow.

Copy skills into `~/.shell3/skills/` or `.shell3/skills/`, then reference them from a persona's `skills:` frontmatter.

## Hooks

Example hooks live in [`hooks/`](hooks/).

- [`hooks/confirm-bash.sh`](hooks/confirm-bash.sh): asks for terminal confirmation before running the built-in `bash` tool.

Copy hooks into `~/.shell3/hooks/` or `.shell3/hooks/`, make scripts executable, and configure them in persona/config frontmatter.
