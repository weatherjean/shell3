package chat

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestLoadVideoPart_ExtRouting(t *testing.T) {
	tmp := t.TempDir()
	cases := map[string]string{
		"clip.mp4":  "video/mp4",
		"clip.webm": "video/webm",
		"clip.mov":  "video/quicktime",
	}
	for name, mime := range cases {
		p := filepath.Join(tmp, name)
		writeBytes(t, p, []byte("fake video bytes"))

		part, desc, err := loadVideoPart(p, "")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if part.Type != llm.ContentPartTypeVideoURL {
			t.Errorf("%s: type = %q, want video_url", name, part.Type)
		}
		want := "data:" + mime + ";base64,"
		if !strings.HasPrefix(part.VideoURL, want) {
			t.Errorf("%s: video url prefix = %.40s, want prefix %q", name, part.VideoURL, want)
		}
		if !strings.Contains(desc, mime) {
			t.Errorf("%s: desc = %q", name, desc)
		}
	}
}

func TestLoadVideoPart_Unsupported(t *testing.T) {
	_, _, err := loadVideoPart("/tmp/clip.avi", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("want unsupported error, got %v", err)
	}
}

func TestLoadVideoPart_Missing(t *testing.T) {
	if _, _, err := loadVideoPart("/no/such/clip.mp4", ""); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestVideoPartFromBytes_TooLarge(t *testing.T) {
	if _, _, err := videoPartFromBytes(make([]byte, maxVideoBytes+1), "video/mp4"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("video cap not enforced: %v", err)
	}
}

func TestVideoPartFromBytes_Empty(t *testing.T) {
	if _, _, err := videoPartFromBytes([]byte{}, "video/mp4"); err == nil || !strings.Contains(err.Error(), "empty video") {
		t.Fatalf("want empty-video error, got %v", err)
	}
}
