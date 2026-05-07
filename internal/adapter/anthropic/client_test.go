package anthropic

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestParamSpecs(t *testing.T) {
	c := &Client{}
	specs := c.ParamSpecs()
	names := map[string]bool{}
	for _, s := range specs {
		names[s.Name] = true
	}
	for _, want := range []string{"reasoning_effort", "max_tokens", "temperature"} {
		if !names[want] {
			t.Errorf("missing param spec %q", want)
		}
	}
}

func TestToAnthropicMessages_Basic(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	out, system := toAnthropicMessages(msgs)
	if system != "" {
		t.Fatalf("expected no system, got %q", system)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
}

func TestToAnthropicMessages_SystemExtracted(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "you are helpful"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	out, system := toAnthropicMessages(msgs)
	if system != "you are helpful" {
		t.Fatalf("system: %q", system)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 non-system msg, got %d", len(out))
	}
}

func TestToAnthropicMessages_ToolResultGrouped(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "bash", RawArgs: `{"cmd":"ls"}`},
			{ID: "tc2", Name: "bash", RawArgs: `{"cmd":"pwd"}`},
		}},
		{Role: llm.RoleTool, Content: "file.txt", ToolCallID: "tc1"},
		{Role: llm.RoleTool, Content: "/home", ToolCallID: "tc2"},
	}
	out, _ := toAnthropicMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages (assistant+grouped-user), got %d", len(out))
	}
}

func TestImageBlock_DataURI(t *testing.T) {
	blk, ok := imageBlock("data:image/png;base64,AAAA")
	if !ok {
		t.Fatal("expected ok for valid data URI")
	}
	if blk.OfImage == nil || blk.OfImage.Source.OfBase64 == nil {
		t.Fatalf("expected base64 image source, got %+v", blk)
	}
	if string(blk.OfImage.Source.OfBase64.MediaType) != "image/png" {
		t.Errorf("media type: %q", blk.OfImage.Source.OfBase64.MediaType)
	}
	if blk.OfImage.Source.OfBase64.Data != "AAAA" {
		t.Errorf("data: %q", blk.OfImage.Source.OfBase64.Data)
	}
}

func TestImageBlock_HTTPSURL(t *testing.T) {
	blk, ok := imageBlock("https://example.com/cat.png")
	if !ok {
		t.Fatal("expected ok for https URL")
	}
	if blk.OfImage == nil || blk.OfImage.Source.OfURL == nil {
		t.Fatalf("expected url image source, got %+v", blk)
	}
	if blk.OfImage.Source.OfURL.URL != "https://example.com/cat.png" {
		t.Errorf("url: %q", blk.OfImage.Source.OfURL.URL)
	}
}

func TestImageBlock_RejectsUnknownMediaType(t *testing.T) {
	if _, ok := imageBlock("data:image/heic;base64,XXX"); ok {
		t.Fatal("expected reject for image/heic — Anthropic accepts only jpeg/png/gif/webp")
	}
	if _, ok := imageBlock("ftp://nope/img.png"); ok {
		t.Fatal("expected reject for ftp scheme")
	}
	if _, ok := imageBlock(""); ok {
		t.Fatal("expected reject for empty input")
	}
}

func TestToAnthropicMessages_VisionContentParts(t *testing.T) {
	msgs := []llm.Message{
		{
			Role: llm.RoleUser,
			ContentParts: []llm.ContentPart{
				{Type: llm.ContentPartTypeText, Text: "describe this"},
				{Type: llm.ContentPartTypeImageURL, ImageURL: "https://example.com/cat.png"},
			},
		},
	}
	out, _ := toAnthropicMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 user msg, got %d", len(out))
	}
	if got := len(out[0].Content); got != 2 {
		t.Fatalf("expected 2 content blocks (text + image), got %d", got)
	}
}

func TestToAnthropicTools(t *testing.T) {
	tools := []llm.ToolDefinition{
		{
			Name:        "bash",
			Description: "run shell",
			Parameters: map[string]any{
				"type":     "object",
				"properties": map[string]any{
					"cmd": map[string]any{"type": "string"},
				},
				"required": []any{"cmd"},
			},
		},
	}
	out := toAnthropicTools(tools)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	tp := out[0].OfTool
	if tp == nil {
		t.Fatalf("expected OfTool set, got %+v", out[0])
	}
	if tp.Name != "bash" {
		t.Fatalf("name: %q", tp.Name)
	}
	if len(tp.InputSchema.Required) != 1 || tp.InputSchema.Required[0] != "cmd" {
		t.Fatalf("required: %+v", tp.InputSchema.Required)
	}
}
