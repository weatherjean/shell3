//go:build unix

package telegram

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// saveVoice/savePhoto build one saved attachment on disk (cleaned up by
// t.Cleanup) for preflight tests to operate on.
func saveVoice(t *testing.T) savedFile {
	t.Helper()
	saved := saveAttachments([]Media{{Bytes: []byte("OggS-fake"), MIME: "audio/ogg", Filename: "voice.ogg"}})
	if len(saved) != 1 {
		t.Fatalf("want 1 saved file, got %d", len(saved))
	}
	t.Cleanup(func() { _ = os.Remove(saved[0].Path) })
	return saved[0]
}

func savePhoto(t *testing.T) savedFile {
	t.Helper()
	saved := saveAttachments([]Media{{Bytes: []byte("\xff\xd8\xff"), MIME: "image/jpeg", Filename: "photo.jpg"}})
	if len(saved) != 1 {
		t.Fatalf("want 1 saved file, got %d", len(saved))
	}
	t.Cleanup(func() { _ = os.Remove(saved[0].Path) })
	return saved[0]
}

func TestPreflight_VoiceTranscribeSuccessEchoes(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "hi there", nil },
		STTEcho:    true,
	}, nil)

	saved := []savedFile{saveVoice(t)}
	injected, hadVoice := b.preflight(context.Background(), saved)

	if !hadVoice {
		t.Fatal("want hadVoice=true for an audio/ attachment")
	}
	if !strings.HasPrefix(injected, `"hi there"`) {
		t.Fatalf("injected should start with the quoted transcript, got %q", injected)
	}
	if !strings.Contains(injected, saved[0].Path) {
		t.Fatalf("injected must still carry the path note, got %q", injected)
	}
	if !waitForReply(t, fc, `📝 "hi there"`) {
		t.Fatalf("expected an echo message, got %v", fc.sentTexts())
	}
}

func TestPreflight_TranscribeErrorNoticesChat(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "", errors.New("stt down") },
		STTEcho:    true,
	}, nil)

	saved := []savedFile{saveVoice(t)}
	injected, hadVoice := b.preflight(context.Background(), saved)

	if !hadVoice {
		t.Fatal("want hadVoice=true even on transcribe failure")
	}
	if !strings.Contains(injected, "[voice note could not be transcribed]") {
		t.Fatalf("want the failure marker, got %q", injected)
	}
	// The provider error must reach the user, not vanish: one ⚠️ notice
	// carrying the error text (and no transcript echo).
	texts := fc.sentTexts()
	if len(texts) != 1 || !strings.Contains(texts[0], "⚠️") || !strings.Contains(texts[0], "stt down") {
		t.Fatalf("want one ⚠️ notice carrying the error, got %v", texts)
	}
}

func TestPreflight_STTEchoFalseNoEcho(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "hi there", nil },
		STTEcho:    false,
	}, nil)

	saved := []savedFile{saveVoice(t)}
	injected, _ := b.preflight(context.Background(), saved)

	if !strings.Contains(injected, `"hi there"`) {
		t.Fatalf("transcript should still be injected, got %q", injected)
	}
	if len(fc.sentTexts()) != 0 {
		t.Fatalf("want no echo when STTEcho is false, got %v", fc.sentTexts())
	}
}

func TestPreflight_ImageDescribeSuccess(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		Describe: func(ctx context.Context, path string) (string, error) { return "a red square", nil },
	}, nil)

	saved := []savedFile{savePhoto(t)}
	injected, hadVoice := b.preflight(context.Background(), saved)

	if hadVoice {
		t.Fatal("want hadVoice=false for an image attachment")
	}
	if !strings.Contains(injected, "[image: a red square]") {
		t.Fatalf("want the description injected, got %q", injected)
	}
	if !strings.Contains(injected, saved[0].Path) {
		t.Fatalf("injected must still carry the path note, got %q", injected)
	}
}

func TestPreflight_DescribeErrorNoticesChat(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		Describe: func(ctx context.Context, path string) (string, error) { return "", errors.New("describe down") },
	}, nil)

	saved := []savedFile{savePhoto(t)}
	injected, _ := b.preflight(context.Background(), saved)

	if strings.Contains(injected, "[image:") {
		t.Fatalf("want no description on failure, got %q", injected)
	}
	want := attachmentNote(saved, b.hasTool("read_media"))
	if injected != want {
		t.Fatalf("want plain attachment note %q, got %q", want, injected)
	}
	texts := fc.sentTexts()
	if len(texts) != 1 || !strings.Contains(texts[0], "⚠️") || !strings.Contains(texts[0], "describe down") {
		t.Fatalf("want one ⚠️ notice carrying the error, got %v", texts)
	}
}

