---
name: web-search
description: Use Brave Search and page fetching for current, external, or source-grounded information
---

# Web Search Skill

Use this skill when a task needs current information, external facts,
documentation, citations, or verification beyond the local repo/context.

## Setup (once)

Two wrapper scripts, written per the scripting skill. If they don't exist yet,
create them and `chmod +x` (ask the user to put `BRAVE_API_KEY` in
`~/.shell3/.env` — free tier at brave.com/search/api):

`~/.shell3/lib/bin/brave-search`:

```bash
#!/usr/bin/env bash
# brave-search <query> [count] — titles, URLs, snippets
set -euo pipefail
q="${1:?usage: brave-search <query> [count]}"; n="${2:-5}"
key="$(grep '^BRAVE_API_KEY=' ~/.shell3/.env | cut -d= -f2-)"
curl -fsS --max-time 15 -G "https://api.search.brave.com/res/v1/web/search" \
  -H "X-Subscription-Token: ${key}" \
  --data-urlencode "q=${q}" --data-urlencode "count=${n}" \
| jq -r '.web.results[] | .title + "\n  " + .url + "\n  " + (.description // "") + "\n"'
```

`~/.shell3/lib/bin/web-fetch`:

```bash
#!/usr/bin/env bash
# web-fetch <url> — page text, tags stripped
set -euo pipefail
url="${1:?usage: web-fetch <url>}"
curl -sfL --max-time 15 "$url" \
| python3 -c 'import sys,re,html; t=sys.stdin.read(); t=re.sub(r"(?is)<(script|style)[^>]*>.*?</\1>"," ",t); t=re.sub(r"(?s)<[^>]+>"," ",t); print(re.sub(r"\s+"," ",html.unescape(t)).strip())'
```

## Keep retrieval small by default

- Simple lookup / verify one fact: `brave-search "query" 3`.
- Normal research: count 5-10.
- Broad discovery: raise count gradually, and `web-fetch` the most promising
  results rather than searching repeatedly.

A search snippet is not the full page. Do not imply you read a page you only
saw in snippets — `web-fetch` it first when details matter.

## Workflow

1. Decide whether the question needs web search. If local files/docs can
   answer it, inspect those first.
2. Start with a small count.
3. Read enough source content to answer accurately; `web-fetch` specific
   pages as needed.
4. Prefer official docs, standards, release notes, and primary sources.
5. Cross-check important claims with at least one authoritative source when
   practical.
6. Summarize with links/source names when the answer depends on web content.
7. Note uncertainty or stale/ambiguous information instead of overclaiming.

## Cost and context hygiene

- Avoid repeatedly searching the same query; refine based on results.
- Extract what you need from large outputs into a short note; do not re-fetch
  them (old outputs are auto-pruned for you).
- Prefer a targeted `web-fetch` over many broad searches.
