package chat

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register the webp decoder read_media advertises

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

// loadImagePart resolves path against workDir, validates type and size, and
// hands the bytes to imagePartFromBytes — decode, downscale, and part
// construction live only there. Returns the image_url ContentPart plus an
// "image WxH" description (source pixel dimensions).
func loadImagePart(path, workDir string) (llm.ContentPart, string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if !supportedImageExts[ext] {
		return llm.ContentPart{}, "", fmt.Errorf("unsupported file type %q — use jpg, png, gif, or webp", ext)
	}

	raw, _, err := readMediaFile(path, workDir, "image", maxImageBytes)
	if err != nil {
		return llm.ContentPart{}, "", err
	}

	return imagePartFromBytes(raw)
}

// imagePartFromBytes validates the size cap, downscales/JPEG-encodes raw image
// bytes via resizeAndEncodeJPEG, and returns an image_url ContentPart plus an
// "image WxH" description (source pixel dimensions).
func imagePartFromBytes(data []byte) (llm.ContentPart, string, error) {
	if len(data) > maxImageBytes {
		return llm.ContentPart{}, "", mediaTooLarge("image", int64(len(data)), maxImageBytes)
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
		img = resizeBiLinear(img, w, h)
	}

	// Pre-allocate: JPEG at q85 is typically 0.1–0.5 bits/pixel.
	buf := bytes.NewBuffer(make([]byte, 0, len(raw)/4))
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return "", 0, 0, fmt.Errorf("jpeg encode: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), srcW, srcH, nil
}

// resizeBiLinear scales src to newW×newH with bilinear sampling — smoother
// than nearest-neighbour on the downscales this path always does. The target
// is *image.RGBA for image/jpeg's rgbaToYCbCr encoder fast path (draw.Src
// overwrites every pixel, so premultiplied storage is fully defined).
func resizeBiLinear(src image.Image, newW, newH int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}
