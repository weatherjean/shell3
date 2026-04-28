package chat

import (
	"bytes"
	"encoding/base64"
	"fmt"
	_ "image/gif"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

const (
	maxImageBytes = 10 << 20 // 10 MB
	maxImageSide  = 1000     // longest side after resize
	jpegQuality   = 85
)

var supportedImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

// buildImageMessage parses "/image args" into a multimodal llm.Message.
// Quoted paths handle filenames with spaces: /image "Screenshot 2026.png" prompt
func buildImageMessage(args, workDir string) (llm.Message, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return llm.Message{}, fmt.Errorf(`usage: /image "<path>" [prompt]`)
	}

	var path, prompt string
	if strings.HasPrefix(args, `"`) {
		var ok bool
		path, prompt, ok = strings.Cut(args[1:], `"`)
		if !ok {
			return llm.Message{}, fmt.Errorf("unterminated quote in path")
		}
		prompt = strings.TrimSpace(prompt)
	} else {
		path, prompt, _ = strings.Cut(args, " ")
		prompt = strings.TrimSpace(prompt)
	}

	if prompt == "" {
		prompt = "Describe this image."
	}

	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}

	ext := strings.ToLower(filepath.Ext(path))
	if !supportedImageExts[ext] {
		return llm.Message{}, fmt.Errorf("unsupported file type %q — use jpg, png, gif, or webp", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return llm.Message{}, fmt.Errorf(`cannot read "%s": %w`, path, err)
	}
	if info.Size() > maxImageBytes {
		return llm.Message{}, fmt.Errorf("image too large (%d MB, max 10 MB)", info.Size()>>20)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return llm.Message{}, fmt.Errorf(`cannot read "%s": %w`, path, err)
	}

	encoded, err := resizeAndEncodeJPEG(raw, maxImageSide, jpegQuality)
	if err != nil {
		return llm.Message{}, fmt.Errorf("image encode: %w", err)
	}

	dataURI := "data:image/jpeg;base64," + encoded

	return llm.Message{
		Role: llm.RoleUser,
		ContentParts: []llm.ContentPart{
			{Type: llm.ContentPartTypeImageURL, ImageURL: dataURI},
			{Type: llm.ContentPartTypeText, Text: prompt},
		},
	}, nil
}

// resizeAndEncodeJPEG decodes raw image bytes, shrinks so longest side ≤
// maxSide (no-op if already within bounds), JPEG-encodes at the given
// quality, and returns the base64 result.
func resizeAndEncodeJPEG(raw []byte, maxSide, quality int) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > maxSide || h > maxSide {
		if w >= h {
			h = h * maxSide / w
			w = maxSide
		} else {
			w = w * maxSide / h
			h = maxSide
		}
		img = resizeNearest(img, w, h)
	}

	// Pre-allocate: JPEG at q85 is typically 0.1–0.5 bits/pixel.
	buf := bytes.NewBuffer(make([]byte, 0, len(raw)/4))
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return "", fmt.Errorf("jpeg encode: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// resizeNearest scales src to newW×newH using nearest-neighbour sampling.
func resizeNearest(src image.Image, newW, newH int) *image.NRGBA {
	b := src.Bounds()
	scaleX := float64(b.Dx()) / float64(newW)
	scaleY := float64(b.Dy()) / float64(newH)
	dst := image.NewNRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		srcY := int(float64(y)*scaleY) + b.Min.Y
		for x := 0; x < newW; x++ {
			srcX := int(float64(x)*scaleX) + b.Min.X
			r, g, bl, a := src.At(srcX, srcY).RGBA()
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(bl >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}
