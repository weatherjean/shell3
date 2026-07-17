//go:build unix

package shell3

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// TestSubagentSession_ReadMediaRoundtrips pins the tools={media=true}
// guarantee for subagents at the runtime level: a HEADLESS child session
// (exactly what the job manager spawns) whose model calls read_media on an
// image must get the decoded media part injected into the next LLM round —
// the attach mechanism must not depend on anything only the main session has.
func TestSubagentSession_ReadMediaRoundtrips(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(imgPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "i", Name: "read_media", RawArgs: `{"path":"` + imgPath + `"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "a red square"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: fake, ModeLabel: "seer"}
	})
	child, err := rt.Session(SessionOpts{Agent: "seer", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	for range child.Send(context.Background(), "what is in shot.png?") {
	}

	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
	found := false
	for _, m := range fake.Calls[1].Msgs {
		for _, p := range m.ContentParts {
			if p.Type == llm.ContentPartTypeImageURL && strings.HasPrefix(p.ImageURL, "data:image/jpeg;base64,") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("round 2 prompt in the headless child session is missing the injected media part")
	}
}
