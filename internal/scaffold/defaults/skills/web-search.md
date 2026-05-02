---
name: web-search
description: Use Brave Search and page fetching for current, external, or source-grounded information
---

# Web Search Skill

Use this skill when a task needs current information, external facts, documentation, citations, or verification beyond the local repo/context.

## Tool choices

- Use `brave_search` with `mode=search` for quick discovery, candidate URLs, or broad orientation.
- Use `brave_search` with `mode=context` when the model needs source text, tables, code, or grounded snippets in one call.
- Use `web_fetch` when a specific URL needs closer reading after search, or when search snippets are insufficient.

## Keep limits low by default

Start small and increase only when the task requires it.

Suggested defaults:

- Simple lookup / verify one fact:
  - `mode=context`
  - `count=3`
  - `max_urls=2`
  - `max_tokens=2048`
  - `threshold=strict`
- Normal research:
  - `mode=context`
  - `count=5-10`
  - `max_urls=3-5`
  - `max_tokens=4096-8192`
  - `threshold=balanced` or `strict`
- Broad discovery:
  - `mode=search`
  - `count=5-10`
- Deep research only when explicitly needed:
  - Increase `count`, `max_urls`, and `max_tokens` gradually.
  - Explain why larger retrieval is necessary.

## Freshness

Use `freshness` for time-sensitive topics:

- `pd`: past day
- `pw`: past week
- `pm`: past month
- `py`: past year
- `YYYY-MM-DDtoYYYY-MM-DD`: explicit date range

Prefer a freshness filter for news, changelogs, recent API changes, pricing, availability, and security advisories.

## Source quality

- Prefer official docs, standards, release notes, and primary sources.
- Use `threshold=strict` when precision matters more than recall.
- Use `threshold=balanced` for normal research.
- Use `threshold=lenient` only when strict/balanced misses relevant material.
- If a search result is only a snippet, do not imply you fully read the page. Fetch it first when details matter.

## Workflow

1. Decide whether the question needs web search. If local files/docs can answer it, inspect those first.
2. Start with small limits.
3. Read enough source content to answer accurately; use `web_fetch` for specific pages as needed.
4. Cross-check important claims with at least one authoritative source when practical.
5. Summarize with links/source names when the answer depends on web content.
6. Note uncertainty or stale/ambiguous information instead of overclaiming.

## Cost and context hygiene

- Avoid large `max_tokens` unless needed.
- Avoid repeatedly searching the same query; refine based on results.
- Prune large successful tool outputs after extracting what you need.
- Prefer targeted follow-up searches over one very large search.
