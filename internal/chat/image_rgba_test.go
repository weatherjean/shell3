package chat

import (
	"image"
	"testing"
)

// The resize destination must be *image.RGBA: image/jpeg's encoder has a
// specialized rgbaToYCbCr fast path for it, while any other type (NRGBA
// included) falls back to per-pixel At()/color-model conversion.
func TestResizeBiLinearTargetsRGBA(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	var dst image.Image = resizeBiLinear(src, 2, 1)
	if _, ok := dst.(*image.RGBA); !ok {
		t.Fatalf("resize target is %T, want *image.RGBA (jpeg encoder fast path)", dst)
	}
}
