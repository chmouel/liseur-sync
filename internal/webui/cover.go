package webui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"sync"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// CoverServer serves a book's cover for a caller identified by a session
// cookie rather than a token. As with downloads, the rules about what may
// be served live on the other side of this interface and are not repeated
// here.
type CoverServer interface {
	ServeBookCover(w http.ResponseWriter, r *http.Request, userID, bookID string)
}

// handleBookCover shows a cover, or a placeholder when there is none.
//
// The API answers 404 for a book without a cover, which is the honest
// answer to a client that asked a question. A browser given the same
// answer draws a broken-image icon, and a shelf of sideloaded books —
// where missing covers are normal, not exceptional — would be a wall of
// them. So the API's answer is intercepted here and only here: the
// contract is unchanged, and the page gets something to draw.
func (s *Server) handleBookCover(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if s.Covers == nil {
		servePlaceholderCover(w, r)
		return
	}
	intercept := &coverFallbackWriter{ResponseWriter: w, request: r}
	s.Covers.ServeBookCover(intercept, r, u.ID, r.PathValue("id"))
	intercept.finish()
}

// coverFallbackWriter swallows the body of a failed cover response and
// draws a placeholder instead. Only the body is discarded: everything the
// handler decided about the book — whether it exists, whether the caller
// may see it — has already been enforced by the time it writes one.
type coverFallbackWriter struct {
	http.ResponseWriter
	request  *http.Request
	failed   bool
	finished bool
}

func (w *coverFallbackWriter) WriteHeader(status int) {
	if status < 400 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	// Nothing is deleted here. What survives a failed cover response is
	// the error's own Content-Type, and the placeholder replaces that
	// itself; the cover's ETag and caching headers are already gone,
	// because the only thing that can fail after setting them is
	// ServeContent, which clears them on its way out. Deleting them
	// again would be code no test could reach.
	w.failed = true
}

func (w *coverFallbackWriter) Write(p []byte) (int, error) {
	if w.failed {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

func (w *coverFallbackWriter) finish() {
	if !w.failed || w.finished {
		return
	}
	w.finished = true
	servePlaceholderCover(w.ResponseWriter, w.request)
}

// placeholderCover is a plain card the size of a thumbnail. It is built
// once because it never changes, and it is an image rather than an icon
// font or an SVG so that it is subject to the same nosniff rules as a
// real cover.
var placeholderCover = sync.OnceValue(func() []byte {
	const width, height = 120, 160
	card := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(card, card.Bounds(),
		&image.Uniform{C: color.RGBA{R: 0xe8, G: 0xe8, B: 0xea, A: 0xff}},
		image.Point{}, draw.Src)
	// A darker spine down the left edge, so the shape reads as a book
	// rather than as a failed image.
	draw.Draw(card, image.Rect(0, 0, 8, height),
		&image.Uniform{C: color.RGBA{R: 0xc4, G: 0xc4, B: 0xc8, A: 0xff}},
		image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, card); err != nil {
		// Encoding a small opaque image cannot fail; an empty body is a
		// broken image, which is what the placeholder exists to avoid,
		// but it is not worth a panic in a page handler.
		return nil
	}
	return encoded.Bytes()
})

func servePlaceholderCover(w http.ResponseWriter, r *http.Request) {
	body := placeholderCover()
	if r.Header.Get("Range") != "" {
		// The client asked for a range of a different, longer picture.
		// Applying it here would answer a request for a cover with a
		// range error about a placeholder.
		r = r.Clone(r.Context())
		r.Header.Del("Range")
		r.Header.Del("If-Range")
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Fixed content, so it is worth caching, but not as long as a real
	// cover: a book acquires one when its metadata is corrected.
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
