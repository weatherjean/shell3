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

func msgHasMediaPart(m llm.Message) bool {
	for _, p := range m.ContentParts {
		if p.Type == llm.ContentPartTypeImageURL &&
			strings.HasPrefix(p.ImageURL, "data:image/jpeg;base64,") {
			return true
		}
		if p.Type == llm.ContentPartTypeInputAudio && p.AudioData != "" {
			return true
		}
	}
	return false
}

func anyMsgHasMediaPart(msgs []llm.Message) bool {
	for _, m := range msgs {
		if msgHasMediaPart(m) {
			return true
		}
	}
	return false
}

// TestRunTurn_ReadMedia_InjectsMediaUserMessage drives a turn where round 1 calls
// read_media and round 2 returns text. The media must be injected as a user
// message AFTER the tool result, and round 2's prompt must carry the media part.
func TestRunTurn_ReadMedia_InjectsMediaUserMessage(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath) // helper from image_test.go (same package)

	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "i", Name: "read_media", RawArgs: `{"path":"` + imgPath + `"}`}},
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

	if !hasToolMessage(sess, "read_media", "Loaded ") {
		t.Fatalf("expected read_media tool result, got %+v", sess.messages)
	}

	toolIdx, mediaUserIdx := -1, -1
	for i, m := range sess.messages {
		if m.Role == llm.RoleTool && m.Name == "read_media" {
			toolIdx = i
		}
		if m.Role == llm.RoleUser && msgHasMediaPart(m) {
			mediaUserIdx = i
		}
	}
	if mediaUserIdx < 0 {
		t.Fatalf("no injected media user message in %+v", sess.messages)
	}
	if mediaUserIdx <= toolIdx {
		t.Fatalf("media user message must follow the tool result (tool=%d media=%d)", toolIdx, mediaUserIdx)
	}

	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
	if !anyMsgHasMediaPart(fake.Calls[1].Msgs) {
		t.Fatalf("round 2 prompt is missing the injected media part")
	}
}

// TestRunTurn_ReadMedia_InjectsAudioUserMessage drives a turn where round 1 calls
// read_media on an mp3 and round 2 returns text. The audio part must be injected
// as a user message AFTER the tool result, and round 2's prompt must carry it.
func TestRunTurn_ReadMedia_InjectsAudioUserMessage(t *testing.T) {
	tmp := t.TempDir()
	audPath := filepath.Join(tmp, "clip.mp3")
	writeBytes(t, audPath, []byte("fake mp3 bytes")) // helper from audio_test.go

	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "read_media", RawArgs: `{"path":"` + audPath + `"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "I hear a clip"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
	}

	_, sess := collectTurn(t, context.Background(), cfg, "listen to this")

	if !hasToolMessage(sess, "read_media", "Loaded mp3 audio") {
		t.Fatalf("expected read_media audio tool result, got %+v", sess.messages)
	}

	toolIdx, audUserIdx := -1, -1
	for i, m := range sess.messages {
		if m.Role == llm.RoleTool && m.Name == "read_media" {
			toolIdx = i
		}
		if m.Role == llm.RoleUser {
			for _, p := range m.ContentParts {
				if p.Type == llm.ContentPartTypeInputAudio && p.AudioData != "" {
					audUserIdx = i
				}
			}
		}
	}
	if audUserIdx < 0 {
		t.Fatalf("no injected audio user message in %+v", sess.messages)
	}
	if audUserIdx <= toolIdx {
		t.Fatalf("audio user message must follow the tool result (tool=%d audio=%d)", toolIdx, audUserIdx)
	}

	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
	foundAudio := false
	for _, m := range fake.Calls[1].Msgs {
		for _, p := range m.ContentParts {
			if p.Type == llm.ContentPartTypeInputAudio && p.AudioData != "" {
				foundAudio = true
			}
		}
	}
	if !foundAudio {
		t.Fatalf("round 2 prompt is missing the injected input_audio part")
	}
}

// TestRunTurn_ReadMedia_ErrorQueuesNoMedia: a bad path yields an error tool
// result and NO injected media user message.
func TestRunTurn_ReadMedia_ErrorQueuesNoMedia(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "i", Name: "read_media", RawArgs: `{"path":"/no/such/file.png"}`}},
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
		if m.Role == llm.RoleUser && msgHasMediaPart(m) {
			t.Fatalf("error path must not inject a media user message")
		}
	}

	if !hasToolMessage(sess, "read_media", "error") {
		t.Fatalf("expected an error tool result for the bad path, got %+v", sess.messages)
	}
}

// TestRunTurn_ReadMedia_MultipleInOneRound scripts two read_media tool calls in a
// single assistant round, then text in round 2. Exactly one injected media user
// message must be produced, carrying both media parts.
func TestRunTurn_ReadMedia_MultipleInOneRound(t *testing.T) {
	tmp := t.TempDir()
	p1 := filepath.Join(tmp, "a.png")
	p2 := filepath.Join(tmp, "b.png")
	writePNG(t, p1)
	writePNG(t, p2)

	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "1", Name: "read_media", RawArgs: `{"path":"` + p1 + `"}`}},
			{ToolCall: &llm.ToolCall{ID: "2", Name: "read_media", RawArgs: `{"path":"` + p2 + `"}`}},
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

	mediaMsgCount, mediaPartCount := 0, 0
	for _, m := range sess.messages {
		if m.Role == llm.RoleUser && msgHasMediaPart(m) {
			mediaMsgCount++
			for _, p := range m.ContentParts {
				if p.Type == llm.ContentPartTypeImageURL {
					mediaPartCount++
				}
			}
		}
	}
	if mediaMsgCount != 1 {
		t.Fatalf("want exactly 1 injected media user message, got %d", mediaMsgCount)
	}
	if mediaPartCount != 2 {
		t.Fatalf("want 2 media parts in the injected message, got %d", mediaPartCount)
	}
}
