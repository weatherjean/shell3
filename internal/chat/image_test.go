package chat

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

func TestLoadImagePart_ReturnsDimensions(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath) // makePNG is 4x4
	part, desc, err := loadImagePart(imgPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "image 4x4" {
		t.Errorf("desc = %q, want %q", desc, "image 4x4")
	}
	if part.Type != llm.ContentPartTypeImageURL {
		t.Errorf("part type = %q", part.Type)
	}
}
