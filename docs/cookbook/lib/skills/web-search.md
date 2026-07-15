---
name: web-search
description: Use Brave Search and page fetching for current, external, or source-grounded information
---

# Web Search Skill

Use this skill when a task needs current information, external facts, documentation, citations, or verification beyond the local repo/context.

## Tools

Two tools are available:

- `brave_search{ query, count }` — runs a Brave Web Search and returns titles, URLs, and snippets. `count` is 1-20 (default 10). Use it for discovery, candidate URLs, and quick orientation.
- `web_fetch{ url }` — fetches a single page, strips HTML, and returns its plain text plus the links it contains. Use it to read a specific page closely once search snippets are insufficient.

## Keep retrieval small by default

Start with a low `count` and increase only when the task requires it.

- Simple lookup / verify one fact: `count=3`.
- Normal research: `count=5-10`.
- Broad discovery / deep research: raise `count` gradually, and `web_fetch` the most promising results rather than searching repeatedly. Explain why larger retrieval is necessary.

A search snippet is not the full page. Do not imply you read a page you only saw in snippets — `web_fetch` it first when details matter.

## Workflow

1. Decide whether the question needs web search. If local files/docs can answer it, inspect those first.
2. Start with a small `count`.
3. Read enough source content to answer accurately; `web_fetch` specific pages as needed.
4. Prefer official docs, standards, release notes, and primary sources.
5. Cross-check important claims with at least one authoritative source when practical.
6. Summarize with links/source names when the answer depends on web content.
7. Note uncertainty or stale/ambiguous information instead of overclaiming.

## Cost and context hygiene

- Avoid repeatedly searching the same query; refine based on results.
- Extract what you need from large outputs into a short note; do not re-fetch them (old outputs are auto-pruned for you).
- Prefer a targeted `web_fetch` over many broad searches.
