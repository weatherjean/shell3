//go:build unix

package telegram

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/media"
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
	b := newBot(t, fc, rt)
	b.SetMedia(&MediaCaps{Clients: media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "hi there", nil },
	},
		STTEcho: true,
	})

	saved := []savedFile{saveVoice(t)}
	injected := b.preflightText(context.Background(), saved, sess)

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
	b := newBot(t, fc, rt)
	b.SetMedia(&MediaCaps{Clients: media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "", errors.New("stt down") },
	},
		STTEcho: true,
	})

	saved := []savedFile{saveVoice(t)}
	injected := b.preflightText(context.Background(), saved, sess)

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
	b := newBot(t, fc, rt)
	b.SetMedia(&MediaCaps{Clients: media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "hi there", nil },
	},
		STTEcho: false,
	})

	saved := []savedFile{saveVoice(t)}
	injected := b.preflightText(context.Background(), saved, sess)

	if !strings.Contains(injected, `"hi there"`) {
		t.Fatalf("transcript should still be injected, got %q", injected)
	}
	if len(fc.sentTexts()) != 0 {
		t.Fatalf("want no echo when STTEcho is false, got %v", fc.sentTexts())
	}
}

// TestPreflight_ImageAttachmentNoteSurvives pins that an image attachment
// still carries its path note through preflight now that media.describe is
// gone — the agent's own tools (bash/read_media) are the only way to look at
// it.
func TestPreflight_ImageAttachmentNoteSurvives(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetMedia(&MediaCaps{Clients: media.Clients{}})

	saved := []savedFile{savePhoto(t)}
	injected := b.preflightText(context.Background(), saved, sess)

	if !strings.Contains(injected, saved[0].Path) {
		t.Fatalf("injected must still carry the path note, got %q", injected)
	}
}

func TestPreflight_NoMediaConfiguredMatchesToday(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetMedia(&MediaCaps{Clients: media.Clients{}})

	saved := []savedFile{saveVoice(t), savePhoto(t)}
	injected := b.preflightText(context.Background(), saved, sess)

	want := attachmentNote(saved, b.hasTool(sess, "read_media"))
	if injected != want {
		t.Fatalf("with no capabilities configured, preflight must match plain attachmentNote:\ngot  %q\nwant %q", injected, want)
	}
}

func TestPreflight_MediaNeverSetMatchesToday(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt) // SetMedia never called (b.media stays nil)

	saved := []savedFile{savePhoto(t)}
	injected := b.preflightText(context.Background(), saved, sess)

	want := attachmentNote(saved, b.hasTool(sess, "read_media"))
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
	b := newBot(t, fc, rt)

	b.SetMedia(&MediaCaps{Clients: media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "first", nil },
	}})
	b.SetMedia(&MediaCaps{Clients: media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "second", nil },
	}})

	saved := []savedFile{saveVoice(t)}
	injected := b.preflightText(context.Background(), saved, sess)

	if strings.Contains(injected, "first") {
		t.Fatalf("preflight used the first SetMedia call's Transcribe, want the second: %q", injected)
	}
	if !strings.Contains(injected, "second") {
		t.Fatalf("preflight should reflect the second SetMedia call's Transcribe, got %q", injected)
	}
}

// TestPreflight_VoiceAndImage_LineOrdering pins the reviewer's Minor: a
// message carrying both a voice note and a photo, with Transcribe configured
// and succeeding, must inject the quoted transcript first and the path note
// last — the iteration order of saved (voice.ogg saved before photo.jpg).
func TestPreflight_VoiceAndImage_LineOrdering(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetMedia(&MediaCaps{Clients: media.Clients{
		Transcribe: func(ctx context.Context, path string) (string, error) { return "hi there", nil },
	}})

	saved := []savedFile{saveVoice(t), savePhoto(t)}
	injected := b.preflightText(context.Background(), saved, sess)

	transcriptIdx := strings.Index(injected, `"hi there"`)
	noteIdx := strings.Index(injected, "[The user sent")
	if transcriptIdx == -1 || noteIdx == -1 {
		t.Fatalf("expected both segments present, got %q", injected)
	}
	if transcriptIdx >= noteIdx {
		t.Fatalf("want transcript < note ordering, got %q", injected)
	}
}
