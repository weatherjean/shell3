//go:build unix

package telegram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/media"
)

// writeSpeechFile writes data to a temp file under t.TempDir and returns its path.
func writeSpeechFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing fake speech file: %v", err)
	}
	return path
}

func TestDeliverReply_ModeAlwaysSendsVoice(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		TTSMode: "always",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{Path: writeSpeechFile(t, "out.opus", []byte("opus-bytes")), VoiceCompatible: true}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "hello there", false)

	if len(fc.voices) != 1 {
		t.Fatalf("want 1 voice sent, got %d", len(fc.voices))
	}
	if len(fc.sentTexts()) != 0 {
		t.Fatalf("voice reply must replace the text bubble, got texts %v", fc.sentTexts())
	}
}

func TestDeliverReply_CleansSynthesizedFileAfterSendVoice(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	var filePath string
	b.SetMedia(&media.Clients{
		TTSMode: "always",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			filePath = writeSpeechFile(t, "out.opus", []byte("opus-bytes"))
			return media.Speech{Path: filePath, VoiceCompatible: true}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "hello there", false)

	if _, err := os.Stat(filePath); err == nil {
		t.Fatalf("want synthesized file to be cleaned up, but it still exists at %s", filePath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error checking file: %v", err)
	}
}

func TestDeliverReply_SendVoiceFailsFallsBackToText(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	fc.failVoice = errors.New("voice api down")
	b.SetMedia(&media.Clients{
		TTSMode: "always",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{Path: writeSpeechFile(t, "out.opus", []byte("opus-bytes")), VoiceCompatible: true}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "fallback text", false)

	if len(fc.voices) != 0 {
		t.Fatalf("want no voice sent when SendVoice fails, got %d", len(fc.voices))
	}
	if !containsText(fc, "fallback text") {
		t.Fatalf("want text fallback when SendVoice fails, got %v", fc.sentTexts())
	}
}

func TestDeliverReply_SendAudioFailsFallsBackToText(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	fc.failAudio = errors.New("audio api down")
	b.SetMedia(&media.Clients{
		TTSMode: "always",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{Path: writeSpeechFile(t, "out.mp3", []byte("mp3-bytes")), VoiceCompatible: false}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "fallback text", false)

	if len(fc.audios) != 0 {
		t.Fatalf("want no audio sent when SendAudio fails, got %d", len(fc.audios))
	}
	if !containsText(fc, "fallback text") {
		t.Fatalf("want text fallback when SendAudio fails, got %v", fc.sentTexts())
	}
}

func TestDeliverReply_ModeInboundWithVoiceSendsVoice(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	calls := 0
	b.SetMedia(&media.Clients{
		TTSMode: "inbound",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			calls++
			return media.Speech{Path: writeSpeechFile(t, "out.opus", []byte("opus-bytes")), VoiceCompatible: true}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "hi", true)

	if len(fc.voices) != 1 {
		t.Fatalf("want 1 voice sent, got %d", len(fc.voices))
	}
	if calls != 1 {
		t.Fatalf("want Speak called once, got %d", calls)
	}
}

func TestDeliverReply_ModeInboundWithoutVoiceSendsText(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	calls := 0
	b.SetMedia(&media.Clients{
		TTSMode: "inbound",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			calls++
			return media.Speech{Path: writeSpeechFile(t, "out.opus", []byte("opus-bytes")), VoiceCompatible: true}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "hi", false)

	if len(fc.voices) != 0 {
		t.Fatalf("want no voice sent, got %d", len(fc.voices))
	}
	if calls != 0 {
		t.Fatalf("want Speak never called, got %d calls", calls)
	}
	if !containsText(fc, "hi") {
		t.Fatalf("want text reply sent, got %v", fc.sentTexts())
	}
}

func TestDeliverReply_ModeOffSendsText(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	calls := 0
	b.SetMedia(&media.Clients{
		TTSMode: "off",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			calls++
			return media.Speech{Path: writeSpeechFile(t, "out.opus", []byte("opus-bytes")), VoiceCompatible: true}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "hi", true)

	if len(fc.voices) != 0 || calls != 0 {
		t.Fatalf("mode=off must never speak, got voices=%d calls=%d", len(fc.voices), calls)
	}
	if !containsText(fc, "hi") {
		t.Fatalf("want text reply sent, got %v", fc.sentTexts())
	}
}

func TestDeliverReply_SpeakErrorFallsBackToText(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		TTSMode: "always",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{}, errors.New("tts down")
		},
	}, nil)

	b.deliverReply(context.Background(), "hi", false)

	if len(fc.voices) != 0 {
		t.Fatalf("want no voice on Speak error, got %d", len(fc.voices))
	}
	if !containsText(fc, "hi") {
		t.Fatalf("want text fallback, got %v", fc.sentTexts())
	}
	// The fallback must say WHY it fell back: a ⚠️ notice with the error.
	found := false
	for _, txt := range fc.sentTexts() {
		if strings.Contains(txt, "⚠️") && strings.Contains(txt, "tts down") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a ⚠️ notice carrying the Speak error, got %v", fc.sentTexts())
	}
}

func TestDeliverReply_Mp3SendsAudio(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		TTSMode: "always",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{Path: writeSpeechFile(t, "out.mp3", []byte("mp3-bytes")), VoiceCompatible: false}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "hi", false)

	if len(fc.audios) != 1 {
		t.Fatalf("want 1 audio sent, got %d", len(fc.audios))
	}
	if len(fc.voices) != 0 {
		t.Fatalf("mp3 must not go through SendVoice, got %d voices", len(fc.voices))
	}
	if len(fc.sentTexts()) != 0 {
		t.Fatalf("audio reply must replace text bubble, got %v", fc.sentTexts())
	}
}

func TestDeliverReply_EmptyReplyNeverSpeaks(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	calls := 0
	b.SetMedia(&media.Clients{
		TTSMode: "always",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			calls++
			return media.Speech{Path: writeSpeechFile(t, "out.opus", []byte("x")), VoiceCompatible: true}, nil
		},
	}, nil)

	b.deliverReply(context.Background(), "", false)

	if calls != 0 {
		t.Fatalf("want Speak never called for an empty reply, got %d", calls)
	}
	if !containsText(fc, "(no output)") {
		t.Fatalf("want the usual empty-reply placeholder, got %v", fc.sentTexts())
	}
}

// containsText reports whether any sent/HTML message contains sub.
func containsText(fc *fakeClient, sub string) bool {
	for _, s := range fc.sentTexts() {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestVoiceCommand_BareShowsMenu(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		TTSMode: "inbound",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{}, nil
		},
	}, nil)

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/voice"})

	if len(fc.menus) != 1 {
		t.Fatalf("want 1 menu sent, got %d", len(fc.menus))
	}
	m := fc.menus[0]
	if len(m.options) != 3 {
		t.Fatalf("want 3 options, got %d", len(m.options))
	}
	expectedOpts := voiceModeOptions()
	for i, opt := range m.options {
		if opt.Data != expectedOpts[i].Data {
			t.Fatalf("option %d: want Data %q, got %q", i, expectedOpts[i].Data, opt.Data)
		}
		if opt.Label == "" {
			t.Fatalf("option %d: want non-empty Label, got empty", i)
		}
		if opt.Label != expectedOpts[i].Label {
			t.Fatalf("option %d: want Label %q, got %q", i, expectedOpts[i].Label, opt.Label)
		}
	}
	if !strings.Contains(m.text, "inbound") {
		t.Fatalf("want current mode in menu text, got %q", m.text)
	}
}

func TestVoiceCommand_SetModePersistsAndConfirms(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	store := &media.ModeStore{Path: filepath.Join(t.TempDir(), "mode.json")}
	b.SetMedia(&media.Clients{
		TTSMode: "off",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{}, nil
		},
	}, store)

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/voice always"})

	if got := store.Get("off"); got != "always" {
		t.Fatalf("want persisted mode 'always', got %q", got)
	}
	if !containsText(fc, "always") {
		t.Fatalf("want confirmation reply mentioning the new mode, got %v", fc.sentTexts())
	}
}

