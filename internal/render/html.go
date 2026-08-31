package render

import (
	"fmt"
	"html"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// A run replay can be genuinely large — a real session
// runs 44 to 455 messages and renders to 100–500 KB of markdown, where every
// tool result sits open at full length. Reading it means scrolling past
// hundreds of results to find one call.
//
// This renders the same data as a self-contained page: one <details> per
// message, reasoning and tool output folded shut, expand what you want. The
// folding is plain HTML — no framework, no script for the common case — so a
// 500 KB transcript opens instantly.
//
// Everything that came from a model or a tool is escaped. Tool output is
// arbitrary bytes: a result containing "</details>" or "<script>" must land as
// text, not as markup that eats the rest of the page.

// RunReplayHTML renders one run as a self-contained HTML page.
func RunReplayHTML(root, id string) (string, error) {
	// filepath.Base leaves "." and ".." unchanged, so the equality check alone
	// would admit both.
	if id == "" || id == "." || id == ".." || id != filepath.Base(id) {
		return "", fmt.Errorf("render: invalid run id %q", id)
	}
	st, err := runs.Open(root)
	if err != nil {
		return "", err
	}
	defer st.Close()
	if !st.HasMessages(id) {
		return "", fmt.Errorf("render: no such run %q", id)
	}
	msgs, err := st.LoadMessages(id)
	if err != nil {
		return "", err
	}
	return renderRunHTML(id, msgs, st.PromptsForSession(id)), nil
}

// esc escapes text for HTML and drops invalid UTF-8. A tool that emitted raw
// bytes (a binary file, a truncated multibyte sequence) would otherwise
// produce a page the browser rejects mid-document.
func esc(s string) string {
	return html.EscapeString(strings.ToValidUTF8(s, "�"))
}

func renderRunHTML(id string, msgs []llm.Message, prompts []runs.PromptRecord) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>run %s</title>\n<style>%s</style>\n</head><body>\n", esc(id), runCSS)

	tools, calls := countTools(msgs)
	b.WriteString("<header>\n")
	fmt.Fprintf(&b, "<h1>run <code>%s</code></h1>\n", esc(id))
	fmt.Fprintf(&b, "<p class=\"meta\">%d messages · %d tool calls · %d results</p>\n", len(msgs), calls, tools)
	b.WriteString("<p class=\"meta\"><button onclick=\"a(1)\">expand all</button> <button onclick=\"a(0)\">collapse all</button></p>\n")
	b.WriteString("</header>\n<main>\n")

	if len(msgs) == 0 {
		b.WriteString("<p class=\"meta\">This run has no messages.</p>\n")
	}
	// The system prompt is folded in at the message position it took effect,
	// so a replay shows what the model was TOLD alongside what it said. Only
	// CHANGES are stored, so a conversation nobody edited shows exactly one
	// block, at the top.
	next := 0
	for i, m := range msgs {
		for next < len(prompts) && prompts[next].Seq <= i {
			writePrompt(&b, prompts[next])
			next++
		}
		writeMessage(&b, i+1, m)
	}
	for ; next < len(prompts); next++ {
		writePrompt(&b, prompts[next])
	}

	b.WriteString("</main>\n<script>\nfunction a(open){for(const d of document.querySelectorAll('details'))d.open=!!open}\n</script>\n</body></html>\n")
	return b.String()
}

func countTools(msgs []llm.Message) (results, calls int) {
	for _, m := range msgs {
		if m.Role == llm.RoleTool {
			results++
		}
		calls += len(m.ToolCalls)
	}
	return results, calls
}

