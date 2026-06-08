package chat

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// loadMediaPart resolves and loads a media file as a multimodal ContentPart,
// routing by file extension: images (jpg/png/gif) become image_url parts, audio
// (wav/mp3) becomes input_audio parts. It returns the part plus a short
// human-readable description for the tool result.
func loadMediaPart(path, workDir string) (llm.ContentPart, string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case supportedImageExts[ext]:
		part, w, h, err := loadImagePart(path, workDir)
		if err != nil {
			return llm.ContentPart{}, "", err
		}
		return part, fmt.Sprintf("image %dx%d", w, h), nil
	case supportedAudioExts[ext]:
		return loadAudioPart(path, workDir)
	default:
		return llm.ContentPart{}, "", fmt.Errorf("unsupported media type %q — use jpg, png, gif, wav, or mp3", ext)
	}
}

// handleReadMedia parses {"path": "..."} tool args, loads the media via
// loadMediaPart, and returns the tool-result text plus the media ContentPart.
// On any failure it returns an "error: ..." string and the zero ContentPart so
// the caller queues nothing.
func handleReadMedia(rawArgs, workDir string) (string, llm.ContentPart) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err), llm.ContentPart{}
	}
	if strings.TrimSpace(args.Path) == "" {
		return "error: path is required", llm.ContentPart{}
	}
	part, desc, err := loadMediaPart(args.Path, workDir)
	if err != nil {
		return "error: " + err.Error(), llm.ContentPart{}
	}
	return fmt.Sprintf("Loaded %s from %q. It is attached as a user message right after the tool results so you can view/hear it on the next step.", desc, args.Path), part
}
