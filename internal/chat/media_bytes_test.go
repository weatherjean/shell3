package chat

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// pngBytes encodes a w×h solid-red PNG in memory (no disk; the byte loaders
// must work without paths). Distinct from image_test.go's writePNG, which
// writes a file.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestMediaPartFromBytes_PNG: image bytes become a resized-JPEG data-URI part
// with the same description format LoadMediaPart produces.
func TestMediaPartFromBytes_PNG(t *testing.T) {
	part, desc, err := MediaPartFromBytes(pngBytes(t, 3, 2), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if part.Type != llm.ContentPartTypeImageURL || !strings.HasPrefix(part.ImageURL, "data:image/jpeg;base64,") {
		t.Fatalf("part = %+v", part)
	}
	if desc != "image 3x2" {
		t.Fatalf("desc = %q, want \"image 3x2\"", desc)
	}
}

// TestMediaPartFromBytes_MIMENormalization: case and ";"-parameters are
// tolerated (Telegram and HTTP stacks send e.g. "audio/ogg; codecs=opus" —
// for supported types we must accept "image/PNG; charset=binary" forms).
func TestMediaPartFromBytes_MIMENormalization(t *testing.T) {
	if _, _, err := MediaPartFromBytes(pngBytes(t, 1, 1), "IMAGE/PNG; charset=binary"); err != nil {
		t.Fatalf("MIME params/case should be tolerated: %v", err)
	}
}

// TestMediaPartFromBytes_Audio: every accepted audio MIME maps to the right
// wire format and the data is base64 of the input, untranscoded.
func TestMediaPartFromBytes_Audio(t *testing.T) {
	raw := []byte("RIFF....fake-wav-payload")
	for mime, format := range map[string]string{
		"audio/wav": "wav", "audio/x-wav": "wav", "audio/wave": "wav",
		"audio/mpeg": "mp3", "audio/mp3": "mp3",
	} {
		part, desc, err := MediaPartFromBytes(raw, mime)
		if err != nil {
			t.Fatalf("%s: %v", mime, err)
		}
		if part.Type != llm.ContentPartTypeInputAudio || part.AudioFormat != format {
			t.Fatalf("%s: part = %+v, want input_audio/%s", mime, part, format)
		}
		if part.AudioData != base64.StdEncoding.EncodeToString(raw) {
			t.Fatalf("%s: AudioData is not the base64 of the input", mime)
		}
		if !strings.Contains(desc, format+" audio") {
			t.Fatalf("%s: desc = %q", mime, desc)
		}
	}
}

// TestMediaPartFromBytes_UnsupportedMIME: anything else errors with guidance.
func TestMediaPartFromBytes_UnsupportedMIME(t *testing.T) {
	if _, _, err := MediaPartFromBytes([]byte("x"), "video/mp4"); err == nil || !strings.Contains(err.Error(), "unsupported MIME") {
		t.Fatalf("want unsupported-MIME error, got %v", err)
	}
}

// TestMediaPartFromBytes_SizeCaps: the path loaders' caps apply to bytes too.
func TestMediaPartFromBytes_SizeCaps(t *testing.T) {
	if _, _, err := MediaPartFromBytes(make([]byte, maxImageBytes+1), "image/png"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("image cap not enforced: %v", err)
	}
	if _, _, err := MediaPartFromBytes(make([]byte, maxAudioBytes+1), "audio/wav"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("audio cap not enforced: %v", err)
	}
}
