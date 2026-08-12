//go:build live && unix

package media

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveOpenRouterSpeakTranscribeRoundtrip synthesizes a known sentence via
// OpenRouter's /audio/speech (mp3 — OpenRouter offers no opus), then feeds the
// resulting file back through /audio/transcriptions (whisper-1) and asserts
// the sentence survives the roundtrip. One test, both wire paths, no human
// listening required.
func TestLiveOpenRouterSpeakTranscribeRoundtrip(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	script := `
models:
  m:
    base_url: https://openrouter.ai/api/v1
    api_key: "` + key + `"
    model: hexgrad/kokoro-82m
  or-stt:
    base_url: https://openrouter.ai/api/v1
    api_key: "` + key + `"
    model: openai/whisper-1
media:
  tts: { model: m, voice: af_bella, format: mp3 }
  stt: { model: or-stt, language: en }
`
	c := newTestClients(t, script, nil)
	if c.Speak == nil || c.Transcribe == nil {
		t.Fatal("Speak/Transcribe not configured")
	}

	const sentence = "The quick brown fox jumps over the lazy dog."
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sp, err := c.Speak(ctx, sentence)
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	defer os.Remove(sp.Path)
	info, err := os.Stat(sp.Path)
	if err != nil || info.Size() < 1024 {
		t.Fatalf("synthesized file suspicious: %v (size=%d)", err, info.Size())
	}
	t.Logf("live TTS: %s (%d bytes)", sp.Path, info.Size())

	transcript, err := c.Transcribe(ctx, sp.Path)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	t.Logf("live roundtrip transcript: %q", transcript)
	low := strings.ToLower(transcript)
	for _, word := range []string{"quick", "brown", "fox", "lazy", "dog"} {
		if !strings.Contains(low, word) {
			t.Errorf("transcript missing %q: %q", word, transcript)
		}
	}
}
