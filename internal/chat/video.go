package chat

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

const maxVideoBytes = 40 << 20 // 40 MB

var supportedVideoExts = map[string]bool{
	".mp4": true, ".webm": true, ".mov": true,
}

// videoExtMIME maps a supported video extension (with leading dot) to its
// MIME type, or "" if unsupported.
func videoExtMIME(ext string) string {
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	default:
		return ""
	}
}

// loadVideoPart resolves path against workDir (~ expands), validates the
// extension and size, reads the raw bytes, and returns a video_url
// ContentPart — an OpenRouter/Gemini-style extension of the OpenAI
// chat-completions dialect, not part of the vanilla OpenAI API — carrying a
// base64 data URI plus a human-readable description. Video is never decoded
// or transcoded.
func loadVideoPart(path, workDir string) (llm.ContentPart, string, error) {
	path = resolvePath(path, workDir)

	ext := strings.ToLower(filepath.Ext(path))
	mime := videoExtMIME(ext)
	if mime == "" {
		return llm.ContentPart{}, "", fmt.Errorf("unsupported video type %q — use mp4, webm, or mov", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf("cannot read %q: %w", path, err)
	}
	if info.Size() > maxVideoBytes {
		return llm.ContentPart{}, "", fmt.Errorf("video too large (%d MB, max 40 MB)", info.Size()>>20)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf("cannot read %q: %w", path, err)
	}

	return videoPartFromBytes(raw, mime)
}

// videoPartFromBytes validates the size cap and wraps raw video bytes as a
// base64 video_url ContentPart (data URI). mime must be one of video/mp4,
// video/webm, or video/quicktime.
func videoPartFromBytes(data []byte, mime string) (llm.ContentPart, string, error) {
	if len(data) == 0 {
		return llm.ContentPart{}, "", fmt.Errorf("empty video (0 bytes) — no %s data to attach", mime)
	}
	if len(data) > maxVideoBytes {
		return llm.ContentPart{}, "", fmt.Errorf("video too large (%d MB, max 40 MB)", len(data)>>20)
	}
	desc := fmt.Sprintf("%s video, %.1f MB", mime, float64(len(data))/(1<<20))
	return llm.ContentPart{
		Type:     llm.ContentPartTypeVideoURL,
		VideoURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
	}, desc, nil
}
