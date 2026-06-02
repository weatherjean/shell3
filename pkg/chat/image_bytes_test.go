package chat

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/llm"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBuildImageMessageFromBytes_OK(t *testing.T) {
	msg, err := BuildImageMessageFromBytes(tinyPNG(t), "")
	if err != nil {
		t.Fatalf("BuildImageMessageFromBytes: %v", err)
	}
	if len(msg.ContentParts) != 2 {
		t.Fatalf("parts = %d, want 2", len(msg.ContentParts))
	}
	if msg.ContentParts[0].Type != llm.ContentPartTypeImageURL ||
		!strings.HasPrefix(msg.ContentParts[0].ImageURL, "data:image/jpeg;base64,") {
		t.Errorf("image part wrong: %+v", msg.ContentParts[0])
	}
	if msg.ContentParts[1].Type != llm.ContentPartTypeText || msg.ContentParts[1].Text != "Describe this image." {
		t.Errorf("text part wrong: %+v", msg.ContentParts[1])
	}
}

func TestBuildImageMessageFromBytes_Oversize(t *testing.T) {
	if _, err := BuildImageMessageFromBytes(make([]byte, maxImageBytes+1), "x"); err == nil {
		t.Fatal("expected error for oversize image")
	}
}

func TestBuildImageMessageFromBytes_Undecodable(t *testing.T) {
	if _, err := BuildImageMessageFromBytes([]byte("not an image"), "x"); err == nil {
		t.Fatal("expected error for undecodable bytes")
	}
}