func TestVoiceCallback_SetsModeAndEditsMenu(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	store := &media.ModeStore{Path: filepath.Join(t.TempDir(), "mode.json")}
	b.SetMedia(&media.Clients{
		TTSMode: "inbound",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{}, nil
		},
	}, store)

	// Send the menu first so voiceMenuMsgID is recorded.
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/voice"})
	if len(fc.menus) != 1 {
		t.Fatalf("setup: want 1 menu sent, got %d", len(fc.menus))
	}
	menuMsgID := fc.menus[0].msgID

	// Extract the "off" option's Data from the actual menu.
	opts := fc.menus[0].options
	var offData string
	for _, opt := range opts {
		if opt.Label == "off" {
			offData = opt.Data
			break
		}
	}
	if offData == "" {
		t.Fatalf("setup: 'off' option not found in menu")
	}

	b.handleCallback(context.Background(), Callback{ID: "cb1", Data: offData})

	if got := store.Get("inbound"); got != "off" {
		t.Fatalf("want persisted mode 'off', got %q", got)
	}
	edits := fc.editTexts()
	if len(edits) != 1 {
		t.Fatalf("want 1 edit, got %d: %v", len(edits), edits)
	}
	if !strings.Contains(edits[0], "off") {
		t.Fatalf("want edited menu text to mention 'off', got %q", edits[0])
	}
	_ = menuMsgID
}

func TestVoiceCommand_NotConfigured(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42) // SetMedia never called

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/voice"})

	if !containsText(fc, "not configured") {
		t.Fatalf("want a not-configured reply, got %v", fc.sentTexts())
	}
	if len(fc.menus) != 0 {
		t.Fatalf("want no menu when TTS is unconfigured, got %d", len(fc.menus))
	}
}

// TestHandleMsg_EndToEndVoiceReply drives a full user turn (fakellm) with
// turnHadVoice set and mode=inbound, asserting the reply is delivered as a
// voice note rather than text.
func TestHandleMsg_EndToEndVoiceReply(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "spoken reply")
	b := NewBot(fc, rt, sess, 42)
	b.SetMedia(&media.Clients{
		TTSMode: "inbound",
		Speak: func(ctx context.Context, text string) (media.Speech, error) {
			return media.Speech{Path: writeSpeechFile(t, "out.opus", []byte("opus-bytes")), VoiceCompatible: true}, nil
		},
	}, nil)

	b.handleMsg(context.Background(), Msg{ChatID: 42, Media: []Media{
		{Bytes: []byte("OggS-fake"), MIME: "audio/ogg", Filename: "voice.ogg"},
	}})

	voiceCount := func() int {
		fc.mu.Lock()
		defer fc.mu.Unlock()
		return len(fc.voices)
	}
	waitFor(t, func() bool {
		return voiceCount() > 0 || len(fc.sentTexts()) > 0
	})
	if n := voiceCount(); n != 1 {
		t.Fatalf("want 1 voice sent for the end-to-end voice turn, got %d (texts=%v)", n, fc.sentTexts())
	}
}
