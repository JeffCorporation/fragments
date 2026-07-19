package catalog

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // allow PNG sources too, just in case
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
)

// thumbJPEGQuality is the quality of generated thumbnails.
const thumbJPEGQuality = 85

// GenerateThumbnail decodes the JPEG bytes, downscales so the longest edge is at
// most maxEdge px (never upscaling), applies the EXIF orientation so the result
// is upright, and writes a JPEG to outPath (creating parent directories). It
// returns the thumbnail's dimensions. fast selects the cheaper ApproxBiLinear
// resampler (less CPU, slightly softer) instead of CatmullRom — useful when a
// small VPS runs the worker pool.
func GenerateThumbnail(jpegData []byte, orientation, maxEdge int, fast bool, outPath string) (w, h int, err error) {
	src, _, err := image.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return 0, 0, fmt.Errorf("decode image: %w", err)
	}

	scaled := scaleToFit(src, maxEdge, fast)
	upright := applyOrientation(scaled, orientation)

	if err := writeJPEG(upright, outPath); err != nil {
		return 0, 0, err
	}
	b := upright.Bounds()
	return b.Dx(), b.Dy(), nil
}

// scaleToFit returns src scaled so its longest edge is <= maxEdge. If src is
// already small enough it is returned unchanged. fast picks ApproxBiLinear over
// the higher-quality CatmullRom.
func scaleToFit(src image.Image, maxEdge int, fast bool) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if maxEdge <= 0 || (sw <= maxEdge && sh <= maxEdge) {
		return src
	}
	var dw, dh int
	if sw >= sh {
		dw = maxEdge
		dh = int(float64(sh) * float64(maxEdge) / float64(sw))
	} else {
		dh = maxEdge
		dw = int(float64(sw) * float64(maxEdge) / float64(sh))
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	scaler := xdraw.Interpolator(xdraw.CatmullRom)
	if fast {
		scaler = xdraw.ApproxBiLinear
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	scaler.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	return dst
}

// applyOrientation returns img transformed per the EXIF orientation tag (1..8).
// Orientation 0 or 1 (or anything unknown) is returned unchanged.
func applyOrientation(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return flipH(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipV(img)
	case 5: // transpose: mirror across the main diagonal
		return flipH(rotate90(img))
	case 6: // rotate 90° CW
		return rotate90(img)
	case 7: // transverse: mirror across the anti-diagonal
		return flipH(rotate270(img))
	case 8: // rotate 90° CCW
		return rotate270(img)
	default:
		return img
	}
}

func rotate90(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate270(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate180(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipH(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipV(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// writeJPEG encodes img to outPath atomically: it writes to a temp file in the
// same directory and renames on success, so a failed encode never leaves a
// truncated thumbnail behind. The Close error is checked (a final flush can
// fail on a full or networked filesystem).
func writeJPEG(img image.Image, outPath string) (err error) {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir for thumb: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".thumb-*.jpg")
	if err != nil {
		return fmt.Errorf("create thumb: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if err = jpeg.Encode(tmp, img, &jpeg.Options{Quality: thumbJPEGQuality}); err != nil {
		return fmt.Errorf("encode thumb: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close thumb: %w", err)
	}
	if err = os.Rename(tmpName, outPath); err != nil {
		return fmt.Errorf("rename thumb: %w", err)
	}
	return nil
}
