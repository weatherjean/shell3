package shell3

import (
	"errors"
	"fmt"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
)

// PartKind discriminates a Part's media type.
type PartKind int

const (
	PartImage PartKind = iota // jpg/png/gif/webp → resized JPEG data URI
	PartAudio                 // wav/mp3 → base64 input_audio
)

// String returns "image"/"audio" for error messages.
func (k PartKind) String() string {
	switch k {
	case PartImage:
		return "image"
	case PartAudio:
		return "audio"
	default:
		return fmt.Sprintf("PartKind(%d)", int(k))
	}
}

// Part is one inbound media attachment for SendParts and Interject. Set
// exactly one of Path or Data. With Data, MIME is required ("image/png",
// "audio/mpeg", …) and selects the handling; with Path, routing is by file
// extension and MIME is ignored. Relative paths resolve against the session
// workdir. Size caps match read_media: 10 MB images, 25 MB audio. Images are
// downscaled and re-encoded as JPEG; audio is passed through untranscoded
// (wav/mp3 only — the wire formats). Images are decoded and thus
// content-validated; audio bytes are trusted from the caller as-is — only the
// MIME/Kind cross-check applies, the content is never sniffed.
type Part struct {
	Kind PartKind
	Path string // file on disk (extension-routed)
	Data []byte // in-memory bytes (MIME-routed)
	MIME string // required with Data, e.g. "image/png", "audio/mpeg"
}

// loadPart converts one public Part into an internal ContentPart, enforcing
// the Part contract: exactly one of Path/Data, MIME with Data, and Kind
// matching the loaded media type. Size caps are enforced by the chat loaders.
// Errors are unprefixed: callers add the outermost "shell3: " (loadParts also
// adds the part index; Interject embeds them in a dropped-attachment note).
func (s *Session) loadPart(p Part) (llm.ContentPart, error) {
	if p.Kind != PartImage && p.Kind != PartAudio {
		return llm.ContentPart{}, fmt.Errorf("unknown part kind %s", p.Kind)
	}
	var cp llm.ContentPart
	var err error
	switch {
	case p.Path != "" && len(p.Data) > 0:
		return llm.ContentPart{}, errors.New("part sets both Path and Data; set exactly one")
	case p.Path != "":
		cp, _, err = chat.LoadMediaPart(p.Path, s.cfg.WorkDir)
	case len(p.Data) > 0:
		if p.MIME == "" {
			return llm.ContentPart{}, errors.New("part with Data requires MIME")
		}
		cp, _, err = chat.MediaPartFromBytes(p.Data, p.MIME)
	default:
		return llm.ContentPart{}, errors.New("part sets neither Path nor Data")
	}
	if err != nil {
		return llm.ContentPart{}, err
	}
	want := llm.ContentPartTypeImageURL
	if p.Kind == PartAudio {
		want = llm.ContentPartTypeInputAudio
	}
	if cp.Type != want {
		return llm.ContentPart{}, fmt.Errorf("part declared %s but loaded %s media", p.Kind, cp.Type)
	}
	return cp, nil
}

// loadParts converts a Part slice, failing fast on the first invalid part
// (SendParts' all-or-nothing contract; Interject drops per-part instead).
func (s *Session) loadParts(parts []Part) ([]llm.ContentPart, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]llm.ContentPart, 0, len(parts))
	for i, p := range parts {
		cp, err := s.loadPart(p)
		if err != nil {
			return nil, fmt.Errorf("shell3: part %d: %w", i, err)
		}
		out = append(out, cp)
	}
	return out, nil
}
