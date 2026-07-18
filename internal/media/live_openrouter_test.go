//go:build live && unix

package media

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSolidPNG writes a w×h solid-color PNG — big and unambiguous enough for
// a real vision model to name the color.
func writeSolidPNG(t *testing.T, path string, w, h int, r, g, b uint8) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// TestLiveOpenRouterDescribe exercises Describe against the real OpenRouter
// API (chat completions with an image part). Gated behind -tags live and the
// OPENROUTER_API_KEY env var; never runs in the normal suite:
//
//	OPENROUTER_API_KEY=... go test -tags live -run TestLiveOpenRouter ./internal/media/ -v
func TestLiveOpenRouterDescribe(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	script := `
models:
  m:
    base_url: https://openrouter.ai/api/v1
    api_key: "` + key + `"
    model: openai/gpt-4o-mini
media:
  describe: { model: m }
`
	c := newTestClients(t, script, nil)
	if c.Describe == nil {
		t.Fatal("Describe not configured")
	}

	img := filepath.Join(t.TempDir(), "red.png")
	writeSolidPNG(t, img, 64, 64, 220, 30, 30) // unmistakably red square

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	desc, err := c.Describe(ctx, img)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	t.Logf("live description: %q", desc)
	if !strings.Contains(strings.ToLower(desc), "red") {
		t.Errorf("description of a solid red square does not mention red: %q", desc)
	}
}

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
	if sp.VoiceCompatible {
		t.Errorf("mp3 output reported VoiceCompatible=true")
	}
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

// TestLiveOpenRouterGenerateImage exercises the api="openrouter" generator
// against the real chat-completions image-output route (~$0.03/run — the
// priciest live test; keep -run filters tight when iterating).
func TestLiveOpenRouterGenerateImage(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	script := `
models:
  m:
    base_url: https://openrouter.ai/api/v1
    api_key: "` + key + `"
    model: google/gemini-3.1-flash-lite-image
media:
  imagegen: { model: m, api: openrouter }
`
	c := newTestClients(t, script, nil)
	if c.Generate == nil {
		t.Fatal("Generate not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	path, err := c.Generate(ctx, "a small blue triangle on a white background, flat minimal", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer os.Remove(path)
	info, err := os.Stat(path)
	if err != nil || info.Size() < 4096 {
		t.Fatalf("generated file suspicious: %v (size=%d)", err, info.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, format, err := image.Decode(f); err != nil {
		t.Errorf("generated file does not decode as an image: %v", err)
	} else {
		t.Logf("live imagegen: %s (%d bytes, %s)", path, info.Size(), format)
	}
}
