package chat

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// makePNG encodes a 4x4 red image to a PNG byte slice.
func makePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writePNG(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, makePNG(t), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildImageMessage_NoArgs(t *testing.T) {
	_, err := BuildImageMessage("", "")
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected usage error, got %v", err)
	}
}

func TestBuildImageMessage_MissingFile(t *testing.T) {
	_, err := BuildImageMessage("/nonexistent/file.png describe it", "")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestBuildImageMessage_UnsupportedType(t *testing.T) {
	_, err := BuildImageMessage("/tmp/file.bmp describe it", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported type error, got %v", err)
	}
}

func TestBuildImageMessage_UnquotedNoSpaces(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath)

	msg, err := BuildImageMessage(imgPath+" what do you see?", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != llm.RoleUser {
		t.Errorf("role = %s, want user", msg.Role)
	}
	if len(msg.ContentParts) != 2 {
		t.Fatalf("want 2 content parts, got %d", len(msg.ContentParts))
	}
	if msg.ContentParts[0].Type != "image_url" {
		t.Errorf("part[0].Type = %s, want image_url", msg.ContentParts[0].Type)
	}
	if !strings.HasPrefix(msg.ContentParts[0].ImageURL, "data:image/jpeg;base64,") {
		t.Errorf("image URL should be jpeg data URI, got prefix: %.30s", msg.ContentParts[0].ImageURL)
	}
	if msg.ContentParts[1].Type != "text" || msg.ContentParts[1].Text != "what do you see?" {
		t.Errorf("text part wrong: %+v", msg.ContentParts[1])
	}
}

func TestBuildImageMessage_QuotedPathWithSpaces(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "Screenshot 2026-04-28 at 12.43.18.png")
	writePNG(t, imgPath)

	msg, err := BuildImageMessage(`"`+imgPath+`" what is wrong?`, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.ContentParts[1].Text != "what is wrong?" {
		t.Errorf("prompt = %q", msg.ContentParts[1].Text)
	}
}

func TestBuildImageMessage_QuotedPathBackslashEscapedSpaces(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "Screenshot 2026-04-28 at 13.24.39.png")
	writePNG(t, imgPath)

	escaped := strings.ReplaceAll(imgPath, " ", `\ `)
	_, err := BuildImageMessage(`"`+escaped+`" not much changed`, "")
	if err != nil {
		t.Fatalf("quoted path with backslash-escaped spaces failed: %v", err)
	}
}

func TestBuildImageMessage_DefaultPrompt(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath)

	msg, err := BuildImageMessage(imgPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.ContentParts[1].Text != "Describe this image." {
		t.Errorf("default prompt = %q", msg.ContentParts[1].Text)
	}
}

func TestBuildImageMessage_RelativePath(t *testing.T) {
	tmp := t.TempDir()
	writePNG(t, filepath.Join(tmp, "img.png"))

	_, err := BuildImageMessage("img.png describe it", tmp)
	if err != nil {
		t.Fatalf("relative path with workDir failed: %v", err)
	}
}

func TestBuildImageMessage_LargeImageResized(t *testing.T) {
	// Build a 2000x1500 image — longest side > 1000, should be resized.
	img := image.NewRGBA(image.Rect(0, 0, 2000, 1500))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "big.png")
	if err := os.WriteFile(imgPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	msg, err := BuildImageMessage(imgPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Just verify it round-trips — actual pixel check would require decode.
	if !strings.HasPrefix(msg.ContentParts[0].ImageURL, "data:image/jpeg;base64,") {
		t.Errorf("expected jpeg data URI")
	}
}

func TestHandleReadImage_Success(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath)

	out, part := handleReadImage(`{"path":"`+imgPath+`"}`, "")
	if part.Type != llm.ContentPartTypeImageURL {
		t.Fatalf("part type = %q, want image_url", part.Type)
	}
	if !strings.HasPrefix(part.ImageURL, "data:image/jpeg;base64,") {
		t.Errorf("image URL prefix wrong: %.30s", part.ImageURL)
	}
	if !strings.Contains(out, "Loaded image") {
		t.Errorf("result text = %q, want it to mention loading", out)
	}
}

func TestHandleReadImage_BadJSON(t *testing.T) {
	out, part := handleReadImage(`{not json`, "")
	if part.ImageURL != "" {
		t.Error("expected zero part on bad json")
	}
	if !strings.HasPrefix(out, "error:") {
		t.Errorf("want error string, got %q", out)
	}
}

func TestHandleReadImage_MissingPath(t *testing.T) {
	out, part := handleReadImage(`{"path":"  "}`, "")
	if part.ImageURL != "" || !strings.HasPrefix(out, "error:") {
		t.Errorf("want error + zero part, got out=%q part=%+v", out, part)
	}
}

func TestHandleReadImage_Unsupported(t *testing.T) {
	out, part := handleReadImage(`{"path":"/tmp/x.bmp"}`, "")
	if part.ImageURL != "" || !strings.Contains(out, "unsupported") {
		t.Errorf("want unsupported error + zero part, got out=%q", out)
	}
}

func TestLoadImagePart_ReturnsDimensions(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath) // makePNG is 4x4
	part, w, h, err := loadImagePart(imgPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 4 || h != 4 {
		t.Errorf("dims = %dx%d, want 4x4", w, h)
	}
	if part.Type != llm.ContentPartTypeImageURL {
		t.Errorf("part type = %q", part.Type)
	}
}
