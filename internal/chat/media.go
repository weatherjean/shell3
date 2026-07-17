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
// parts, PDFs (pdf) become file parts, and video (mp4, webm, mov) becomes
// video_url parts. It returns the part plus a short
// human-readable description for the tool result. Consumed by the read_media
// tool and by internal/shell3's Part{Path: …}.
func LoadMediaPart(path, workDir string) (llm.ContentPart, string, error) {
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
	case supportedPDFExts[ext]:
		return loadPDFPart(path, workDir)
	case supportedVideoExts[ext]:
		return loadVideoPart(path, workDir)
	default:
		return llm.ContentPart{}, "", fmt.Errorf("unsupported media type %q — use jpg, jpeg, png, gif, webp, wav, mp3, ogg, oga, opus, pdf, mp4, webm, or mov", ext)
	}
}

// MediaPartFromBytes converts in-memory media bytes into a multimodal
// ContentPart, routing by MIME type — the byte-based sibling of LoadMediaPart
// for hosts that hold the data directly (e.g. an in-memory photo download).
// Matching is case-insensitive and parameters after ";" are ignored:
//
//	image/jpeg, image/png, image/gif, image/webp → image_url (resized,
//	    JPEG-encoded base64 data URI, like read_media)
//	audio/wav, audio/x-wav, audio/wave           → input_audio, format "wav"
//	audio/mpeg, audio/mp3                        → input_audio, format "mp3"
//	application/pdf                              → file (filename synthesized
//	    as "file.pdf" — there is no path to derive one from)
//	video/mp4, video/webm, video/quicktime       → video_url
//
// The path loaders' size caps apply (maxImageBytes / maxAudioBytes /
// maxPDFBytes / maxVideoBytes). Returns the part plus a short human-readable
// description.
func MediaPartFromBytes(data []byte, mime string) (llm.ContentPart, string, error) {
	mt := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	switch mt {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return imagePartFromBytes(data)
	case "audio/wav", "audio/x-wav", "audio/wave":
		return audioPartFromBytes(data, "wav")
	case "audio/mpeg", "audio/mp3":
		return audioPartFromBytes(data, "mp3")
	case "audio/ogg", "audio/opus", "audio/oga", "audio/x-opus+ogg":
		return audioPartFromBytes(data, "ogg")
	case "application/pdf":
		return pdfPartFromBytes(data, "")
	case "video/mp4", "video/webm", "video/quicktime":
		return videoPartFromBytes(data, mt)
	default:
		return llm.ContentPart{}, "", fmt.Errorf("unsupported MIME type %q — use image/jpeg, image/png, image/gif, image/webp, audio/wav, audio/mpeg, audio/ogg, application/pdf, video/mp4, video/webm, or video/quicktime", mime)
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
