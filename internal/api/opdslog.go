package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/chmouel/liseur-sync/internal/auth"
)

// opdsLogKey carries what the OPDS logging middleware needs to know about
// a request it is not itself allowed to read. The token, in particular,
// only exists inside the authenticated request the Basic middleware
// derives, so the value travels as a pointer the inner handler fills in.
type opdsLogKey struct{}

// opdsRequestLog is the slice of a request that outlives it: which
// credential asked, and nothing about the secret it presented.
type opdsRequestLog struct {
	TokenID string
}

// opdsLogging reports an OPDS request that was refused or failed.
//
// It exists because this surface is mostly a black box in the field: an
// e-reader that reports "parse feed failed" tells the reader nothing
// about whether the server answered 401, 429 or 500, and does not say
// which URL it asked for. A WARN line with the status, the route pattern
// and the credential's id is what turns that into a diagnosis. Successful
// catalog reads stay unlogged — a shelf is a request per visible book,
// and a log of them is a log of what somebody is reading.
//
// The pattern rather than the raw path is logged, so a redacted log is
// still a complete one: no folder id, no book id, and never the secret,
// which appears in no branch of this code.
func (s *Server) opdsLogging(pattern string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &opdsRequestLog{}
		rec := &opdsStatusWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(
			context.WithValue(r.Context(), opdsLogKey{}, state)))
		if rec.status < 400 {
			return
		}
		slog.Warn("opds request failed",
			"status", rec.status,
			"method", r.Method,
			"route", pattern,
			"token_id", state.TokenID,
			"ip", auth.ClientIP(r, s.Cfg),
		)
	})
}

// opdsTokenCapture records which credential reached the handler. It sits
// inside the Basic middleware, which is the only place the token exists,
// and outside both the scope check and the handler, either of which may
// refuse for its own reasons.
func opdsTokenCapture(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok, ok := auth.TokenFrom(r); ok {
			if state, ok := r.Context().Value(opdsLogKey{}).(*opdsRequestLog); ok {
				state.TokenID = tok.ID
			}
		}
		next.ServeHTTP(w, r)
	})
}

// opdsStatusWriter remembers the status the handler chose. It forwards the
// optional interfaces a download or a cover needs: without ReadFrom,
// http.ServeContent gives up its copy fast path, and without Flush a
// streaming response would sit in a buffer until the handler returned.
type opdsStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *opdsStatusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *opdsStatusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *opdsStatusWriter) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(w.ResponseWriter, src)
}

func (w *opdsStatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap is what http.ResponseController follows to reach the underlying
// writer, so a handler that needs an interface this wrapper does not
// implement still finds it.
func (w *opdsStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
