//go:build unix

package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
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

// TestMediaMessage_AttachmentNoteReachesTurnPrompt is the end-to-end
// must-not-regress check for the media.stt removal (Task 4): a message
// carrying an attachment, with no text of its own, still gets its saved
// file's path injected into the prompt the model actually receives —
// composeText in bot.go's runUserTurn calls attachmentNote(mail.saved, ...)
// directly now that preflightText (and its STT half) is gone. Unit-testing
// attachmentNote alone (above) proves the function is correct; this proves
// the wiring that calls it from handleMsg is still connected.
func TestMediaMessage_AttachmentNoteReachesTurnPrompt(t *testing.T) {
	fc := newFakeClient()
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	rt := storeRuntimeClient(t, client)
	b := newBot(t, fc, rt)

	m := Msg{
		ChatID: 42, SenderID: 42, ID: "1",
		Media: []Media{{Bytes: []byte("\xff\xd8\xff"), MIME: "image/jpeg", Filename: "photo.jpg"}},
	}
	b.handleMsg(context.Background(), m)

	waitFor(t, func() bool { return client.CallCount() > 0 })
	calls := client.CallsSnapshot()
	last := calls[len(calls)-1]
	var prompt string
	for _, msg := range last.Msgs {
		if msg.Role == llm.RoleUser {
			prompt += msg.Content
		}
	}
	if !strings.Contains(prompt, "photo.jpg") || !strings.Contains(prompt, "[The user sent") {
		t.Fatalf("attachment note must reach the turn prompt, got %q", prompt)
	}
}
