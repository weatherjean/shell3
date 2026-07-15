package chat

import (
	"reflect"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
)

func asst(calls ...llm.ToolCall) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}
}

func TestExtractFileManifest(t *testing.T) {
	head := []llm.Message{
		asst(llm.ToolCall{Name: "edit_file", RawArgs: `{"file_path":"b.go","old_string":"x","new_string":"y"}`}),
		asst(llm.ToolCall{Name: "edit_file", RawArgs: `{"file_path":"b.go","old_string":"y","new_string":"z"}`}), // dup edit
		asst(llm.ToolCall{Name: "bash", RawArgs: `{"command":"cat c.go"}`}),                                      // invisible
		asst(llm.ToolCall{Name: "edit_file", RawArgs: `{bad json`}),                                              // skipped
		// Non-assistant role carrying tool calls must be ignored (role guard).
		{Role: llm.RoleUser, ToolCalls: []llm.ToolCall{{Name: "edit_file", RawArgs: `{"file_path":"ignored.go"}`}}},
	}
	mod := extractFileManifest(head)
	if !reflect.DeepEqual(mod, []string{"b.go"}) {
		t.Fatalf("modified = %v, want [b.go]", mod)
	}
}

func TestCompactInto_ManifestRendered(t *testing.T) {
	sess := newTestSession(t)
	sess.messages = []llm.Message{
		msg(llm.RoleUser, "old-1"), msg(llm.RoleAssistant, "old-2"),
	}
	compactInto(CompactSummary{
		Summary:        "did stuff",
		ImportantFiles: []string{"b.go"},
	}, nil, sess, nil, applog.Noop{}, "", "", "")

	if len(sess.messages) < 1 {
		t.Fatalf("expected at least a continuation message, got %d", len(sess.messages))
	}
	continuation := sess.messages[0].Content
	for _, want := range []string{
		"<modified-files>",
		"- b.go",
		"</modified-files>",
	} {
		if !strings.Contains(continuation, want) {
			t.Fatalf("expected continuation to contain %q, got:\n%s", want, continuation)
		}
	}
}
