package render

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/runs"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestRunHTMLEscapesHostileToolOutput(t *testing.T) {
	nasty := "</details></main><script>alert(1)</script> & <b>bold</b> ]]> \x00\xff end"
	page := renderRunHTML("run-1", []llm.Message{
		{Role: llm.RoleUser, Content: "look at <this>"},
		{Role: llm.RoleTool, Name: "bash", Content: nasty},
		{Role: llm.RoleAssistant, Content: "after the nasty one"},
	}, nil)

	for _, forbidden := range []string{"</details></main>", "<b>bold</b>"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("page contains unescaped %q — tool output escaped into markup", forbidden)
		}
	}
	if n := strings.Count(page, "<script"); n != 1 {
		t.Errorf("page has %d <script> tags, want 1 (the renderer's own)", n)
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("the tool's <script> was not rendered as escaped text")
	}
	if !strings.Contains(page, "after the nasty one") {
		t.Error("the message following hostile tool output is missing — the page was truncated")
	}
	if !strings.Contains(page, "&lt;this&gt;") {
		t.Error("user text was not escaped")
	}
	if o, c := strings.Count(page, "<details"), strings.Count(page, "</details>"); o != c {
		t.Errorf("unbalanced details: %d open, %d close", o, c)
	}
	if !strings.HasSuffix(strings.TrimSpace(page), "</html>") {
		t.Error("page does not end with </html>")
	}
}

func TestRunHTMLDropsInvalidUTF8(t *testing.T) {
	page := renderRunHTML("run-2", []llm.Message{
		{Role: llm.RoleTool, Name: "cat", Content: "before \xc3\x28 after"},
	}, nil)
	if !strings.Contains(page, "before") || !strings.Contains(page, "after") {
		t.Error("valid text around an invalid sequence was lost")
	}
	if !utf8.ValidString(page) {
		t.Error("the rendered page is not valid UTF-8")
	}
}

func TestRunHTMLFoldsTheBulkShut(t *testing.T) {
	page := renderRunHTML("run-3", []llm.Message{
		{Role: llm.RoleAssistant, Content: "the answer", ReasoningContent: "long private thinking"},
		{Role: llm.RoleTool, Name: "bash", Content: "10000 lines of output"},
	}, nil)
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

func TestRunHTMLEmptyRun(t *testing.T) {
	page := renderRunHTML("run-4", nil, nil)
	if !strings.Contains(page, "no messages") {
		t.Error("empty run does not say so")
	}
	if !strings.HasPrefix(page, "<!doctype html>") || !strings.HasSuffix(strings.TrimSpace(page), "</html>") {
		t.Error("empty run did not render a whole document")
	}
}

func TestRunHTMLRejectsTraversalIDs(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../other", "a/b", "runs/../../etc"} {
		if _, err := RunReplayHTML(t.TempDir(), id); err == nil {
			t.Errorf("RunReplayHTML accepted invalid id %q", id)
		}
	}
}

func TestRunReplayHTMLLoadsStoredConversation(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession(runs.Meta{Agent: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "stored <question>"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleAssistant, Content: "stored answer"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	page, err := RunReplayHTML(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "stored &lt;question&gt;") || !strings.Contains(page, "stored answer") {
		t.Fatalf("stored conversation missing:\n%s", page)
	}
}

func TestRunReplayShowsSystemPrompts(t *testing.T) {
	page := renderRunHTML("run-p", []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Content: "answer"},
		{Role: llm.RoleUser, Content: "second"},
	}, []runs.PromptRecord{
		{Seq: 0, Hash: "aaaaaaaabbbb", Text: "original prompt", TS: time.Now()},
		{Seq: 2, Hash: "ccccccccdddd", Text: "edited prompt", TS: time.Now()},
	})
	for _, want := range []string{"system prompt", "original prompt", "edited prompt", "aaaaaaaa"} {
		if !strings.Contains(page, want) {
			t.Errorf("replay missing %q", want)
		}
	}
	if strings.Index(page, "original prompt") > strings.Index(page, "edited prompt") {
		t.Error("prompt versions rendered out of order")
	}
	if strings.Index(page, "edited prompt") < strings.Index(page, "answer") {
		t.Error("the edited prompt must be folded in at the message it took effect from")
	}
}

func TestRunReplayEscapesPromptText(t *testing.T) {
	page := renderRunHTML("run-e", nil, []runs.PromptRecord{
		{Seq: 0, Hash: "aaaaaaaabbbb", Text: "<script>x</script>"},
	})
	if strings.Contains(page, "<script>x") {
		t.Fatal("prompt text was not escaped")
	}
}
