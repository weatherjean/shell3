// Package mdpage renders Markdown as self-contained HTML.
package mdpage

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

// md is the full-document renderer: GFM, because the whole point of the page
// is the structure the chat cannot show. Unsafe HTML stays DISABLED — the
// markdown here is model output, and a page that renders arbitrary embedded
// HTML is a page that can carry a script or an offsite image beacon.
var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM, // tables, strikethrough, task lists, autolinks
		extension.Footnote,
	),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
)

// Render turns markdown into a complete HTML document titled title.
//
// A render failure is not an error worth failing a reply over: the caller is
// mid-delivery with something the user is waiting for, so the markdown is
// emitted as preformatted text instead. A readable fallback beats no message.
func Render(title, markdown string) []byte {
	var body bytes.Buffer
	if err := md.Convert([]byte(markdown), &body); err != nil {
		body.Reset()
		body.WriteString("<pre>" + html.EscapeString(markdown) + "</pre>")
	}
	var b strings.Builder
	b.Grow(body.Len() + len(pageCSS) + 512)
	fmt.Fprintf(&b, docHead, html.EscapeString(strings.TrimSpace(title)), pageCSS)
	b.WriteString(body.String())
	b.WriteString(docTail)
	return []byte(b.String())
}

const docHead = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>%s</style>
</head>
<body>
<main>
`

const docTail = `</main>
</body>
</html>
`

// pageCSS is deliberately small and dependency-free. The font stack is
// whatever the device already has; the dark variant is driven by
// prefers-color-scheme because the reader arrives from a chat app without
// having chosen anything, and a white page at night is the commonest way a
// document like this goes unread.
const pageCSS = `
:root {
  --bg: #ffffff; --fg: #1b1b1f; --muted: #5c5f6b;
  --rule: #e3e4e8; --code-bg: #f4f5f7; --accent: #2f6feb;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #16171a; --fg: #e6e7ea; --muted: #a0a3ad;
    --rule: #2c2e34; --code-bg: #1e2025; --accent: #7aa2f7;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0; padding: 1.5rem 1.1rem 4rem;
  background: var(--bg); color: var(--fg);
  font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
        "Helvetica Neue", Arial, sans-serif;
  -webkit-text-size-adjust: 100%;
}
main { max-width: 46rem; margin: 0 auto; }
h1, h2, h3, h4 { line-height: 1.25; margin: 2rem 0 .6rem; font-weight: 650; }
h1 { font-size: 1.6rem; margin-top: 0; }
h2 { font-size: 1.3rem; padding-bottom: .3rem; border-bottom: 1px solid var(--rule); }
h3 { font-size: 1.1rem; }
p, ul, ol, blockquote, table, pre { margin: 0 0 1rem; }
a { color: var(--accent); }
ul, ol { padding-left: 1.4rem; }
li { margin: .25rem 0; }
blockquote {
  margin-left: 0; padding: .1rem 0 .1rem 1rem;
  border-left: 3px solid var(--rule); color: var(--muted);
}
code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: .89em; background: var(--code-bg);
  padding: .12em .35em; border-radius: 4px;
}
pre {
  background: var(--code-bg); padding: .85rem 1rem;
  border-radius: 8px; overflow-x: auto;
}
pre code { background: none; padding: 0; font-size: .85em; }
/* Tables are the reason this page exists: the chat cannot render one at all. */
.table-scroll, table { display: block; overflow-x: auto; }
table { border-collapse: collapse; width: 100%; font-size: .94em; }
th, td { padding: .45rem .7rem; border: 1px solid var(--rule); text-align: left; }
th { background: var(--code-bg); font-weight: 620; }
hr { border: 0; border-top: 1px solid var(--rule); margin: 2rem 0; }
img { max-width: 100%; height: auto; }
input[type=checkbox] { margin-right: .4rem; }
`
