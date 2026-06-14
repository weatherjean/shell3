# patchmd

Small ANSI markdown renderer optimized for streaming LLM output. Pure function: pass full accumulated text, get rendered lines back.

## What it does

Supports the markdown subset LLMs actually produce:

- Headers (`#`, `##`, `###`).
- `**bold**`, `*italic*`, `***bold italic***`, `~~strike~~`.
- `` `inline code` `` (tokenized first so other inline regexes can't eat ANSI escapes — see [the leak regression test](patchmd_test.go)).
- `[links](url)` (rendered as styled label, URL dropped).
- Lists (`-`, `*`, `1.`).
- `> blockquotes`.

## What it does NOT do

- No fenced code block handling. Callers stream-rendering line-by-line should track fence state themselves and emit code-block lines verbatim — per-line rendering can't see block context anyway.
- No tables, HTML, footnotes, definition lists.
- No line wrapping — caller's responsibility (use `patchtui.VisibleLen` to measure).
- No streaming state. Re-render the full accumulated text on each chunk; the function is pure and idempotent.

## Usage

```go
lines := patchmd.Render(markdown)
for _, l := range lines {
    fmt.Println(l)
}
```

For streaming chat output, see the render sink in `internal/tui` — it commits per-line patchmd output to scrollback as chunks arrive.
