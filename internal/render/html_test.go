package render

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/llm"
)

// Tool output is arbitrary bytes. A result carrying "</details>" or "<script>"
// must land as TEXT — if it lands as markup it closes the message it belongs
// to and swallows every message after it, so one hostile-looking log line
// silently truncates the transcript. mdhtml was bitten by this exact class,
// where raw HTML in prose was dropped and cut words out of a reply mid-sentence.
func TestRunHTMLEscapesHostileToolOutput(t *testing.T) {
	nasty := "</details></main><script>alert(1)</script> & <b>bold</b> ]]> \x00\xff end"
	page := renderRunHTML("run-1", []llm.Message{
		{Role: llm.RoleUser, Content: "look at <this>"},
		{Role: llm.RoleTool, Name: "bash", Content: nasty},
		{Role: llm.RoleAssistant, Content: "after the nasty one"},
	})

	for _, forbidden := range []string{"</details></main>", "<b>bold</b>"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("page contains unescaped %q — tool output escaped into markup", forbidden)
		}
	}
	// The page carries exactly one <script>: the expand/collapse helper this
	// renderer writes. A second one means the tool's output opened its own.
	if n := strings.Count(page, "<script"); n != 1 {
		t.Errorf("page has %d <script> tags, want 1 (the renderer's own)", n)
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("the tool's <script> was not rendered as escaped text")
	}
	// The message AFTER the hostile one must still be there: proof the page
	// structure survived rather than being closed early.
	if !strings.Contains(page, "after the nasty one") {
		t.Error("the message following hostile tool output is missing — the page was truncated")
	}
	if !strings.Contains(page, "&lt;this&gt;") {
		t.Error("user text was not escaped")
	}
	// One <details> per message, plus none left unbalanced.
	if o, c := strings.Count(page, "<details"), strings.Count(page, "</details>"); o != c {
		t.Errorf("unbalanced details: %d open, %d close", o, c)
	}
	if !strings.HasSuffix(strings.TrimSpace(page), "</html>") {
		t.Error("page does not end with </html>")
	}
}

// Invalid UTF-8 must not reach the browser: a truncated multibyte sequence
// makes a page the parser rejects mid-document.
func TestRunHTMLDropsInvalidUTF8(t *testing.T) {
	page := renderRunHTML("run-2", []llm.Message{
		{Role: llm.RoleTool, Name: "cat", Content: "before \xc3\x28 after"},
	})
	if !strings.Contains(page, "before") || !strings.Contains(page, "after") {
		t.Error("valid text around an invalid sequence was lost")
	}
	if !utf8.ValidString(page) {
		t.Error("the rendered page is not valid UTF-8")
	}
}

// Reasoning and tool results are folded shut; what a person or the model
// actually wrote stays open. That default is the entire point — a 455-message
// run is unreadable with every result expanded.
func TestRunHTMLFoldsTheBulkShut(t *testing.T) {
	page := renderRunHTML("run-3", []llm.Message{
		{Role: llm.RoleAssistant, Content: "the answer", ReasoningContent: "long private thinking"},
		{Role: llm.RoleTool, Name: "bash", Content: "10000 lines of output"},
	})
	if !strings.Contains(page, `<details class="m assistant" open>`) {
		t.Error("assistant message is not open by default")
	}
	if strings.Contains(page, `<details class="m tool" open>`) {
		t.Error("tool result is open by default — the bulk must start folded")
	}
	if strings.Contains(page, `<details class="reason" open>`) {
		t.Error("reasoning is open by default")
	}
}

// A run with no messages still renders a valid page rather than a fragment.
func TestRunHTMLEmptyRun(t *testing.T) {
	page := renderRunHTML("run-4", nil)
	if !strings.Contains(page, "no messages") {
		t.Error("empty run does not say so")
	}
	if !strings.HasPrefix(page, "<!doctype html>") || !strings.HasSuffix(strings.TrimSpace(page), "</html>") {
		t.Error("empty run did not render a whole document")
	}
}
