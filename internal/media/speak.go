//go:build unix

package media

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/openai/openai-go"

	"github.com/weatherjean/shell3/internal/luacfg"
)

// maxSpeechRunes is the largest input Speak will send for synthesis (a
// conservative cut below OpenAI's 4096-character wire limit, leaving room for
// the "… (truncated)" marker appended by capSpeechText).
const maxSpeechRunes = 4000

// markdown transforms applied in order by StripMarkdownForSpeech: links
// first (so their brackets/parens don't get mangled by emphasis markers),
// then code spans, then emphasis wrappers, then leading heading/list
// markers.
var (
	mdFencedCode = regexp.MustCompile("(?s)```[a-zA-Z0-9]*\n?(.*?)```")
	mdLink       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdInlineCode = regexp.MustCompile("`([^`]*)`")
	mdBoldStar   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdBoldUnder  = regexp.MustCompile(`__([^_]+)__`)
	mdItalicStar = regexp.MustCompile(`\*([^*]+)\*`)
	mdItalicUnd  = regexp.MustCompile(`_([^_]+)_`)
	mdHeading    = regexp.MustCompile(`(?m)^#+[ \t]+`)
	mdListBullet = regexp.MustCompile(`(?m)^[-*][ \t]+`)
)

// StripMarkdownForSpeech removes common Markdown markup from s so a TTS
// engine doesn't read out literal asterisks, backticks, or link syntax.
// It is a best-effort textual transform, not a full Markdown parser.
func StripMarkdownForSpeech(s string) string {
	s = mdFencedCode.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdInlineCode.ReplaceAllString(s, "$1")
	s = mdBoldStar.ReplaceAllString(s, "$1")
	s = mdBoldUnder.ReplaceAllString(s, "$1")
	s = mdItalicStar.ReplaceAllString(s, "$1")
	s = mdItalicUnd.ReplaceAllString(s, "$1")
	s = mdHeading.ReplaceAllString(s, "")
	s = mdListBullet.ReplaceAllString(s, "")
	return s
}

// capSpeechText truncates s to maxSpeechRunes runes (rune-safe: no partial
// UTF-8 sequence at the cut), appending "… (truncated)" when a cut happened.
// Below the cap, s is returned unchanged.
func capSpeechText(s string) string {
	r := []rune(s)
	if len(r) <= maxSpeechRunes {
		return s
	}
	return string(r[:maxSpeechRunes]) + "… (truncated)"
}

// speechExt returns the output file extension for a shell3.tts{} format:
// opus is muxed into an .ogg container (Telegram voice bubbles expect Ogg
// Opus), every other format keeps its name as the extension.
func speechExt(format string) string {
	if format == "opus" || format == "ogg" {
		return ".ogg"
	}
	return "." + format
}

// newSpeaker builds Clients.Speak for cfg, resolving its client (and, on
// first use, spawning cfg's model's run_proxy) via sdk.
func newSpeaker(sdk sdkFn, cfg luacfg.TTSConfig) func(context.Context, string) (Speech, error) {
	return func(ctx context.Context, text string) (Speech, error) {
		stripped := strings.TrimSpace(StripMarkdownForSpeech(text))
		if stripped == "" {
			return Speech{}, fmt.Errorf("media: nothing to speak")
		}
		stripped = capSpeechText(stripped)

		client, m := sdk(cfg.ModelRef)
		params := openai.AudioSpeechNewParams{
			Model:          openai.SpeechModel(m.ModelID),
			Input:          stripped,
			Voice:          openai.AudioSpeechNewParamsVoice(cfg.Voice),
			ResponseFormat: openai.AudioSpeechNewParamsResponseFormat(cfg.Format),
		}
		resp, err := client.Audio.Speech.New(ctx, params)
		if err != nil {
			return Speech{}, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return Speech{}, fmt.Errorf("media: reading speech response: %w", err)
		}

		dir, err := outDir()
		if err != nil {
			return Speech{}, err
		}
		f, err := os.CreateTemp(dir, "tts-*"+speechExt(cfg.Format))
		if err != nil {
			return Speech{}, fmt.Errorf("media: creating speech file: %w", err)
		}
		defer f.Close()
		if _, err := f.Write(data); err != nil {
			os.Remove(f.Name())
			return Speech{}, fmt.Errorf("media: writing speech file: %w", err)
		}

		return Speech{
			Path:            f.Name(),
			VoiceCompatible: cfg.Format == "opus" || cfg.Format == "ogg",
		}, nil
	}
}
