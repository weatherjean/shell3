//go:build unix

package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendMediaTool_RegisteredAndSends(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)

	if !b.hasTool("send_media_telegram") {
		t.Fatal("send_media_telegram should be registered in the schema")
	}

	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"report.txt","caption":"here"}`)
	if !strings.Contains(out, "sent report.txt") {
		t.Fatalf("unexpected result: %q", out)
	}
	doc, ok := fc.lastDoc()
	if !ok || doc.filename != "report.txt" || string(doc.data) != "hello" || doc.caption != "here" {
		t.Fatalf("document not sent correctly: %+v ok=%v", doc, ok)
	}
}

func TestSendMediaTool_KindOmittedSendsDocument(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"report.txt"}`)
	if !strings.Contains(out, "sent report.txt") {
		t.Fatalf("unexpected result: %q", out)
	}
	if _, ok := fc.lastDoc(); !ok {
		t.Fatal("expected a document to be sent when kind is omitted")
	}
}

func TestSendMediaTool_KindPhotoSendsPhoto(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "chart.png"), []byte("pngdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"chart.png","kind":"photo"}`)
	if !strings.Contains(out, "sent chart.png") {
		t.Fatalf("unexpected result: %q", out)
	}
	if len(fc.photos) != 1 {
		t.Fatalf("expected one photo sent, got %d", len(fc.photos))
	}
}

func TestSendMediaTool_KindPhotoRejectsNonImage(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"notes.txt","kind":"photo"}`)
	want := "error: kind=photo requires an image file (jpg, jpeg, png, gif, webp)"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if len(fc.photos) != 0 {
		t.Fatal("no photo should have been sent")
	}
}

func TestSendMediaTool_KindVoiceSendsVoice(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reply.ogg"), []byte("oggdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"reply.ogg","kind":"voice"}`)
	if !strings.Contains(out, "sent reply.ogg") {
		t.Fatalf("unexpected result: %q", out)
	}
	if len(fc.voices) != 1 {
		t.Fatalf("expected one voice sent, got %d", len(fc.voices))
	}
}

func TestSendMediaTool_KindVoiceRejectsMp3(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), []byte("mp3data"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"song.mp3","kind":"voice"}`)
	want := "error: kind=voice requires an .ogg/.opus file — use kind=audio for mp3"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if len(fc.voices) != 0 {
		t.Fatal("no voice should have been sent")
	}
}

func TestSendMediaTool_KindAudioSendsAudio(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), []byte("mp3data"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"song.mp3","kind":"audio"}`)
	if !strings.Contains(out, "sent song.mp3") {
		t.Fatalf("unexpected result: %q", out)
	}
	if len(fc.audios) != 1 {
		t.Fatalf("expected one audio sent, got %d", len(fc.audios))
	}
}

func TestSendMediaTool_KindVideoSendsVideo(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("mp4data"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"clip.mp4","kind":"video"}`)
	if !strings.Contains(out, "sent clip.mp4") {
		t.Fatalf("unexpected result: %q", out)
	}
	if len(fc.videos) != 1 {
		t.Fatalf("expected one video sent, got %d", len(fc.videos))
	}
}

func TestSendMediaTool_KindVideoRejectsNonVideo(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"notes.txt","kind":"video"}`)
	want := "error: kind=video requires an .mp4/.webm/.mov file"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if len(fc.videos) != 0 {
		t.Fatal("no video should have been sent")
	}
}

func TestSendMediaTool_KindUnknownReturnsEnumError(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"report.txt","kind":"banana"}`)
	want := "error: kind must be photo, voice, audio, video, or document"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestValidateKind(t *testing.T) {
	const tenMB = 10 << 20
	const fiftyMB = 50 << 20
	cases := []struct {
		name    string
		kind    string
		ext     string
		size    int64
		wantErr string
	}{
		{"document any ext", "document", ".exe", 1, ""},
		{"photo png ok", "photo", ".png", tenMB, ""},
		{"photo jpg ok", "photo", ".jpg", 100, ""},
		{"photo too large", "photo", ".png", tenMB + 1, "error: kind=photo requires an image file under 10 MB"},
		{"photo wrong ext", "photo", ".txt", 100, "error: kind=photo requires an image file (jpg, jpeg, png, gif, webp)"},
		{"voice ogg ok", "voice", ".ogg", fiftyMB, ""},
		{"voice opus ok", "voice", ".opus", 100, ""},
		{"voice mp3 rejected", "voice", ".mp3", 100, "error: kind=voice requires an .ogg/.opus file — use kind=audio for mp3"},
		{"audio mp3 ok", "audio", ".mp3", 100, ""},
		{"audio wrong ext", "audio", ".txt", 100, "error: kind=audio requires an audio file (mp3, m4a, ogg, opus, wav)"},
		{"video mp4 ok", "video", ".mp4", fiftyMB, ""},
		{"video webm ok", "video", ".webm", 100, ""},
		{"video mov ok", "video", ".mov", 100, ""},
		{"video wrong ext", "video", ".txt", 100, "error: kind=video requires an .mp4/.webm/.mov file"},
		{"unknown kind", "banana", ".png", 100, "error: kind must be photo, voice, audio, video, or document"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateKind(c.kind, c.ext, c.size)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != c.wantErr {
				t.Fatalf("got %v, want %q", err, c.wantErr)
			}
		})
	}
}

func TestSendMediaTool_RefusesEnv(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":".env"}`)
	if !strings.Contains(out, "refusing") {
		t.Fatalf("expected refusal for .env, got %q", out)
	}
	if _, ok := fc.lastDoc(); ok {
		t.Fatal(".env must not be sent")
	}
}
