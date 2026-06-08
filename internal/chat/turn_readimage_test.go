package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

func msgHasImagePart(m llm.Message) bool {
	for _, p := range m.ContentParts {
		if p.Type == llm.ContentPartTypeImageURL &&
			strings.HasPrefix(p.ImageURL, "data:image/jpeg;base64,") {
			return true
		}
	}
	return false
}

func anyMsgHasImagePart(msgs []llm.Message) bool {
	for _, m := range msgs {
		if msgHasImagePart(m) {
			return true
		}
	}
	return false
}

// TestRunTurn_ReadImage_InjectsImageUserMessage drives a turn where round 1 calls
// read_image and round 2 returns text. The image must be injected as a user
// message AFTER the tool result, and round 2's prompt must carry the image part.
func TestRunTurn_ReadImage_InjectsImageUserMessage(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath) // helper from image_test.go (same package)

	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "i", Name: "read_image", RawArgs: `{"path":"` + imgPath + `"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "I see a red square"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
	}

	_, sess := collectTurn(t, context.Background(), cfg, "what is this")

	if !hasToolMessage(sess, "read_image", "Loaded image") {
		t.Fatalf("expected read_image tool result, got %+v", sess.messages)
	}

	toolIdx, imgUserIdx := -1, -1
	for i, m := range sess.messages {
		if m.Role == llm.RoleTool && m.Name == "read_image" {
			toolIdx = i
		}
		if m.Role == llm.RoleUser && msgHasImagePart(m) {
			imgUserIdx = i
		}
	}
	if imgUserIdx < 0 {
		t.Fatalf("no injected image user message in %+v", sess.messages)
	}
	if imgUserIdx <= toolIdx {
		t.Fatalf("image user message must follow the tool result (tool=%d img=%d)", toolIdx, imgUserIdx)
	}

	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
	if !anyMsgHasImagePart(fake.Calls[1].Msgs) {
		t.Fatalf("round 2 prompt is missing the injected image part")
	}
}

// TestRunTurn_ReadImage_ErrorQueuesNoImage: a bad path yields an error tool
// result and NO injected image user message.
func TestRunTurn_ReadImage_ErrorQueuesNoImage(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "i", Name: "read_image", RawArgs: `{"path":"/no/such/file.png"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "could not load"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
	}

	_, sess := collectTurn(t, context.Background(), cfg, "look")

	for _, m := range sess.messages {
		if m.Role == llm.RoleUser && msgHasImagePart(m) {
			t.Fatalf("error path must not inject an image user message")
		}
	}

	if !hasToolMessage(sess, "read_image", "error") {
		t.Fatalf("expected an error tool result for the bad path, got %+v", sess.messages)
	}
}

// TestRunTurn_ReadImage_MultipleInOneRound scripts two read_image tool calls in a
// single assistant round, then text in round 2. Exactly one injected image user
// message must be produced, carrying both image parts.
func TestRunTurn_ReadImage_MultipleInOneRound(t *testing.T) {
	tmp := t.TempDir()
	p1 := filepath.Join(tmp, "a.png")
	p2 := filepath.Join(tmp, "b.png")
	writePNG(t, p1)
	writePNG(t, p2)

	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "1", Name: "read_image", RawArgs: `{"path":"` + p1 + `"}`}},
			{ToolCall: &llm.ToolCall{ID: "2", Name: "read_image", RawArgs: `{"path":"` + p2 + `"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "two images"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
	}

	_, sess := collectTurn(t, context.Background(), cfg, "look at both")

	imgMsgCount, imgPartCount := 0, 0
	for _, m := range sess.messages {
		if m.Role == llm.RoleUser && msgHasImagePart(m) {
			imgMsgCount++
			for _, p := range m.ContentParts {
				if p.Type == llm.ContentPartTypeImageURL {
					imgPartCount++
				}
			}
		}
	}
	if imgMsgCount != 1 {
		t.Fatalf("want exactly 1 injected image user message, got %d", imgMsgCount)
	}
	if imgPartCount != 2 {
		t.Fatalf("want 2 image parts in the injected message, got %d", imgPartCount)
	}
}
