package cover

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// checkerboard is a source with structure: an image scaled by sampling
// rather than averaging keeps the extremes, while one that averages lands
// on the mean. That difference is what the scaling test measures.
func checkerboard(t *testing.T, width, height, cell int) image.Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			shade := uint8(0)
			if (x/cell+y/cell)%2 == 0 {
				shade = 255
			}
			img.SetRGBA(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Fatalf("rendered format = %q, want jpeg", format)
	}
	return img
}

// TestRenderBoundsTheLongestEdge: the point of rendering at all is that a
// phone drawing a grid of thumbnails is not sent publisher-sized images.
func TestRenderBoundsTheLongestEdge(t *testing.T) {
	limits := DefaultLimits()
	// Both orientations: a portrait cover is the common case, but a
	// landscape one takes the other branch, and squaring it off there
	// would go unnoticed.
	for _, shape := range []struct {
		name          string
		width, height int
	}{
		{"portrait", 1600, 2400},
		{"landscape", 2400, 1600},
	} {
		source := encodePNG(t, checkerboard(t, shape.width, shape.height, 8))
		for _, tc := range []struct {
			size Size
			edge int
		}{
			{SizeThumbnail, limits.MaxDimension / 3},
			{SizeFull, limits.MaxDimension},
		} {
			t.Run(shape.name+"/"+string(tc.size), func(t *testing.T) {
				out, err := Render(source, tc.size, limits)
				if err != nil {
					t.Fatal(err)
				}
				bounds := decode(t, out).Bounds()
				long, short := bounds.Dy(), bounds.Dx()
				srcLong, srcShort := shape.height, shape.width
				if shape.width > shape.height {
					long, short = short, long
					srcLong, srcShort = srcShort, srcLong
				}
				if long != tc.edge {
					t.Fatalf("longest edge = %d, want %d", long, tc.edge)
				}
				// A 2:3 cover must stay one. One pixel of slack for the
				// integer division.
				want := tc.edge * srcShort / srcLong
				if short < want-1 || short > want+1 {
					t.Fatalf("short edge = %d, want %d (aspect ratio lost)", short, want)
				}
			})
		}
	}
}

// stripes varies along one axis only, so it isolates that axis: an image
// scaled by sampling columns keeps vertical stripes at full contrast, and
// one that averages them lands on the mean.
func stripes(t *testing.T, width, height, period int, vertical bool) image.Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			axis := y
			if vertical {
				axis = x
			}
			shade := uint8(0)
			if (axis/period)%2 == 0 {
				shade = 255
			}
			img.SetRGBA(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	return img
}

// extremeFraction is the share of pixels that stayed near pure black or
// pure white. It is the measurement that separates the two behaviours: an
// average of alternating black and white pixels is mid grey everywhere,
// while picking one of them keeps every pixel at an extreme. A mean over
// the whole image cannot tell them apart, because sampled extremes
// average out to the same mid grey.
func extremeFraction(t *testing.T, img image.Image) float64 {
	t.Helper()
	bounds := img.Bounds()
	var extreme, count int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			grey := int(r >> 8)
			if grey < 48 || grey > 207 {
				extreme++
			}
			count++
		}
	}
	return float64(extreme) / float64(count)
}

