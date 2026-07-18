package chat

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// minimalWebP is a valid 1x1 lossless (VP8L) WebP image.
const minimalWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func webpBytes(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(minimalWebP)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// read_media advertises webp (tool description, ext allowlist, MIME switch),
// so the decoder must actually be registered — a .webp reaching image.Decode
// without it fails with "image: unknown format".
func TestLoadImagePart_WebP(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "pic.webp")
	if err := os.WriteFile(p, webpBytes(t), 0644); err != nil {
		t.Fatal(err)
	}
	part, desc, err := loadImagePart(p, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "image 1x1" {
		t.Errorf("desc = %q, want %q", desc, "image 1x1")
	}
	if part.Type != llm.ContentPartTypeImageURL {
		t.Errorf("part type = %q", part.Type)
	}
}

func TestMediaPartFromBytes_WebP(t *testing.T) {
	part, desc, err := MediaPartFromBytes(webpBytes(t), "image/webp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.Type != llm.ContentPartTypeImageURL {
		t.Errorf("part type = %q", part.Type)
	}
	if desc != "image 1x1" {
		t.Errorf("desc = %q, want %q", desc, "image 1x1")
	}
}
