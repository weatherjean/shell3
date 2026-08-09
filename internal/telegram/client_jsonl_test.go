//go:build unix

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decodeLines parses every non-empty JSONL line written so far.
func decodeLines(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("malformed output line %q: %v", ln, err)
		}
		evs = append(evs, m)
	}
	return evs
}

func newTestJSONL(t *testing.T, in string) (*JSONLClient, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return NewJSONLClient(strings.NewReader(in), out, ConsoleChatID, t.TempDir()), out
}

func TestJSONLInboundMessage(t *testing.T) {
	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "pic.jpg")
	if err := os.WriteFile(mediaPath, []byte("jpegbytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := `{"type":"message","id":"m1","reply_to_id":"a9","text":"hi","reply_to":"quoted","media":[{"path":"` + mediaPath + `","mime":"image/jpeg","filename":"pic.jpg"}]}` + "\n" +
		`not json at all` + "\n" + // ignored
		`{"type":"wat"}` + "\n" + // unknown type ignored
		`{"type":"message","text":"no id"}` + "\n"
	c, _ := newTestJSONL(t, in)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := c.Updates(ctx)

	m1 := <-ch
	if m1.ID != "m1" || m1.ReplyToID != "a9" || m1.Text != "hi" || m1.ReplyTo != "quoted" || m1.ChatID != ConsoleChatID {
		t.Fatalf("m1 = %+v", m1)
	}
	if len(m1.Media) != 1 || string(m1.Media[0].Bytes) != "jpegbytes" ||
		m1.Media[0].MIME != "image/jpeg" || m1.Media[0].Filename != "pic.jpg" {
		t.Fatalf("media = %+v", m1.Media)
	}

	m2 := <-ch
	if m2.Text != "no id" || m2.ID == "" {
		t.Fatalf("empty inbound id must be assigned, got %+v", m2)
	}

	// EOF closes the channel.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after EOF")
	}
}

func TestJSONLInboundMediaUnreadableSkipped(t *testing.T) {
	in := `{"type":"message","id":"m1","text":"x","media":[{"path":"/nonexistent/nope.png","mime":"image/png","filename":"nope.png"}]}` + "\n"
	c, _ := newTestJSONL(t, in)
	m := <-c.Updates(context.Background())
	if len(m.Media) != 0 {
		t.Fatalf("unreadable media must be skipped, got %+v", m.Media)
	}
}

func TestJSONLCallback(t *testing.T) {
	in := `{"type":"callback","id":"cb1","data":"allow-x"}` + "\n"
	c, _ := newTestJSONL(t, in)
	c.Updates(context.Background()) // starts the read loop
	select {
	case cb := <-c.Callbacks(context.Background()):
		if cb.ID != "cb1" || cb.Data != "allow-x" || cb.ChatID != ConsoleChatID {
			t.Fatalf("callback = %+v", cb)
		}
	case <-time.After(time.Second):
		t.Fatal("no callback delivered")
	}
}

func TestJSONLSendEvents(t *testing.T) {
	c, out := newTestJSONL(t, "")
	ctx := context.Background()

	id1, err := c.Send(ctx, ConsoleChatID, "hello **md**")
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := c.SendReply(ctx, ConsoleChatID, "threaded", "m1")
	if id1 == "" || id2 == "" || id1 == id2 {
		t.Fatalf("send ids must be distinct and non-empty: %q %q", id1, id2)
	}

	evs := decodeLines(t, out)
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d: %v", len(evs), evs)
	}
	if evs[0]["type"] != "send" || evs[0]["id"] != id1 || evs[0]["text"] != "hello **md**" {
		t.Fatalf("send event = %v", evs[0])
	}
	if evs[1]["type"] != "send" || evs[1]["reply_to_id"] != "m1" || evs[1]["text"] != "threaded" {
		t.Fatalf("reply event = %v", evs[1])
	}
	if _, present := evs[0]["reply_to_id"]; present {
		t.Fatalf("unthreaded send must omit reply_to_id: %v", evs[0])
	}
}

func TestJSONLNoHTMLOnTheWire(t *testing.T) {
	c, out := newTestJSONL(t, "")
	ctx := context.Background()
	if _, err := c.SendHTML(ctx, ConsoleChatID, "<b>x</b>"); !errors.Is(err, ErrNoHTML) {
		t.Fatalf("SendHTML err = %v, want ErrNoHTML", err)
	}
	if _, err := c.SendHTMLReply(ctx, ConsoleChatID, "<b>x</b>", "m1"); !errors.Is(err, ErrNoHTML) {
		t.Fatalf("SendHTMLReply err = %v, want ErrNoHTML", err)
	}
	if out.Len() != 0 {
		t.Fatalf("HTML sends must emit nothing, got %q", out.String())
	}
}

func TestJSONLDocumentSpools(t *testing.T) {
	spool := t.TempDir()
	out := &bytes.Buffer{}
	c := NewJSONLClient(strings.NewReader(""), out, ConsoleChatID, spool)
	id, err := c.SendDocument(context.Background(), ConsoleChatID, "reply.md", []byte("# full"), "full reply")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("document send must return an id")
	}
	evs := decodeLines(t, out)
	if len(evs) != 1 || evs[0]["type"] != "media" || evs[0]["kind"] != "document" ||
		evs[0]["filename"] != "reply.md" || evs[0]["caption"] != "full reply" || evs[0]["id"] != id {
		t.Fatalf("media event = %v", evs)
	}
	path, _ := evs[0]["path"].(string)
	if !strings.HasPrefix(path, spool) {
		t.Fatalf("spooled path %q not under %q", path, spool)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "# full" {
		t.Fatalf("spooled content = %q, %v", data, err)
	}
}

func TestJSONLMediaKinds(t *testing.T) {
	c, out := newTestJSONL(t, "")
	ctx := context.Background()
	_ = c.SendPhoto(ctx, ConsoleChatID, "cat.png", []byte("p"), "a cat")
	_ = c.SendVoice(ctx, ConsoleChatID, []byte("v"), "spoken")
	_ = c.SendAudio(ctx, ConsoleChatID, "song.mp3", []byte("a"), "")
	_ = c.SendVideo(ctx, ConsoleChatID, "clip.mp4", []byte("v"), "")
	evs := decodeLines(t, out)
	kinds := []string{"photo", "voice", "audio", "video"}
	if len(evs) != len(kinds) {
		t.Fatalf("want %d media events, got %v", len(kinds), evs)
	}
	for i, k := range kinds {
		if evs[i]["type"] != "media" || evs[i]["kind"] != k {
			t.Fatalf("event %d = %v, want kind %s", i, evs[i], k)
		}
		if p, _ := evs[i]["path"].(string); p == "" {
			t.Fatalf("event %d has no path: %v", i, evs[i])
		}
	}
}

func TestJSONLHello(t *testing.T) {
	c, out := newTestJSONL(t, "")
	c.EmitHello([]Command{{Command: "status", Description: "show status"}})
	evs := decodeLines(t, out)
	if len(evs) != 1 || evs[0]["type"] != "hello" || evs[0]["protocol"] != float64(1) {
		t.Fatalf("hello = %v", evs)
	}
	cmds, _ := evs[0]["commands"].([]any)
	if len(cmds) != 1 || cmds[0].(map[string]any)["command"] != "status" {
		t.Fatalf("hello commands = %v", evs[0]["commands"])
	}
}
