package acp

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// ── toolKind ──────────────────────────────────────────────────────────────────

func TestToolKind(t *testing.T) {
	cases := []struct {
		name string
		want acpsdk.ToolKind
	}{
		{"bash", acpsdk.ToolKindExecute},
		{"bash_bg", acpsdk.ToolKindExecute},
		{"shell_interactive", acpsdk.ToolKindExecute},
		{"read", acpsdk.ToolKindRead},
		{"list_files", acpsdk.ToolKindSearch},
		{"edit_file", acpsdk.ToolKindEdit},
		{"custom_tool", acpsdk.ToolKindOther},
		{"", acpsdk.ToolKindOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolKind(tc.name)
			if got != tc.want {
				t.Fatalf("toolKind(%q) = %q; want %q", tc.name, got, tc.want)
			}
		})
	}
}

// ── promptToParts ─────────────────────────────────────────────────────────────

func TestPromptToParts_TextOnly(t *testing.T) {
	blocks := []acpsdk.ContentBlock{
		acpsdk.TextBlock("hello world"),
	}
	prompt, parts, err := promptToParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt != "hello world" {
		t.Fatalf("prompt = %q; want %q", prompt, "hello world")
	}
	if len(parts) != 0 {
		t.Fatalf("parts len = %d; want 0", len(parts))
	}
}

func TestPromptToParts_MultipleText(t *testing.T) {
	blocks := []acpsdk.ContentBlock{
		acpsdk.TextBlock("line1"),
		acpsdk.TextBlock("line2"),
	}
	prompt, _, err := promptToParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "line1") || !strings.Contains(prompt, "line2") {
		t.Fatalf("prompt %q missing expected content", prompt)
	}
}

func TestPromptToParts_TextAndImage(t *testing.T) {
	rawBytes := []byte("fake-image-data")
	encoded := base64.StdEncoding.EncodeToString(rawBytes)

	blocks := []acpsdk.ContentBlock{
		acpsdk.TextBlock("describe this"),
		acpsdk.ImageBlock(encoded, "image/png"),
	}
	prompt, parts, err := promptToParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt != "describe this" {
		t.Fatalf("prompt = %q; want %q", prompt, "describe this")
	}
	if len(parts) != 1 {
		t.Fatalf("parts len = %d; want 1", len(parts))
	}
	p := parts[0]
	if p.Kind != shell3.PartImage {
		t.Fatalf("part kind = %v; want PartImage", p.Kind)
	}
	if string(p.Data) != string(rawBytes) {
		t.Fatalf("part data mismatch: got %q want %q", p.Data, rawBytes)
	}
	if p.MIME != "image/png" {
		t.Fatalf("part MIME = %q; want %q", p.MIME, "image/png")
	}
}

func TestPromptToParts_ResourceLink(t *testing.T) {
	blocks := []acpsdk.ContentBlock{
		acpsdk.ResourceLinkBlock("my-file", "file:///home/user/doc.txt"),
	}
	prompt, parts, err := promptToParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "file:///home/user/doc.txt") {
		t.Fatalf("prompt %q does not contain URI", prompt)
	}
	if len(parts) != 0 {
		t.Fatalf("parts len = %d; want 0", len(parts))
	}
}

func TestPromptToParts_EmbeddedResource(t *testing.T) {
	res := acpsdk.EmbeddedResourceResource{}
	res.TextResourceContents = &acpsdk.TextResourceContents{
		Text: "package main\n\nfunc main() {}",
		Uri:  "file:///home/user/main.go",
	}
	blocks := []acpsdk.ContentBlock{
		acpsdk.ResourceBlock(res),
	}
	prompt, parts, err := promptToParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "package main") {
		t.Fatalf("prompt %q missing resource text", prompt)
	}
	// Should be in a fenced code block.
	if !strings.Contains(prompt, "```") {
		t.Fatalf("prompt %q missing fenced code block", prompt)
	}
	if len(parts) != 0 {
		t.Fatalf("parts len = %d; want 0", len(parts))
	}
}

