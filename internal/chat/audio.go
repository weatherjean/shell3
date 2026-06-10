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
	".wav": true, ".mp3": true,
}

// loadAudioPart resolves path against workDir, validates the extension and size,
// reads the raw bytes, and returns an input_audio ContentPart carrying the
// base64-encoded data plus a human-readable description. Audio is not decoded or
// transcoded — only wav and mp3 are accepted, as the wire format requires.
func loadAudioPart(path, workDir string) (llm.ContentPart, string, error) {
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}

	ext := strings.ToLower(filepath.Ext(path))
	if !supportedAudioExts[ext] {
		return llm.ContentPart{}, "", fmt.Errorf("unsupported audio type %q — use wav or mp3", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf(`cannot read "%s": %w`, path, err)
	}
	if info.Size() > maxAudioBytes {
		return llm.ContentPart{}, "", fmt.Errorf("audio too large (%d MB, max 25 MB)", info.Size()>>20)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf(`cannot read "%s": %w`, path, err)
	}

	return audioPartFromBytes(raw, strings.TrimPrefix(ext, "."))
}

// audioPartFromBytes validates the size cap and wraps raw audio bytes as a
// base64 input_audio ContentPart. format must be "wav" or "mp3" (the wire
// formats); audio is never decoded or transcoded.
func audioPartFromBytes(data []byte, format string) (llm.ContentPart, string, error) {
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