// TestRenderAveragesInsteadOfSampling: a cover reduced by taking one pixel
// in ten keeps a tenth of the image and drops the rest, which turns the
// title text on most covers into noise. Each axis is checked on its own,
// because averaging rows while sampling columns is just as wrong and
// looks fine on a checkerboard.
func TestRenderAveragesInsteadOfSampling(t *testing.T) {
	for name, vertical := range map[string]bool{
		"columns": true, "rows": false,
	} {
		t.Run(name, func(t *testing.T) {
			// One-pixel stripes: every destination pixel covers both a
			// black and a white source pixel, so averaging can only
			// produce mid grey.
			source := encodePNG(t, stripes(t, 900, 900, 1, vertical))
			out, err := Render(source, SizeThumbnail, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if got := extremeFraction(t, decode(t, out)); got > 0.05 {
				t.Fatalf("%.0f%% of pixels stayed at an extreme: %s were sampled, not averaged",
					got*100, name)
			}
		})
	}
}

// TestRenderDoesNotEnlarge: a 200-pixel cover blown up to 1000 costs
// bytes and adds no detail.
func TestRenderDoesNotEnlarge(t *testing.T) {
	source := encodePNG(t, checkerboard(t, 120, 180, 4))
	out, err := Render(source, SizeFull, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if bounds := decode(t, out).Bounds(); bounds.Dx() != 120 || bounds.Dy() != 180 {
		t.Fatalf("bounds = %v, want the source size", bounds)
	}
}

// TestRenderAcceptsEveryFormatItAdvertises: the registered decoders are
// the allowlist, so each has to actually work.
func TestRenderAcceptsTheFormatsItRegisters(t *testing.T) {
	img := checkerboard(t, 40, 60, 4)
	var jpg, gifBuf bytes.Buffer
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifBuf, img, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"png": encodePNG(t, img), "jpeg": jpg.Bytes(), "gif": gifBuf.Bytes(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(data, SizeThumbnail, DefaultLimits()); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

// TestRenderRefusesWhatItCannotDecode: an SVG cover is common and is not a
// raster image. Passing those bytes through would serve
// publisher-controlled markup from the server's own origin.
func TestRenderRefusesWhatItCannotDecode(t *testing.T) {
	for name, data := range map[string][]byte{
		"svg":   []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`),
		"html":  []byte("<!doctype html><script>alert(1)</script>"),
		"empty": nil,
		"junk":  bytes.Repeat([]byte{0xff}, 512),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(data, SizeThumbnail, DefaultLimits()); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("%s: err = %v, want ErrUnsupported", name, err)
			}
		})
	}
}

// TestRenderRefusesAPixelBomb: a few kilobytes of PNG can declare an image
// whose decoded form is hundreds of megabytes, so the header is checked
// before anything is decoded. A byte cap cannot catch this.
func TestRenderRefusesAPixelBomb(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxPixels = 10_000
	source := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 400, 400)))
	if _, err := Render(source, SizeThumbnail, limits); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported for 160000 pixels", err)
	}
	limits.MaxPixels = 1 << 20
	if _, err := Render(source, SizeThumbnail, limits); err != nil {
		t.Fatalf("a cover inside the pixel limit was refused: %v", err)
	}
}

func TestRenderRejectsInvalidLimitsAndSizes(t *testing.T) {
	source := encodePNG(t, checkerboard(t, 40, 40, 4))
	for name, limits := range map[string]Limits{
		"no pixels":    {MaxPixels: 0, MaxDimension: 100, Quality: 80},
		"no dimension": {MaxPixels: 1 << 20, MaxDimension: 0, Quality: 80},
		"no quality":   {MaxPixels: 1 << 20, MaxDimension: 100, Quality: 0},
		"over quality": {MaxPixels: 1 << 20, MaxDimension: 100, Quality: 101},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(source, SizeThumbnail, limits); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("%s: err = %v", name, err)
			}
		})
	}
	if _, err := Render(source, Size("enormous"), DefaultLimits()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("an unknown size was rendered: %v", err)
	}
}

// TestParseSizeIsAnAllowlist: the variant ends up in a cache path, and it
// decides how much work a request costs. A client naming its own must not
// be able to do either.
func TestParseSizeIsAnAllowlist(t *testing.T) {
	for value, want := range map[string]Size{
		"":          SizeThumbnail,
		"thumbnail": SizeThumbnail,
		"full":      SizeFull,
	} {
		got, ok := ParseSize(value)
		if !ok || got != want {
			t.Fatalf("ParseSize(%q) = %q, %v", value, got, ok)
		}
	}
	for _, value := range []string{"THUMBNAIL", "../../etc", "4000", "large"} {
		if _, ok := ParseSize(value); ok {
			t.Fatalf("ParseSize(%q) was accepted", value)
		}
	}
}
