//go:build unix

package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAttachmentsUsesMediaDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", dir)
	saved := saveAttachments([]Media{{Bytes: []byte("x"), MIME: "image/png", Filename: "a.png"}})
	if len(saved) != 1 {
		t.Fatalf("want 1 saved, got %d", len(saved))
	}
	t.Cleanup(func() { _ = os.Remove(saved[0].Path) })
	if filepath.Dir(saved[0].Path) != dir {
		t.Fatalf("saved to %q, want under media dir %q", saved[0].Path, dir)
	}
}

func TestSaveAttachments_WritesFiles(t *testing.T) {
	saved := saveAttachments([]Media{
		{Bytes: []byte("OggS-fake"), MIME: "audio/ogg", Filename: "voice.ogg"},
		{Bytes: []byte("%PDF-1.4"), MIME: "application/pdf", Filename: "doc.pdf"},
	})
	if len(saved) != 2 {
		t.Fatalf("want 2 saved files, got %d", len(saved))
	}
	for _, s := range saved {
		t.Cleanup(func() { _ = os.Remove(s.Path) })
		b, err := os.ReadFile(s.Path)
		if err != nil || len(b) == 0 {
			t.Fatalf("file %s not written: %v", s.Path, err)
		}
	}
}

func TestAttachmentNote_ToolGating(t *testing.T) {
	saved := saveAttachments([]Media{{Bytes: []byte("x"), MIME: "image/jpeg", Filename: "photo.jpg"}})
	t.Cleanup(func() {
		for _, s := range saved {
			_ = os.Remove(s.Path)
		}
	})

	// read_media enabled → mention it + include the path.
	on := attachmentNote(saved, true)
	if !strings.Contains(on, "read_media") || !strings.Contains(on, saved[0].Path) {
		t.Fatalf("note should name read_media and the path: %q", on)
	}
	// read_media disabled → must NOT mention it; should suggest bash.
	off := attachmentNote(saved, false)
	if strings.Contains(off, "read_media") {
		t.Fatalf("note must not mention read_media when disabled: %q", off)
	}
	if !strings.Contains(off, "bash") {
		t.Fatalf("note should suggest bash when read_media is off: %q", off)
	}
}

func TestAttachmentNote_Empty(t *testing.T) {
	if attachmentNote(nil, true) != "" {
		t.Fatal("want empty note for no attachments")
	}
}
