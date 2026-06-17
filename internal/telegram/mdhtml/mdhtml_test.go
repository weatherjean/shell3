package mdhtml

import "testing"

func TestToTelegramHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bold", "**hi**", "<b>hi</b>"},
		{"italic", "*hi*", "<i>hi</i>"},
		{"bold underscore", "__hi__", "<b>hi</b>"},
		{"inline code", "`x`", "<code>x</code>"},
		{"strikethrough", "~~x~~", "<s>x</s>"},
		{"link", "[t](http://u)", `<a href="http://u">t</a>`},
		{"heading becomes bold", "# Title", "<b>Title</b>"},
		{"escape special chars", "a < b & c > d", "a &lt; b &amp; c &gt; d"},
		{"bullet list", "- one\n- two", "• one\n• two"},
		{"plain paragraph", "just text", "just text"},
		{"unterminated bold stays literal", "**oops", "**oops"},
		{"angle brackets in code escaped", "`a<b>c`", "<code>a&lt;b&gt;c</code>"},
		{"paragraphs separated by blank line", "one\n\ntwo", "one\n\ntwo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ToTelegramHTML(c.in)
			if got != c.want {
				t.Errorf("ToTelegramHTML(%q)\n  got:  %q\n  want: %q", c.in, got, c.want)
			}
		})
	}
}

func TestToTelegramHTMLCodeFence(t *testing.T) {
	in := "```go\nx := 1\n```"
	want := "<pre><code class=\"language-go\">x := 1\n</code></pre>"
	if got := ToTelegramHTML(in); got != want {
		t.Errorf("fence:\n  got:  %q\n  want: %q", got, want)
	}
}
