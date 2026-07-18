//go:build unix

// voice.go implements the TTS reply path (deliverReply, the single reply exit
// for a USER turn) and the /voice command + its "vm|<mode>" menu callback.
package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// voiceModePrefix tags the callback_data of a /voice menu button so
// handleCallback can route it separately from confirmPrefix ("bs").
const voiceModePrefix = "vm"

// deliverReply is the single reply exit for a USER turn (handleMsg's turn
// goroutine). It decides, per the configured/persisted voice mode and whether
// this turn saw voice input, whether to speak the reply as a voice note/audio
// file instead of posting it as text. Voice REPLACES the text bubble — it is
// never sent in addition to it. Any failure at any step (mode resolution,
// Speak, reading the synthesized file, or the send itself) falls back to the
// plain b.sendReply so the reply is never lost; a Speak (synthesis) failure
// additionally sends its error to the chat as a ⚠️ notice.
func (b *Bot) deliverReply(ctx context.Context, reply string, hadVoice bool) {
	if reply == "" {
		b.sendReply(ctx, reply)
		return
	}
	if b.media == nil || b.media.Speak == nil {
		b.sendReply(ctx, reply)
		return
	}

	mode := b.media.TTSMode
	if b.voiceMode != nil {
		mode = b.voiceMode.Get(b.media.TTSMode)
	}

	speak := mode == "always" || (mode == "inbound" && hadVoice)
	if !speak {
		b.sendReply(ctx, reply)
		return
	}

	sp, err := b.media.Speak(ctx, reply)
	if err != nil || sp.Path == "" {
		b.sendReply(ctx, reply)
		if err != nil {
			b.mediaNotice(ctx, "voice reply failed, sent as text", err)
		}
		return
	}
	defer os.Remove(sp.Path)
	data, err := os.ReadFile(sp.Path)
	if err != nil {
		b.sendReply(ctx, reply)
		return
	}

	if sp.VoiceCompatible {
		if err := b.client.SendVoice(ctx, b.chatID, data, ""); err != nil {
			b.sendReply(ctx, reply)
		}
		return
	}
	if err := b.client.SendAudio(ctx, b.chatID, filepath.Base(sp.Path), data, ""); err != nil {
		b.sendReply(ctx, reply)
	}
}

// voiceMenuText renders the /voice bare menu's message body for the given mode.
func voiceMenuText(mode string) string {
	return "🔊 voice replies: " + mode
}

// voiceModeOptions builds the three /voice menu buttons.
func voiceModeOptions() []MenuOption {
	return []MenuOption{
		{Label: "off", Data: voiceModePrefix + "|off"},
		{Label: "inbound", Data: voiceModePrefix + "|inbound"},
		{Label: "always", Data: voiceModePrefix + "|always"},
	}
}

// handleVoiceCommand implements /voice (bare → menu; /voice <mode> → set).
func (b *Bot) handleVoiceCommand(ctx context.Context, arg string) {
	if b.media == nil || b.media.Speak == nil {
		b.sendReply(ctx, "TTS is not configured — add a media.tts block to shell3.yaml")
		return
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		mode := b.media.TTSMode
		if b.voiceMode != nil {
			mode = b.voiceMode.Get(b.media.TTSMode)
		}
		msgID, err := b.client.SendMenu(ctx, b.chatID, voiceMenuText(mode), voiceModeOptions())
		if err != nil {
			b.sendReply(ctx, "failed to send voice menu: "+err.Error())
			return
		}
		b.askMu.Lock()
		b.voiceMenuMsgID = msgID
		b.askMu.Unlock()
		return
	}
	if b.voiceMode == nil {
		b.sendReply(ctx, "voice mode can't be persisted — no mode store configured")
		return
	}
	if err := b.voiceMode.Set(arg); err != nil {
		b.sendReply(ctx, "usage: /voice off|inbound|always")
		return
	}
	b.sendReply(ctx, "🔊 voice replies: "+arg)
}

// handleVoiceCallback handles a "vm|<mode>" menu button press: persists the
// mode and edits the menu message in place to reflect it.
func (b *Bot) handleVoiceCallback(ctx context.Context, mode string) {
	if b.voiceMode == nil {
		return
	}
	if err := b.voiceMode.Set(mode); err != nil {
		return
	}
	b.askMu.Lock()
	msgID := b.voiceMenuMsgID
	b.askMu.Unlock()
	if msgID != 0 {
		_ = b.client.EditPlain(ctx, b.chatID, msgID, voiceMenuText(mode))
	}
}

// parseVoiceModeData decodes a /voice menu button's callback_data
// ("vm|<mode>"). ok is false for any data not produced by voiceModeOptions.
func parseVoiceModeData(data string) (mode string, ok bool) {
	parts := strings.SplitN(data, "|", 2)
	if len(parts) != 2 || parts[0] != voiceModePrefix {
		return "", false
	}
	return parts[1], true
}
