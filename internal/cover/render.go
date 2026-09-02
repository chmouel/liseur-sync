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
	"sync"

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
	// SizeIcon is a browser tab icon: square, because that is the hole
	// it is drawn into, and small, because nothing larger is ever
	// shown.
	SizeIcon Size = "icon"
)

// iconMaxDimension is the icon's edge. It is a constant rather than a
// Limits field for the same reason a thumbnail is a third of the full
// size: an operator's bounds are about source work, and an icon is a
// fixed shape of answer.
const iconMaxDimension = 64

// ParseSize maps a request parameter to a variant. An empty value is the
// thumbnail, because that is what a catalog asks for most.
func ParseSize(value string) (Size, bool) {
	switch value {
	case "", string(SizeThumbnail):
		return SizeThumbnail, true
	case string(SizeFull):
		return SizeFull, true
	case string(SizeIcon):
		return SizeIcon, true
	default:
		return "", false
	}
}

// MaxDimension is the longest edge for one variant.
func (s Size) MaxDimension(limits Limits) int {
	switch s {
	case SizeIcon:
		return iconMaxDimension
	case SizeThumbnail:
		// A thumbnail is a third of the full size rather than its own
		// number, so raising one limit raises both.
		if edge := limits.MaxDimension / 3; edge > 0 {
			return edge
		}
		return 1
	}
	return limits.MaxDimension
}

// square reports whether the variant is drawn into a square hole. Only
// the icon is: every other variant keeps the cover's own aspect ratio.
func (s Size) square() bool { return s == SizeIcon }

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
	if size.square() {
		decoded = centreCrop(decoded)
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, scale(decoded, size.MaxDimension(limits)),
		&jpeg.Options{Quality: limits.Quality}); err != nil {
		return nil, fmt.Errorf("encode cover: %w", err)
	}
	return out.Bytes(), nil
}

// centreCrop cuts the largest centred square out of an image. A tab icon
// is drawn square, and letting the browser squish a portrait cover into
// that hole reads worse than losing its left and right edges — a cover's
// centre is where the title art lives.
func centreCrop(src image.Image) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == height {
		return src
	}
	side := min(width, height)
	rect := image.Rect(
		bounds.Min.X+(width-side)/2, bounds.Min.Y+(height-side)/2,
		bounds.Min.X+(width-side)/2+side, bounds.Min.Y+(height-side)/2+side,
	)
	dst := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(dst, dst.Bounds(), src, rect.Min, draw.Src)
	return dst
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

// PlaceholderIcon is what a book without a cover gets when the asker
// needed an icon rather than an answer: a small card, built once,
// centered on a canvas the icon's own size. It is a JPEG because every
// rendered cover is one — a client of ?size=icon has exactly one type to
// accept, whatever the book had.
var PlaceholderIcon = sync.OnceValue(func() []byte {
	face := color.RGBA{R: 0x3a, G: 0x3d, B: 0x44, A: 0xff}
	spine := color.RGBA{R: 0x2a, G: 0x2c, B: 0x31, A: 0xff}
	canvas := image.NewRGBA(image.Rect(0, 0, iconMaxDimension, iconMaxDimension))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: spine}, image.Point{}, draw.Src)
	// A portrait card centred in the square, with a darker spine of its
	// own down the left edge, so it reads as a book at 16 pixels.
	card := image.Rect(16, 8, 48, 56)
	draw.Draw(canvas, card, &image.Uniform{C: face}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(16, 8, 22, 56), &image.Uniform{C: spine}, image.Point{}, draw.Src)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: 80}); err != nil {
		// Encoding a small opaque image cannot fail; nil makes the
		// caller fall back to its own 404, which is the honest answer
		// this variant exists to soften but not at any price.
		return nil
	}
	return out.Bytes()
})
