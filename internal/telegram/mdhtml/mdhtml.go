// Package mdhtml converts agent Markdown into the small HTML subset that the
// Telegram Bot API accepts with parse_mode=HTML.
//
// Telegram's Markdown modes are unforgiving: a single unbalanced marker makes
// the API reject the whole message with a 400. HTML is far safer because we
// emit the tags ourselves and only ever have to escape & < > in text. We parse
// the source with goldmark (so unbalanced/garbage Markdown degrades to literal
// text instead of broken output) and render only the tags Telegram supports:
// <b> <i> <s> <u> <code> <pre> <a> <blockquote>. Unsupported constructs
// (headings, lists, rules) are flattened to plain text equivalents.
package mdhtml

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var md = goldmark.New(goldmark.WithExtensions(extension.Strikethrough))

// ToTelegramHTML converts Markdown to Telegram-safe HTML. The output is always
// well-formed: every tag it emits is balanced, so Telegram will not reject it.
func ToTelegramHTML(src string) string {
	source := []byte(src)
	doc := md.Parser().Parse(text.NewReader(source))
	r := &renderer{source: source}
	r.renderChildren(doc)
	return strings.TrimRight(r.sb.String(), "\n")
}

type renderer struct {
	sb     strings.Builder
	source []byte
}

// blockSep writes a blank-line separator before a block if output already has
// content, mirroring Markdown's paragraph spacing.
func (r *renderer) blockSep() {
	if r.sb.Len() > 0 {
		r.sb.WriteString("\n\n")
	}
}

func (r *renderer) renderChildren(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		r.renderNode(c)
	}
}

func (r *renderer) renderNode(n ast.Node) {
	switch n := n.(type) {
	case *ast.Heading:
		r.blockSep()
		r.sb.WriteString("<b>")
		r.renderChildren(n)
		r.sb.WriteString("</b>")
	case *ast.Paragraph:
		r.blockSep()
		r.renderChildren(n)
	case *ast.TextBlock:
		r.renderChildren(n)
	case *ast.Blockquote:
		r.blockSep()
		r.sb.WriteString("<blockquote>")
		r.renderChildren(n)
		r.sb.WriteString("</blockquote>")
	case *ast.List:
		r.renderList(n)
	case *ast.FencedCodeBlock:
		r.blockSep()
		lang := string(n.Language(r.source))
		r.sb.WriteString("<pre><code")
		if lang != "" {
			r.sb.WriteString(` class="language-` + escapeAttr(lang) + `"`)
		}
		r.sb.WriteString(">")
		r.writeLines(n)
		r.sb.WriteString("</code></pre>")
	case *ast.CodeBlock:
		r.blockSep()
		r.sb.WriteString("<pre><code>")
		r.writeLines(n)
		r.sb.WriteString("</code></pre>")
	case *ast.ThematicBreak:
		r.blockSep()
		r.sb.WriteString("———")
	case *ast.Emphasis:
		tag := "i"
		if n.Level == 2 {
			tag = "b"
		}
		r.sb.WriteString("<" + tag + ">")
		r.renderChildren(n)
		r.sb.WriteString("</" + tag + ">")
	case *east.Strikethrough:
		r.sb.WriteString("<s>")
		r.renderChildren(n)
		r.sb.WriteString("</s>")
	case *ast.CodeSpan:
		r.sb.WriteString("<code>")
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				r.sb.WriteString(escape(string(t.Segment.Value(r.source))))
			}
		}
		r.sb.WriteString("</code>")
	case *ast.Link:
		r.sb.WriteString(`<a href="` + escapeAttr(string(n.Destination)) + `">`)
		r.renderChildren(n)
		r.sb.WriteString("</a>")
	case *ast.AutoLink:
		url := string(n.URL(r.source))
		r.sb.WriteString(`<a href="` + escapeAttr(url) + `">` + escape(url) + "</a>")
	case *ast.Text:
		r.sb.WriteString(escape(string(n.Segment.Value(r.source))))
		if n.SoftLineBreak() || n.HardLineBreak() {
			r.sb.WriteString("\n")
		}
	case *ast.String:
		r.sb.WriteString(escape(string(n.Value)))
	default:
		r.renderChildren(n)
	}
}

// renderList flattens a list to "• item" lines separated by single newlines.
// List item content is rendered inline (no block spacing between items).
func (r *renderer) renderList(n *ast.List) {
	r.blockSep()
	first := true
	for li := n.FirstChild(); li != nil; li = li.NextSibling() {
		if !first {
			r.sb.WriteString("\n")
		}
		first = false
		r.sb.WriteString("• ")
		// A list item's children are block nodes (TextBlock/Paragraph); render
		// their inline children directly so items stay on one line.
		for blk := li.FirstChild(); blk != nil; blk = blk.NextSibling() {
			r.renderChildren(blk)
		}
	}
}

func (r *renderer) writeLines(n ast.Node) {
	lines := n.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		r.sb.WriteString(escape(string(seg.Value(r.source))))
	}
}

// escape replaces the three characters Telegram requires escaping in HTML text.
func escape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// escapeAttr escapes a value destined for a double-quoted attribute (a link
// href, a code-fence language class). On top of the text escapes it also
// neutralizes " so a quote in the value cannot terminate the attribute early
// and produce malformed markup that Telegram would reject with a 400.
func escapeAttr(s string) string {
	return strings.ReplaceAll(escape(s), `"`, "&quot;")
}
