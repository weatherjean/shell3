//go:build unix

package telegram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func hasTool(sess *shell3.Session, name string) bool {
	if sess == nil {
		return false
	}
	for _, t := range sess.Snapshot().Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func TestTelegramToolRegisteredAndSends(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	sess := decoratedSession(t, b, rt)

	if !hasTool(sess, "telegram") {
		t.Fatal("telegram should be registered in the schema")
	}

	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), sess, `{"path":"report.txt","caption":"here"}`)
	if !strings.Contains(out, "sent report.txt") {
		t.Fatalf("unexpected result: %q", out)
	}
	doc, ok := fc.lastDoc()
	if !ok || doc.filename != "report.txt" || string(doc.data) != "hello" || doc.caption != "here" {
		t.Fatalf("document not sent correctly: %+v ok=%v", doc, ok)
	}
}

func TestOrchestratorDecoratorRegistersOnlyTelegramTransportTool(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	sess, err := rt.Session(shell3.SessionOpts{Name: "orchestrator-tool"})
	if err != nil {
		t.Fatal(err)
	}
	b.DecorateOrchestratorSession(sess)
	var names []string
	for _, tool := range sess.Snapshot().Tools {
		names = append(names, tool.Name)
	}
	if got, want := strings.Join(names, ","), "telegram"; got != want {
		t.Fatalf("tools = %q, want %q", got, want)
	}
}

func TestSendMediaTool_KindOmittedSendsDocument(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	sess := decoratedSession(t, b, rt)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), sess, `{"path":"report.txt"}`)
	if !strings.Contains(out, "sent report.txt") {
		t.Fatalf("unexpected result: %q", out)
	}
	if _, ok := fc.lastDoc(); !ok {
		t.Fatal("expected a document to be sent when kind is omitted")
	}
}

func TestSendMediaToolKinds(t *testing.T) {
	cases := []struct {
		name, file, kind, want string
		sent                   func(*fakeClient) int
	}{
		{"photo", "chart.png", "photo", "sent chart.png", func(c *fakeClient) int { return len(c.photos) }},
		{"photo rejects text", "notes.txt", "photo", "error: kind=photo requires an image file (jpg, jpeg, png, gif, webp)", func(c *fakeClient) int { return len(c.photos) }},
		{"voice", "reply.ogg", "voice", "sent reply.ogg", func(c *fakeClient) int { return len(c.voices) }},
		{"voice rejects mp3", "song.mp3", "voice", "error: kind=voice requires an .ogg/.opus file — use kind=audio for mp3", func(c *fakeClient) int { return len(c.voices) }},
		{"audio", "song.mp3", "audio", "sent song.mp3", func(c *fakeClient) int { return len(c.audios) }},
		{"video", "clip.mp4", "video", "sent clip.mp4", func(c *fakeClient) int { return len(c.videos) }},
		{"video rejects text", "notes.txt", "video", "error: kind=video requires an .mp4/.webm/.mov file", func(c *fakeClient) int { return len(c.videos) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFakeClient()
			rt, _ := newFakeRuntime(t, "ok")
			b := newBot(t, fc, rt)
			sess := decoratedSession(t, b, rt)
			dir := t.TempDir()
			b.SetWorkDir(dir)
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte("data"), 0o644); err != nil {
				t.Fatal(err)
			}
			args := fmt.Sprintf(`{"path":%q,"kind":%q}`, tc.file, tc.kind)
			out, _ := b.sendMediaHandler(context.Background(), sess, args)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("result = %q, want it to contain %q", out, tc.want)
			}
			wantSent := 1
			if strings.HasPrefix(tc.want, "error:") {
				wantSent = 0
			}
			if got := tc.sent(fc); got != wantSent {
				t.Fatalf("sent %d, want %d", got, wantSent)
			}
		})
	}
}

func TestSendMediaTool_KindUnknownReturnsEnumError(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	sess := decoratedSession(t, b, rt)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), sess, `{"path":"report.txt","kind":"banana"}`)
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
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	sess := decoratedSession(t, b, rt)
	dir := t.TempDir()
	b.SetWorkDir(dir)
	for _, name := range []string{".env", ".env.production"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("SECRET=x"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, _ := b.sendMediaHandler(context.Background(), sess, `{"path":"`+name+`"}`)
		if !strings.Contains(out, "refusing") {
			t.Fatalf("expected refusal for %s, got %q", name, out)
		}
		if _, ok := fc.lastDoc(); ok {
			t.Fatalf("%s must not be sent", name)
		}
	}
}

func TestSendMediaRendersMarkdownDocuments(t *testing.T) {
	for _, name := range []string{"plan.md", "PLAN.MARKDOWN"} {
		gotName, gotData := renderMarkdownDoc(name, []byte("# Plan\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"))
		if filepath.Ext(gotName) != ".html" {
			t.Fatalf("%s: sent as %q, want an .html page", name, gotName)
		}
		if !strings.Contains(string(gotData), "<td>1</td>") {
			t.Fatalf("%s: page did not render the table:\n%s", name, gotData)
		}
	}
}

func TestSendMediaLeavesOtherDocumentsAlone(t *testing.T) {
	for _, name := range []string{"notes.txt", "data.csv", "report.pdf", "archive.zip"} {
		gotName, gotData := renderMarkdownDoc(name, []byte("# not markdown here"))
		if gotName != name || string(gotData) != "# not markdown here" {
			t.Fatalf("%s was rewritten to %q", name, gotName)
		}
	}
}