func TestPromptToParts_BadBase64(t *testing.T) {
	blocks := []acpsdk.ContentBlock{
		acpsdk.ImageBlock("not-valid-base64!!!", "image/png"),
	}
	_, _, err := promptToParts(blocks)
	if err == nil {
		t.Fatal("expected error for bad base64, got nil")
	}
}

// ── updatesForEvent ───────────────────────────────────────────────────────────

// wireDiscriminator marshals an acp.SessionUpdate to JSON and extracts the
// "sessionUpdate" field value, which is the wire discriminator string.
func wireDiscriminator(t *testing.T, u acpsdk.SessionUpdate) string {
	t.Helper()
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal SessionUpdate: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	raw, ok := m["sessionUpdate"]
	if !ok {
		t.Fatalf("no sessionUpdate field in JSON: %s", b)
	}
	var disc string
	if err := json.Unmarshal(raw, &disc); err != nil {
		t.Fatalf("unmarshal discriminator: %v", err)
	}
	return disc
}

func TestUpdatesForEvent_Token(t *testing.T) {
	ev := shell3.Event{Kind: shell3.Token, Text: "hello"}
	updates := updatesForEvent(ev)
	if len(updates) != 1 {
		t.Fatalf("len = %d; want 1", len(updates))
	}
	disc := wireDiscriminator(t, updates[0])
	if disc != "agent_message_chunk" {
		t.Fatalf("discriminator = %q; want %q", disc, "agent_message_chunk")
	}
	if updates[0].AgentMessageChunk == nil {
		t.Fatal("AgentMessageChunk should be set")
	}
}

func TestUpdatesForEvent_Reasoning(t *testing.T) {
	ev := shell3.Event{Kind: shell3.Reasoning, Text: "thinking..."}
	updates := updatesForEvent(ev)
	if len(updates) != 1 {
		t.Fatalf("len = %d; want 1", len(updates))
	}
	disc := wireDiscriminator(t, updates[0])
	if disc != "agent_thought_chunk" {
		t.Fatalf("discriminator = %q; want %q", disc, "agent_thought_chunk")
	}
	if updates[0].AgentThoughtChunk == nil {
		t.Fatal("AgentThoughtChunk should be set")
	}
}

func TestUpdatesForEvent_ToolCall_BashCommandTitle(t *testing.T) {
	ev := shell3.Event{
		Kind:       shell3.ToolCall,
		ToolName:   "bash",
		ToolCallID: "tc-1",
		ToolInput:  `{"command":"ls -la /tmp"}`,
	}
	updates := updatesForEvent(ev)
	if len(updates) != 1 {
		t.Fatalf("len = %d; want 1", len(updates))
	}
	disc := wireDiscriminator(t, updates[0])
	if disc != "tool_call" {
		t.Fatalf("discriminator = %q; want %q", disc, "tool_call")
	}
	tc := updates[0].ToolCall
	if tc == nil {
		t.Fatal("ToolCall field should be set")
	}
	if tc.Title != "ls -la /tmp" {
		t.Fatalf("title = %q; want %q", tc.Title, "ls -la /tmp")
	}
	if tc.Kind != acpsdk.ToolKindExecute {
		t.Fatalf("kind = %q; want ToolKindExecute", tc.Kind)
	}
	if tc.Status != acpsdk.ToolCallStatusInProgress {
		t.Fatalf("status = %q; want in_progress", tc.Status)
	}
	if string(tc.ToolCallId) != "tc-1" {
		t.Fatalf("toolCallId = %q; want %q", tc.ToolCallId, "tc-1")
	}
}

