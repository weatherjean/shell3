package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func writeBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAudioPart_MP3(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "clip.mp3")
	writeBytes(t, p, []byte("ID3 fake mp3 bytes")) // not decoded, content irrelevant

	part, desc, err := loadAudioPart(p, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.Type != llm.ContentPartTypeInputAudio {
		t.Errorf("type = %q, want input_audio", part.Type)
	}
	if part.AudioFormat != "mp3" {
		t.Errorf("format = %q, want mp3", part.AudioFormat)
	}
	if part.AudioData == "" {
		t.Error("AudioData should be non-empty base64")
	}
	if !strings.Contains(desc, "mp3 audio") {
		t.Errorf("desc = %q", desc)
	}
}

func TestLoadAudioPart_WAV(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "clip.wav")
	writeBytes(t, p, []byte("RIFF fake wav"))
	part, _, err := loadAudioPart(p, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.AudioFormat != "wav" {
		t.Errorf("format = %q, want wav", part.AudioFormat)
	}
}

func TestLoadAudioPart_Unsupported(t *testing.T) {
	_, _, err := loadAudioPart("/tmp/clip.flac", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("want unsupported error, got %v", err)
	}
}

func TestLoadAudioPart_Missing(t *testing.T) {
	_, _, err := loadAudioPart("/no/such/clip.mp3", "")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadAudioPart_RelativePath(t *testing.T) {
	tmp := t.TempDir()
	writeBytes(t, filepath.Join(tmp, "a.wav"), []byte("RIFF"))
	if _, _, err := loadAudioPart("a.wav", tmp); err != nil {
		t.Fatalf("relative path with workDir failed: %v", err)
	}
}
