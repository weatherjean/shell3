//go:build unix

package telegram

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestMediaToParts_ImageAndAudio(t *testing.T) {
	in := []Media{
		{Bytes: []byte("\xff\xd8\xff"), MIME: "image/jpeg"},
		{Bytes: []byte("RIFF"), MIME: "audio/wav"},
	}
	parts := mediaToParts(in)
	if len(parts) != 2 {
		t.Fatalf("want 2 parts, got %d", len(parts))
	}
	if parts[0].Kind != shell3.PartImage || parts[0].MIME != "image/jpeg" {
		t.Fatalf("bad image part: %+v", parts[0])
	}
	if parts[1].Kind != shell3.PartAudio {
		t.Fatalf("bad audio part: %+v", parts[1])
	}
}

func TestMediaToParts_VoiceOggAccepted(t *testing.T) {
	// Telegram voice note: OGG/Opus, sometimes with a charset-style suffix.
	parts := mediaToParts([]Media{{Bytes: []byte("OggS"), MIME: "audio/ogg; codecs=opus"}})
	if len(parts) != 1 || parts[0].Kind != shell3.PartAudio || parts[0].MIME != "audio/ogg" {
		t.Fatalf("want 1 audio part (audio/ogg), got %+v", parts)
	}
}

func TestMediaToParts_UnsupportedDropped(t *testing.T) {
	parts := mediaToParts([]Media{{Bytes: []byte("x"), MIME: "application/pdf"}})
	if len(parts) != 0 {
		t.Fatalf("want 0 parts for unsupported mime, got %d", len(parts))
	}
}