func TestUpdatesForEvent_ToolCall_NonBashToolNameTitle(t *testing.T) {
	ev := shell3.Event{
		Kind:       shell3.ToolCall,
		ToolName:   "read",
		ToolCallID: "tc-2",
		ToolInput:  `{"path":"/foo/bar"}`,
	}
	updates := updatesForEvent(ev)
	if len(updates) != 1 {
		t.Fatalf("len = %d; want 1", len(updates))
	}
	tc := updates[0].ToolCall
	if tc == nil {
		t.Fatal("ToolCall field should be set")
	}
	if tc.Title != "read" {
		t.Fatalf("title = %q; want %q", tc.Title, "read")
	}
}

func TestUpdatesForEvent_ToolResult_OK(t *testing.T) {
	ev := shell3.Event{
		Kind:       shell3.ToolResult,
		ToolName:   "bash",
		ToolCallID: "tc-1",
		ToolOutput: "total 12\ndrwxr-xr-x ...",
		ToolError:  false,
	}
	updates := updatesForEvent(ev)
	if len(updates) != 1 {
		t.Fatalf("len = %d; want 1", len(updates))
	}
	disc := wireDiscriminator(t, updates[0])
	if disc != "tool_call_update" {
		t.Fatalf("discriminator = %q; want %q", disc, "tool_call_update")
	}
	tu := updates[0].ToolCallUpdate
	if tu == nil {
		t.Fatal("ToolCallUpdate should be set")
	}
	if tu.Status == nil || *tu.Status != acpsdk.ToolCallStatusCompleted {
		t.Fatalf("status = %v; want completed", tu.Status)
	}
	if len(tu.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
}

func TestUpdatesForEvent_ToolResult_Error(t *testing.T) {
	ev := shell3.Event{
		Kind:       shell3.ToolResult,
		ToolName:   "bash",
		ToolCallID: "tc-1",
		ToolOutput: "permission denied",
		ToolError:  true,
	}
	updates := updatesForEvent(ev)
	if len(updates) != 1 {
		t.Fatalf("len = %d; want 1", len(updates))
	}
	tu := updates[0].ToolCallUpdate
	if tu == nil {
		t.Fatal("ToolCallUpdate should be set")
	}
	if tu.Status == nil || *tu.Status != acpsdk.ToolCallStatusFailed {
		t.Fatalf("status = %v; want failed", tu.Status)
	}
}

func TestUpdatesForEvent_SystemReminder(t *testing.T) {
	ev := shell3.Event{Kind: shell3.SystemReminder, Text: "<system-reminder>\nmodel changed\n</system-reminder>"}
	updates := updatesForEvent(ev)
	if len(updates) != 1 {
		t.Fatalf("len = %d; want 1", len(updates))
	}
	disc := wireDiscriminator(t, updates[0])
	if disc != "agent_thought_chunk" {
		t.Fatalf("discriminator = %q; want %q", disc, "agent_thought_chunk")
	}
	if updates[0].AgentThoughtChunk == nil {
		t.Fatal("AgentThoughtChunk should be set")
	}
	// The reminder text is surfaced verbatim behind a warning glyph so the client
	// can distinguish a host reminder from the agent's own thoughts.
	tb := updates[0].AgentThoughtChunk.Content.Text
	if tb == nil {
		t.Fatal("thought content should be a text block")
	}
	if got := tb.Text; !strings.HasPrefix(got, "⚠ ") || !strings.Contains(got, "model changed") {
		t.Fatalf("thought text = %q; want a %q-prefixed reminder containing %q", got, "⚠ ", "model changed")
	}
}

func TestUpdatesForEvent_IgnoredKinds(t *testing.T) {
	ignored := []shell3.Event{
		{Kind: shell3.Compacted, Text: "compacted"},
		{Kind: shell3.Usage, PromptTokens: 100},
		{Kind: shell3.Retry, Text: "retry"},
		{Kind: shell3.Error},
		{Kind: shell3.Done},
	}
	for _, ev := range ignored {
		updates := updatesForEvent(ev)
		if len(updates) != 0 {
			t.Fatalf("kind %v: expected 0 updates, got %d", ev.Kind, len(updates))
		}
	}
}
