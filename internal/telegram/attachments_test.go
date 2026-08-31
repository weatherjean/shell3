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

func TestAttachmentNoteNamesNoTool(t *testing.T) {
	note := attachmentNote([]savedFile{
		{Name: "photo.jpg", MIME: "image/jpeg", Size: 84 * 1024, Path: "/m/tg-1.jpg"},
	})
	if strings.Contains(note, "read_media") {
		t.Error("note must not name a removed tool")
	}
	if strings.Contains(note, "skill") {
		t.Error("the harness must not guess at tools it cannot see; the skills index is already in the prompt")
	}
	for _, want := range []string{"photo.jpg", "image/jpeg", "/m/tg-1.jpg", "bash"} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q: %s", want, note)
		}
	}
}

func TestAttachmentNote_Empty(t *testing.T) {
	if attachmentNote(nil) != "" {
		t.Fatal("want empty note for no attachments")
	}
}

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
