package chat

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// LoadMediaPart resolves and loads a media file as a multimodal ContentPart,
// routing by file extension: images (jpg, jpeg, png, gif, webp) become
// image_url parts, audio (wav, mp3, ogg, oga, opus) becomes input_audio
// parts, and PDFs (pdf) become file parts. It returns the part plus a short
// human-readable description for the tool result — the read_media tool's
// loader.
func LoadMediaPart(path, workDir string) (llm.ContentPart, string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case supportedImageExts[ext]:
		return loadImagePart(path, workDir)
	case supportedAudioExts[ext]:
		return loadAudioPart(path, workDir)
	case supportedPDFExts[ext]:
		return loadPDFPart(path, workDir)
	default:
		return llm.ContentPart{}, "", fmt.Errorf("unsupported media type %q — use jpg, jpeg, png, gif, webp, wav, mp3, ogg, oga, opus, or pdf", ext)
	}
}

// handleReadMedia parses {"path": "..."} tool args, loads the media via
// LoadMediaPart, and returns the tool-result text plus the media ContentPart.
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
	part, desc, err := LoadMediaPart(args.Path, workDir)
	if err != nil {
		return "error: " + err.Error(), llm.ContentPart{}
	}
	return fmt.Sprintf("Loaded %s from %q. It is attached as a user message right after the tool results so you can view/hear it on the next step.", desc, args.Path), part
}
