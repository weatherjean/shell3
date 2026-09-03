package console

import (
	"fmt"
	"io"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

const testMarkdown = "# Markdown heading\n\n" +
	"Plain text with **bold**, *italic*, `inline code`, and a [link](https://example.com).\n\n" +
	"> A block quote checks its gutter and wrapping.\n\n" +
	"- Unordered item\n" +
	"- A second item with enough text to exercise ordinary terminal wrapping without introducing a document margin.\n\n" +
	"1. Ordered item\n" +
	"2. Another ordered item\n\n" +
	"| column | value |\n| --- | --- |\n| status | rendered |\n\n" +
	"```sh\nprintf 'fenced code\\n'\n```\n\n---\n\nFinal paragraph."

func renderTestOutput(out io.Writer, theme consoleTheme) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, theme.info.Render("render test"))
	fmt.Fprintln(out, theme.prompt.Render("you› Sample user prompt"))
	if theme.tty {
		fmt.Fprintln(out, rainbow("thinking", 0))
	} else {
		fmt.Fprintln(out, "… thinking")
	}

	r := turnRenderer{out: out, theme: theme}
	r.event(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolInput: `{"command":"printf 'one\\ntwo\\nthree\\n'"}`})
	r.event(shell3.Event{Kind: shell3.ToolResult, ToolName: "bash", ToolOutput: strings.Repeat("one two three four five ", 20)})
	r.event(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash_bg", ToolInput: `{"command":"long-running-command --flag"}`})
	r.event(shell3.Event{Kind: shell3.ToolResult, ToolName: "bash_bg", ToolOutput: "background job started: job-123"})
	r.event(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolInput: `{"command":"failing-command"}`})
	r.event(shell3.Event{Kind: shell3.ToolResult, ToolName: "bash", ToolOutput: "command not found", ToolError: true})
	r.event(shell3.Event{Kind: shell3.ToolCall, ToolName: "edit_file", ToolInput: `{"file_path":"notes.md"}`})
	r.event(shell3.Event{Kind: shell3.ToolResult, ToolName: "edit_file", ToolOutput: "updated notes.md\n2 lines added\n1 line removed"})
	r.event(shell3.Event{Kind: shell3.ToolCall, ToolName: "sample_tool", ToolInput: `{"value":"custom tool color"}`})
	r.event(shell3.Event{Kind: shell3.ToolResult, ToolName: "sample_tool", ToolOutput: "custom tool result"})
	r.event(shell3.Event{Kind: shell3.Retry, Text: "representative transient provider retry"})
	r.event(shell3.Event{Kind: shell3.Compacted})
	r.stopThinking()
	fmt.Fprintln(out, theme.info.Render("background report"))
	fmt.Fprintln(out, theme.err.Render("error: representative error"))
	r.answer.WriteString(testMarkdown)
	r.event(shell3.Event{Kind: shell3.Done, PromptTokens: 1234, CompletionTokens: 56, TotalTokens: 1290})
	r.finish()
}
