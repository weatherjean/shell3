package chat

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

const maxAudioBytes = 25 << 20 // 25 MB

var supportedAudioExts = map[string]bool{
	".wav": true, ".mp3": true, ".ogg": true, ".oga": true, ".opus": true,
}

// audioExtFormat maps a file extension (no dot) to the input_audio wire format
// string. Opus-carrying containers (oga/opus) report as "ogg".
func audioExtFormat(ext string) string {
	switch ext {
	case "oga", "opus":
		return "ogg"
	default:
		return ext
	}
}

// loadAudioPart resolves path against workDir (~ expands like the read tool),
// validates the extension and size, reads the raw bytes, and returns an
// input_audio ContentPart carrying the base64-encoded data plus a
// human-readable description. Audio is not decoded or transcoded — only the
// wire formats (wav, mp3, ogg) are accepted; opus-family containers report
// as ogg.
func loadAudioPart(path, workDir string) (llm.ContentPart, string, error) {
	path = resolveReadPath(path, workDir)

	ext := strings.ToLower(filepath.Ext(path))
	if !supportedAudioExts[ext] {
		return llm.ContentPart{}, "", fmt.Errorf("unsupported audio type %q — use wav, mp3, or ogg/opus", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf("cannot read %q: %w", path, err)
	}
	if info.Size() > maxAudioBytes {
		return llm.ContentPart{}, "", fmt.Errorf("audio too large (%d MB, max 25 MB)", info.Size()>>20)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf("cannot read %q: %w", path, err)
	}

	return audioPartFromBytes(raw, audioExtFormat(strings.TrimPrefix(ext, ".")))
}

// audioPartFromBytes validates the size cap and wraps raw audio bytes as a
// base64 input_audio ContentPart. format must be a wire format ("wav", "mp3",
// or "ogg"); audio is never decoded or transcoded.
func audioPartFromBytes(data []byte, format string) (llm.ContentPart, string, error) {
	if len(data) == 0 {
		return llm.ContentPart{}, "", fmt.Errorf("empty audio (0 bytes) — no %s data to attach", format)
	}
	if len(data) > maxAudioBytes {
		return llm.ContentPart{}, "", fmt.Errorf("audio too large (%d MB, max 25 MB)", len(data)>>20)
	}
	desc := fmt.Sprintf("%s audio, %.1f MB", format, float64(len(data))/(1<<20))
	return llm.ContentPart{
		Type:        llm.ContentPartTypeInputAudio,
		AudioData:   base64.StdEncoding.EncodeToString(data),
		AudioFormat: format,
	}, desc, nil
}