func TestPreflight_NoMediaConfiguredMatchesToday(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{}, nil)

	saved := []savedFile{saveVoice(t), savePhoto(t)}
	injected, hadVoice := b.preflight(context.Background(), saved)

	if !hadVoice {
		t.Fatal("want hadVoice=true: an audio/ attachment was present even though Transcribe is unconfigured")
	}
	want := attachmentNote(saved, b.hasTool("read_media"))
	if injected != want {
		t.Fatalf("with no capabilities configured, preflight must match plain attachmentNote:\ngot  %q\nwant %q", injected, want)
	}
}

func TestPreflight_MediaNeverSetMatchesToday(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42) // SetMedia never called (b.media stays nil)

	saved := []savedFile{savePhoto(t)}
	injected, _ := b.preflight(context.Background(), saved)

	want := attachmentNote(saved, b.hasTool("read_media"))
	if injected != want {
		t.Fatalf("with SetMedia never called, preflight must match plain attachmentNote:\ngot  %q\nwant %q", injected, want)
	}
}

// TestSetMedia_SecondCallWins pins the reload contract: SetMedia may be
// called again (as the host does after every Runtime.Reload, per its own
// doc comment) with a fresh media.Clients, and the new Clients — not the
// first — govern subsequent preflight behavior.
func TestSetMedia_SecondCallWins(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)

	b.SetMedia(&media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "first", nil },
	}, nil)
	b.SetMedia(&media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "second", nil },
	}, nil)

	saved := []savedFile{saveVoice(t)}
	injected, _ := b.preflight(context.Background(), saved)

	if strings.Contains(injected, "first") {
		t.Fatalf("preflight used the first SetMedia call's Transcribe, want the second: %q", injected)
	}
	if !strings.Contains(injected, "second") {
		t.Fatalf("preflight should reflect the second SetMedia call's Transcribe, got %q", injected)
	}
}

// TestPreflight_VoiceAndImage_LineOrdering pins the reviewer's Minor: a
// message carrying both a voice note and a photo, with both Transcribe and
// Describe configured and succeeding, must inject the quoted transcript
// first, the [image: …] description second, and the path note last — the
// iteration order of saved (voice.ogg saved before photo.jpg).
func TestPreflight_VoiceAndImage_LineOrdering(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "hi there", nil },
		Describe:   func(ctx context.Context, path string) (string, error) { return "a red square", nil },
	}, nil)

	saved := []savedFile{saveVoice(t), savePhoto(t)}
	injected, hadVoice := b.preflight(context.Background(), saved)

	if !hadVoice {
		t.Fatal("want hadVoice=true with a voice attachment present")
	}
	transcriptIdx := strings.Index(injected, `"hi there"`)
	imageIdx := strings.Index(injected, "[image: a red square]")
	noteIdx := strings.Index(injected, "[The user sent")
	if transcriptIdx == -1 || imageIdx == -1 || noteIdx == -1 {
		t.Fatalf("expected all three segments present, got %q", injected)
	}
	if transcriptIdx >= imageIdx || imageIdx >= noteIdx {
		t.Fatalf("want transcript < image < note ordering, got %q", injected)
	}
}

func TestHandleMsg_VoiceSetsTurnHadVoice(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)

	b.handleMsg(context.Background(), Msg{ChatID: 42, Media: []Media{
		{Bytes: []byte("OggS-fake"), MIME: "audio/ogg", Filename: "voice.ogg"},
	}})

	b.mu.Lock()
	hadVoice := b.turnHadVoice
	b.mu.Unlock()
	if !hadVoice {
		t.Fatal("want turnHadVoice=true after a voice-attachment turn")
	}
}

func TestHandleMsg_InterjectedVoiceSetsTurnHadVoice(t *testing.T) {
	fc := newFakeClient()
	blk := fakellm.NewBlocking()
	rt := shell3test.NewRuntimeForTestClient(t, blk)
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	b := NewBot(fc, rt, sess, 42)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, Text: "do work"})
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn never started")
	}

	b.mu.Lock()
	before := b.turnHadVoice
	b.mu.Unlock()
	if before {
		t.Fatal("turnHadVoice should start false for a text-only turn")
	}

	// A second, voice-bearing message arrives while the first turn is still
	// running: it must be Interjected (not start a second turn) and still
	// flag the turn as having seen voice.
	b.handleMsg(context.Background(), Msg{ChatID: 42, Media: []Media{
		{Bytes: []byte("OggS-fake"), MIME: "audio/ogg", Filename: "voice.ogg"},
	}})

	b.mu.Lock()
	after := b.turnHadVoice
	active := b.turnActive
	b.mu.Unlock()
	if !after {
		t.Fatal("want turnHadVoice=true after an interjected voice attachment")
	}
	if !active {
		t.Fatal("the original turn should still be the one running (interject, not a second turn)")
	}

	// Unwind the still-blocked turn so the test doesn't leak a goroutine.
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/stop"})
}
