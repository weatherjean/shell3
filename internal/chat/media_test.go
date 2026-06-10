package chat

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestHandleReadMedia_Image(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "shot.png")
	writePNG(t, p) // helper from image_test.go (4x4 png)

	out, part := handleReadMedia(`{"path":"`+p+`"}`, "")
	if part.Type != llm.ContentPartTypeImageURL {
		t.Fatalf("type = %q, want image_url", part.Type)
	}
	if !strings.HasPrefix(part.ImageURL, "data:image/jpeg;base64,") {
		t.Errorf("image url prefix wrong: %.30s", part.ImageURL)
	}
	if !strings.Contains(out, "Loaded image") {
		t.Errorf("result = %q", out)
	}
}

func TestHandleReadMedia_Audio(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "clip.mp3")
	writeBytes(t, p, []byte("fake mp3")) // helper from audio_test.go

	out, part := handleReadMedia(`{"path":"`+p+`"}`, "")
	if part.Type != llm.ContentPartTypeInputAudio {
		t.Fatalf("type = %q, want input_audio", part.Type)
	}
	if part.AudioFormat != "mp3" || part.AudioData == "" {
		t.Errorf("audio part wrong: format=%q dataEmpty=%v", part.AudioFormat, part.AudioData == "")
	}
	if !strings.Contains(out, "Loaded mp3 audio") {
		t.Errorf("result = %q", out)
	}
}

func TestHandleReadMedia_BadJSON(t *testing.T) {
	out, part := handleReadMedia(`{not json`, "")
	if part.Type != "" || !strings.HasPrefix(out, "error:") {
		t.Errorf("want error + zero part, got out=%q part=%+v", out, part)
	}
}

func TestHandleReadMedia_MissingPath(t *testing.T) {
	out, part := handleReadMedia(`{"path":"  "}`, "")
	if part.Type != "" || !strings.HasPrefix(out, "error:") {
		t.Errorf("want error + zero part, got out=%q", out)
	}
}

func TestHandleReadMedia_Unsupported(t *testing.T) {
	out, part := handleReadMedia(`{"path":"/tmp/x.bmp"}`, "")
	if part.Type != "" || !strings.Contains(out, "unsupported media type") {
		t.Errorf("want unsupported media error, got out=%q", out)
	}
}

func TestLoadMediaPart_RoutesByExt(t *testing.T) {
	tmp := t.TempDir()
	img := filepath.Join(tmp, "a.png")
	writePNG(t, img)
	aud := filepath.Join(tmp, "a.wav")
	writeBytes(t, aud, []byte("RIFF"))

	ip, _, err := LoadMediaPart(img, "")
	if err != nil || ip.Type != llm.ContentPartTypeImageURL {
		t.Fatalf("image route: type=%q err=%v", ip.Type, err)
	}
	ap, _, err := LoadMediaPart(aud, "")
	if err != nil || ap.Type != llm.ContentPartTypeInputAudio {
		t.Fatalf("audio route: type=%q err=%v", ap.Type, err)
	}
	if _, _, err := LoadMediaPart("/tmp/x.bmp", ""); err == nil {
		t.Error("want error for unsupported ext")
	}
}
