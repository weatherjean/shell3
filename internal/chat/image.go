package chat

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
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

// loadImagePart resolves path against workDir, validates type and size, decodes,
// downscales so the longest side is ≤ maxImageSide, JPEG-encodes, and returns an
// image_url ContentPart whose URL is a base64 data URI, plus the source image's
// pixel dimensions.
func loadImagePart(path, workDir string) (llm.ContentPart, int, int, error) {
	path = resolveReadPath(path, workDir) // same ~ + relative resolution as the read tool

	ext := strings.ToLower(filepath.Ext(path))
	if !supportedImageExts[ext] {
		return llm.ContentPart{}, 0, 0, fmt.Errorf("unsupported file type %q — use jpg, png, gif, or webp", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return llm.ContentPart{}, 0, 0, fmt.Errorf("cannot read %q: %w", path, err)
	}
	if info.Size() > maxImageBytes {
		return llm.ContentPart{}, 0, 0, fmt.Errorf("image too large (%d MB, max 10 MB)", info.Size()>>20)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentPart{}, 0, 0, fmt.Errorf("cannot read %q: %w", path, err)
	}

	encoded, w, h, err := resizeAndEncodeJPEG(raw, maxImageSide, jpegQuality)
	if err != nil {
		return llm.ContentPart{}, 0, 0, fmt.Errorf("image encode: %w", err)
	}

	return llm.ContentPart{
		Type:     llm.ContentPartTypeImageURL,
		ImageURL: "data:image/jpeg;base64," + encoded,
	}, w, h, nil
}

// imagePartFromBytes validates the size cap, downscales/JPEG-encodes raw image
// bytes via resizeAndEncodeJPEG, and returns an image_url ContentPart plus an
// "image WxH" description (source pixel dimensions).
func imagePartFromBytes(data []byte) (llm.ContentPart, string, error) {
	if len(data) > maxImageBytes {
		return llm.ContentPart{}, "", fmt.Errorf("image too large (%d MB, max 10 MB)", len(data)>>20)
	}
	encoded, w, h, err := resizeAndEncodeJPEG(data, maxImageSide, jpegQuality)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf("image encode: %w", err)
	}
	return llm.ContentPart{
		Type:     llm.ContentPartTypeImageURL,
		ImageURL: "data:image/jpeg;base64," + encoded,
	}, fmt.Sprintf("image %dx%d", w, h), nil
}

// resizeAndEncodeJPEG decodes raw image bytes, shrinks so longest side ≤
// maxSide (no-op if already within bounds), JPEG-encodes at the given quality,
// and returns the base64 result plus the source image's pixel width and height.
func resizeAndEncodeJPEG(raw []byte, maxSide, quality int) (string, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", 0, 0, fmt.Errorf("decode: %w", err)
	}

	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	w, h := srcW, srcH
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
		return "", 0, 0, fmt.Errorf("jpeg encode: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), srcW, srcH, nil
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