// writeMessage renders one message. A tool result is folded shut by default
// (it is the bulk of a transcript and rarely what you are looking for); the
// text a person or the model actually wrote stays open.
func writeMessage(b *strings.Builder, n int, m llm.Message) {
	role := string(m.Role)
	if m.Role == llm.RoleTool {
		name := m.Name
		if name == "" {
			name = "tool"
		}
		fmt.Fprintf(b, "<details class=\"m tool\"><summary><span class=\"n\">%d</span><span class=\"r\">result</span> <code>%s</code><span class=\"peek\">%s</span></summary>\n",
			n, esc(name), esc(oneLine(stripToolIDPrefix(m.Content), 90)))
		fmt.Fprintf(b, "<pre>%s</pre>\n</details>\n", esc(stripToolIDPrefix(m.Content)))
		return
	}

	text := strings.TrimSpace(m.Content)
	fmt.Fprintf(b, "<details class=\"m %s\" open><summary><span class=\"n\">%d</span><span class=\"r\">%s</span><span class=\"peek\">%s</span></summary>\n",
		esc(role), n, esc(role), esc(oneLine(text, 90)))

	// Reasoning is folded separately: it is often longer than the answer, and
	// it is the first thing you want out of the way when skimming.
	if r := strings.TrimSpace(m.ReasoningContent); r != "" {
		fmt.Fprintf(b, "<details class=\"reason\"><summary>reasoning · %d chars</summary><pre>%s</pre></details>\n", len(r), esc(r))
	}
	if text != "" {
		fmt.Fprintf(b, "<div class=\"body\">%s</div>\n", esc(text))
	}
	for _, call := range m.ToolCalls {
		fmt.Fprintf(b, "<details class=\"call\"><summary>tool <code>%s</code></summary><pre>%s</pre></details>\n",
			esc(call.Name), esc(call.RawArgs))
	}
	b.WriteString("</details>\n")
}

const runCSS = `
:root{--bg:#fff;--fg:#1a1a1a;--dim:#666;--line:#e3e3e3;--pre:#f6f6f6;
--user:#2563eb;--assistant:#7c3aed;--tool:#0f766e;--system:#a16207}
@media (prefers-color-scheme:dark){:root{--bg:#141416;--fg:#e6e6e6;--dim:#9a9a9a;
--line:#2c2c30;--pre:#1c1c20;--user:#7ba7ff;--assistant:#c4a2ff;--tool:#5eead4;--system:#e0b341}}
*{box-sizing:border-box}
body{margin:0;padding:1rem;background:var(--bg);color:var(--fg);
font:15px/1.55 ui-sans-serif,-apple-system,Segoe UI,Roboto,sans-serif}
header{border-bottom:1px solid var(--line);margin-bottom:1rem}
h1{font-size:1.1rem;margin:0 0 .3rem}
.meta{color:var(--dim);font-size:.85rem;margin:.3rem 0}
button{font:inherit;font-size:.85rem;padding:.2rem .6rem;border:1px solid var(--line);
border-radius:5px;background:var(--pre);color:var(--fg);cursor:pointer}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.9em}
.m{border-left:3px solid var(--line);padding:.1rem 0 .1rem .6rem;margin:.5rem 0}
.m.user{border-color:var(--user)}.m.assistant{border-color:var(--assistant)}
.m.tool{border-color:var(--tool)}.m.system{border-color:var(--system)}
summary{cursor:pointer;list-style:none;padding:.25rem 0}
summary::-webkit-details-marker{display:none}
summary::before{content:"\25b8";color:var(--dim);display:inline-block;width:1em}
details[open]>summary::before{content:"\25be"}
.n{color:var(--dim);font-size:.8rem;margin-right:.5rem}
.r{font-weight:600;font-size:.85rem;text-transform:uppercase;letter-spacing:.03em}
.m.user>summary .r{color:var(--user)}.m.assistant>summary .r{color:var(--assistant)}
.m.tool>summary .r{color:var(--tool)}
.peek{color:var(--dim);margin-left:.6rem;font-size:.85rem}
.body{white-space:pre-wrap;overflow-wrap:anywhere;margin:.3rem 0 .6rem}
pre{background:var(--pre);border-radius:6px;padding:.6rem;overflow-x:auto;
white-space:pre-wrap;overflow-wrap:anywhere;font-size:.85rem;margin:.3rem 0}
.reason,.call{margin:.3rem 0}
.reason>summary,.call>summary{color:var(--dim);font-size:.85rem}
`

// writePrompt renders one system-prompt version as a collapsed block. It is
// collapsed by default because it is long and usually unchanged; it is present
// because "what was in the prompt when it did that" is otherwise unanswerable
// once the turn has ended.
func writePrompt(b *strings.Builder, p runs.PromptRecord) {
	b.WriteString("<details class=\"m system\">\n<summary>system prompt")
	if !p.TS.IsZero() {
		fmt.Fprintf(b, " <span class=\"meta\">from message %d · %s · %s</span>",
			p.Seq+1, p.TS.Format("15:04:05"), esc(p.Hash[:8]))
	}
	fmt.Fprintf(b, "</summary>\n<pre>%s</pre>\n</details>\n", esc(p.Text))
}
