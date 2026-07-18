//go:build unix

package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-go"

	"github.com/weatherjean/shell3/internal/config"
)

// maxAudioBytes is the largest audio file Transcribe will upload (OpenAI's
// whisper/transcribe endpoints reject anything larger).
const maxAudioBytes = 25 << 20 // 25 MB

// audioMIME maps supported audio extensions to the content type sent with
// the multipart file part.
var audioMIME = map[string]string{
	".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4",
	".mp4": "audio/mp4", ".webm": "audio/webm", ".flac": "audio/flac",
}

// validateAudioPath checks path's extension against audioMIME and size
// against maxAudioBytes, returning the MIME type to send. Split out from
// newTranscriber so the size cap can be unit-tested with a fabricated size
// instead of writing a 26 MB file.
func validateAudioPath(path string, size int64) (mime string, err error) {
	ext := strings.ToLower(filepath.Ext(path))
	m, ok := audioMIME[ext]
	if !ok {
		return "", fmt.Errorf("media: unsupported audio type %q", ext)
	}
	if size > maxAudioBytes {
		return "", fmt.Errorf("media: audio too large (%d MB, max 25 MB)", size>>20)
	}
	return m, nil
}

// newTranscriber builds Clients.Transcribe for cfg, resolving its client
// (and, on first use, spawning cfg's model's run_proxy) via sdk.
func newTranscriber(sdk sdkFn, cfg config.STTConfig) func(context.Context, string) (string, error) {
	return func(ctx context.Context, path string) (string, error) {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		mime, err := validateAudioPath(path, info.Size())
		if err != nil {
			return "", err
		}
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()

		client, m := sdk(cfg.ModelRef)
		params := openai.AudioTranscriptionNewParams{
			Model: openai.AudioModel(m.ModelID),
			File:  openai.File(f, filepath.Base(path), mime),
		}
		if cfg.Language != "" {
			params.Language = openai.String(cfg.Language)
		}
		tr, err := client.Audio.Transcriptions.New(ctx, params)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(tr.Text), nil
	}
}
