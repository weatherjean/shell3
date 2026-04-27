---
name: web-fetch
description: Fetch web pages and extract readable content and links for follow-up navigation
---

# Web Fetching Skill

When you need to fetch, read, or navigate web content, follow these instructions.

## Step 1: Fetch the page

**Priority:**
1. **Use a dedicated web-fetch tool/function if one is available.** This is the preferred method.
2. **Fall back to `bash` with `curl -sL`** only if no dedicated tool exists.

When using `curl`, always follow redirects (`-L`):

```bash
curl -sL 'https://example.com/docs/page'
```

- Use `-sL` (silent + follow redirects).
- Quote the URL (single or double quotes) to avoid shell expansion.
- For pages that might need a User-Agent, add: `-H 'User-Agent: Mozilla/5.0'`

## Step 2: Extract readable content

Pipe the raw HTML through `python3` to strip tags and get clean text:

```bash
curl -sL 'https://example.com/docs/page' | python3 -c "
import sys, re
html = sys.stdin.read()
# Remove scripts, styles, nav
html = re.sub(r'<script[^>]*>.*?</script>', '', html, flags=re.DOTALL)
html = re.sub(r'<style[^>]*>.*?</style>', '', html, flags=re.DOTALL)
# Extract links (href values) before stripping tags
links = re.findall(r'href=[\"\\']([^\"\\'\\s]+)[\"\\']', html)
# Remove tags
text = re.sub(r'<[^>]+>', ' ', html)
# Clean whitespace
text = re.sub(r'\s+', ' ', text).strip()
# Print links section
if links:
    print('--- LINKS ---')
    for link in links:
        if link.startswith('http') or link.startswith('/'):
            print(link)
    print('--- END LINKS ---')
    print()
print(text[:12000])
"
```

If `html2text` is available on the system, you may use it as an alternative:

```bash
curl -sL 'https://example.com/docs/page' | html2text -style pretty
```

## Step 3: Follow links when needed

If the user's request requires navigating deeper (e.g., "read the API docs page linked from here"), extract relevant links from the output and fetch those too. Prioritize:

1. **Directly relevant links** — URLs that match the user's intent (same domain, matching path segments).
2. **Same-site links** — prefer relative or same-origin URLs over external ones.
3. **Depth limit** — do not follow more than 2 levels deep unless the user explicitly asks.

To resolve relative links, prepend the base URL:
- `href="/docs/api"` on `https://example.com/guide` → `https://example.com/docs/api`

## Step 4: Prune large results

If a fetched page produces a very large tool result (>5000 chars) and you've already extracted what you need, use `prune_tool_result` to free context before making additional calls.

## Checklist

- [ ] Use a dedicated web-fetch tool if available; otherwise use `curl -sL` with quoted URLs
- [ ] Strip HTML to get readable text (python3 one-liner or html2text)
- [ ] Extract and list links from the page for potential follow-up
- [ ] Follow relevant links if the task requires deeper navigation (max 2 levels)
- [ ] Prune large results once you've extracted what you need
- [ ] Never guess at web content — always fetch it
