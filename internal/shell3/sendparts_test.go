package shell3

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// pngBytes duplicates internal/chat/media_bytes_test.go's helper of the same
// name (test helpers can't be shared across packages without exporting).
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestSendParts_ThreadsContentParts: byte-backed image and audio parts reach
// the provider as [text, image data URI, base64 audio] on the user message.
func TestSendParts_ThreadsContentParts(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "seen"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	parts := []Part{
		{Kind: PartImage, Data: pngBytes(t, 2, 2), MIME: "image/png"},
		{Kind: PartAudio, Data: []byte("RIFF-fake"), MIME: "audio/mpeg"},
	}
	for ev := range s.SendParts(context.Background(), "describe these", parts) {
		if ev.Kind == Error {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	msgs := client.CallsSnapshot()[0].Msgs
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || last.Content != "describe these" || len(last.ContentParts) != 3 {
		t.Fatalf("user message = %+v", last)
	}
	if last.ContentParts[0].Text != "describe these" ||
		!strings.HasPrefix(last.ContentParts[1].ImageURL, "data:image/jpeg;base64,") ||
		last.ContentParts[2].AudioFormat != "mp3" ||
		last.ContentParts[2].AudioData != base64.StdEncoding.EncodeToString([]byte("RIFF-fake")) {
		t.Fatalf("ContentParts = %+v", last.ContentParts)
	}
}

// TestSendParts_PathPart: a Path-backed part loads from disk relative to the
// session workdir (extension-routed; MIME ignored).
func TestSendParts_PathPart(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "shot.png"), pngBytes(t, 2, 2), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestSession(t, client, chat.Config{WorkDir: workdir})
	defer s.Close()

	for ev := range s.SendParts(context.Background(), "look", []Part{{Kind: PartImage, Path: "shot.png"}}) {
		if ev.Kind == Error {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	msgs := client.CallsSnapshot()[0].Msgs
	last := msgs[len(msgs)-1]
	if len(last.ContentParts) != 2 || !strings.HasPrefix(last.ContentParts[1].ImageURL, "data:image/jpeg;base64,") {
		t.Fatalf("path part not loaded: %+v", last.ContentParts)
	}
}

// TestSendParts_InvalidPartErrorsAndStaysUsable: every Part-contract violation
// yields exactly one Error event (no turn starts), and the session stays
// usable afterwards.
func TestSendParts_InvalidPartErrorsAndStaysUsable(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	cases := []Part{
		{Kind: PartImage},                    // neither Path nor Data
		{Kind: PartImage, Data: []byte("x")}, // Data without MIME
		{Kind: PartImage, Path: "a.png", Data: []byte("x"), MIME: "image/png"}, // both set
		{Kind: PartImage, Data: []byte("x"), MIME: "video/mp4"},                // unsupported MIME
		{Kind: PartAudio, Data: pngBytes(t, 1, 1), MIME: "image/png"},          // kind/content mismatch
		{Kind: PartKind(99), Data: []byte("x"), MIME: "audio/wav"},             // unknown kind
	}
	for i, p := range cases {
		var evs []Event
		for ev := range s.SendParts(context.Background(), "x", []Part{p}) {
			evs = append(evs, ev)
		}
		if len(evs) != 1 || evs[0].Kind != Error || evs[0].Err == nil {
			t.Fatalf("case %d: want single Error event, got %+v", i, evs)
		}
		// The package prefix must be outermost, exactly once:
		// "shell3: part 0: <reason>", never "part 0: shell3: <reason>".
		msg := evs[0].Err.Error()
		if !strings.HasPrefix(msg, "shell3: part 0: ") {
			t.Fatalf("case %d: error %q must start with %q", i, msg, "shell3: part 0: ")
		}
		if strings.Contains(strings.TrimPrefix(msg, "shell3: "), "shell3:") {
			t.Fatalf("case %d: error %q nests the shell3: prefix", i, msg)
		}
	}
	for ev := range s.SendParts(context.Background(), "x", []Part{{Kind: PartImage}}) {
		if got, want := ev.Err.Error(), "shell3: part 0: part sets neither Path nor Data"; got != want {
			t.Fatalf("rendered error = %q, want %q", got, want)
		}
	}
	if client.CallCount() != 0 {
		t.Fatalf("no turn should have started; provider saw %d calls", client.CallCount())
	}
	var done bool
	for ev := range s.Send(context.Background(), "hello") {
		if ev.Kind == Done {
			done = true
		}
	}
	if !done {
		t.Fatal("Send after rejected SendParts must complete normally")
	}
}

// TestSendParts_BusyRejected: SendParts honors the same ErrBusy contract as
// Send (mirrors TestSession_BusyEnforcement's blockingClient pattern).
func TestSendParts_BusyRejected(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}), returned: make(chan struct{})}
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	out := s.Send(ctx, "hi")
	<-client.entered

	var rejected []Event
	for ev := range s.SendParts(context.Background(), "overlap", []Part{{Kind: PartImage, Data: pngBytes(t, 1, 1), MIME: "image/png"}}) {
		rejected = append(rejected, ev)
	}
	if len(rejected) != 1 || rejected[0].Kind != Error || !errors.Is(rejected[0].Err, ErrBusy) {
		t.Fatalf("SendParts while busy: want one ErrBusy Error event, got %+v", rejected)
	}
	cancel()
	for range out {
	}
}

// TestInterject_PartsReachProvider: a valid part interjected while idle rides
// the next turn as the trailing media user message.
func TestInterject_PartsReachProvider(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	s.Interject("photo incoming", Part{Kind: PartImage, Data: pngBytes(t, 2, 2), MIME: "image/png"})
	for range s.Send(context.Background(), "hi") {
	}
	msgs := client.CallsSnapshot()[0].Msgs
	last := msgs[len(msgs)-1]
	found := false
	for _, p := range last.ContentParts {
		if strings.HasPrefix(p.ImageURL, "data:image/jpeg;base64,") {
			found = true
		}
	}
	if last.Role != llm.RoleUser || !found {
		t.Fatalf("interjected part missing from final user message: %+v", last)
	}
}

// TestInterject_InvalidPartDroppedWithNote: Interject never fails — a bad part
// is dropped and a bracketed note is appended to the queued steering text.
func TestInterject_InvalidPartDroppedWithNote(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	s.Interject("here you go", Part{Kind: PartImage, Data: []byte("not an image"), MIME: "image/png"})
	for range s.Send(context.Background(), "hi") {
	}
	var joined strings.Builder
	for _, m := range client.CallsSnapshot()[0].Msgs {
		joined.WriteString(m.Content + "\n")
	}
	if !strings.Contains(joined.String(), "here you go") || !strings.Contains(joined.String(), "[attachment dropped:") {
		t.Fatalf("dropped-attachment note missing from request: %q", joined.String())
	}
}
