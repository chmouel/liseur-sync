// Package cover turns a publication's cover image into something a
// server can hand to a browser.
//
// EPUB covers are whatever the publisher put in the archive: any size, any
// format, sometimes SVG, sometimes a JPEG with a .png extension. Serving
// those bytes through would make the catalog's image tag an alias for
// arbitrary publisher-controlled content, and would send a phone a
// 4000-pixel image to draw a thumbnail. So the cover is decoded, bounded,
// rescaled, and re-encoded into one format the server chooses
// (ADR-0005, "Extracted media").
package cover

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	// The decoders the server supports are exactly the ones registered
	// here. Anything else — SVG, WebP, AVIF — fails to decode and is
	// reported as having no usable cover, which is the point: the
	// allowlist is the import list.
	_ "image/gif"
	_ "image/png"
)

// ErrUnsupported means the bytes are not an image this server decodes, or
// declare more pixels than it will decode. Both are the same thing to a
// caller: there is no cover to serve.
var ErrUnsupported = errors.New("cover: unsupported image")

// MediaType is what every rendered cover is served as. It is fixed rather
// than derived from the source, because a server-chosen type is what makes
// nosniff mean anything.
const MediaType = "image/jpeg"

// Limits bounds the work one cover may cost.
type Limits struct {
	// MaxPixels rejects a source image before it is decoded. Dimensions
	// come from the header first because a decoded pixel costs four bytes
	// whatever the file size: a small file declaring 30000x30000 is a
	// memory bomb no byte cap would catch.
	MaxPixels int
	// MaxDimension is the longest edge of the rendered cover. The aspect
	// ratio is kept, so a portrait cover stays taller than it is wide.
	MaxDimension int
	// Quality is the JPEG quality of the rendered cover.
	Quality int
}

// DefaultLimits is sized for a catalog grid and a book page on a phone.
func DefaultLimits() Limits {
	return Limits{
		// 40 megapixels is past any real cover and still bounded: the
		// decoded worst case is a few hundred megabytes, and one request
		// at a time reaches it.
		MaxPixels:    40_000_000,
		MaxDimension: 1000,
		Quality:      80,
	}
}

// Size names a rendered variant. Callers pass one of these rather than a
// pixel count, so the cache cannot be filled with one entry per arbitrary
// width a client asks for.
type Size string

const (
	// SizeThumbnail is the catalog grid.
	SizeThumbnail Size = "thumbnail"
	// SizeFull is a book page.
	SizeFull Size = "full"
)

// ParseSize maps a request parameter to a variant. An empty value is the
// thumbnail, because that is what a catalog asks for most.
func ParseSize(value string) (Size, bool) {
	switch value {
	case "", string(SizeThumbnail):
		return SizeThumbnail, true
	case string(SizeFull):
		return SizeFull, true
	default:
		return "", false
	}
}

// MaxDimension is the longest edge for one variant.
func (s Size) MaxDimension(limits Limits) int {
	if s == SizeThumbnail {
		// A thumbnail is a third of the full size rather than its own
		// number, so raising one limit raises both.
		if edge := limits.MaxDimension / 3; edge > 0 {
			return edge
		}
		return 1
	}
	return limits.MaxDimension
}

// Render decodes a cover and re-encodes it as a bounded JPEG.
func Render(source []byte, size Size, limits Limits) ([]byte, error) {
	if len(source) == 0 || limits.MaxPixels <= 0 || limits.MaxDimension <= 0 ||
		limits.Quality <= 0 || limits.Quality > 100 {
		return nil, ErrUnsupported
	}
	if _, ok := ParseSize(string(size)); !ok {
		return nil, ErrUnsupported
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		config.Width > limits.MaxPixels/config.Height {
		return nil, fmt.Errorf("%w: %dx%d", ErrUnsupported, config.Width, config.Height)
	}
	decoded, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, scale(decoded, size.MaxDimension(limits)),
		&jpeg.Options{Quality: limits.Quality}); err != nil {
		return nil, fmt.Errorf("encode cover: %w", err)
	}
	return out.Bytes(), nil
}

// scale reduces an image so its longest edge is at most maxEdge, averaging
// the source pixels that fall into each destination pixel.
//
// Averaging rather than sampling matters at these ratios: a cover shrunk
// to a tenth of its size by taking one pixel in ten throws away most of
// the image, and the title text on it turns to noise. It only ever
// shrinks — enlarging a small cover would spend bytes to add nothing.
func scale(src image.Image, maxEdge int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return src
	}
	dstW, dstH := width, height
	if width > maxEdge || height > maxEdge {
		if width >= height {
			dstW = maxEdge
			dstH = max(height*maxEdge/width, 1)
		} else {
			dstH = maxEdge
			dstW = max(width*maxEdge/height, 1)
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	if dstW == width && dstH == height {
		draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Src)
		return dst
	}
	for y := range dstH {
		// The source band this destination row covers. Using the row
		// edges rather than a centre point is what makes every source
		// pixel contribute to exactly one destination pixel.
		y0 := bounds.Min.Y + y*height/dstH
		y1 := bounds.Min.Y + (y+1)*height/dstH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := range dstW {
			x0 := bounds.Min.X + x*width/dstW
			x1 := bounds.Min.X + (x+1)*width/dstW
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, a, count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					sr, sg, sb, sa := src.At(sx, sy).RGBA()
					r += uint64(sr)
					g += uint64(sg)
					b += uint64(sb)
					a += uint64(sa)
					count++
				}
			}
			if count == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r / count >> 8), G: uint8(g / count >> 8),
				B: uint8(b / count >> 8), A: uint8(a / count >> 8),
			})
		}
	}
	return dst
}
