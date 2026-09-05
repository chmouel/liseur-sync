package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/live"
	"github.com/chmouel/liseur-sync/internal/store"
)

// EventsPolicy bounds a live stream (ADR-0034). The defaults are the
// contract: a heartbeat every 20 seconds, a client that gives up after
// 60 seconds of silence, and both comfortably under the idle timeout of
// any reverse proxy in front of this server.
type EventsPolicy struct {
	// Heartbeat is how often a comment frame goes out on an otherwise
	// idle stream.
	Heartbeat time.Duration
	// Revalidate is how often the credential is checked again, so a
	// revoked token does not keep receiving because it once connected.
	Revalidate time.Duration
	// WriteTimeout bounds a single frame. A client that stops reading
	// is disconnected rather than buffered.
	WriteTimeout time.Duration
	// RetryAdvice is the reconnect delay suggested to the client.
	RetryAdvice time.Duration
}

// DefaultEventsPolicy is used when Server.Events is left zero.
var DefaultEventsPolicy = EventsPolicy{
	Heartbeat:    20 * time.Second,
	Revalidate:   time.Minute,
	WriteTimeout: 10 * time.Second,
	RetryAdvice:  5 * time.Second,
}

func (p EventsPolicy) orDefault() EventsPolicy {
	d := DefaultEventsPolicy
	if p.Heartbeat <= 0 {
		p.Heartbeat = d.Heartbeat
	}
	if p.Revalidate <= 0 {
		p.Revalidate = d.Revalidate
	}
	if p.WriteTimeout <= 0 {
		p.WriteTimeout = d.WriteTimeout
	}
	if p.RetryAdvice <= 0 {
		p.RetryAdvice = d.RetryAdvice
	}
	return p
}

// liveTopicsFor is the authorization rule for the stream: the endpoint
// authenticates once and then filters. Demanding every scope would shut
// out the web reader, whose token carries sync and library-read and has
// no business holding insight authority.
func liveTopicsFor(scopes store.ScopeSet) []store.Topic {
	var out []store.Topic
	if scopes.Allows(store.ScopeSync) {
		out = append(out, store.TopicPositions, store.TopicAnnotations)
	}
	if scopes.Allows(store.ScopeReadInsights) {
		out = append(out, store.TopicInsights)
	}
	return out
}

// HandleEvents streams change notifications for as long as the client
// keeps listening. It carries no reading state: a frame names topics,
// and the client answers by reading the feeds it already knows.
func (s *Server) HandleEvents(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	// Authorization is decided before availability: whether this
	// credential could ever receive anything does not depend on whether
	// this build happens to have a hub.
	allowed := liveTopicsFor(tok.Scopes)
	if len(allowed) == 0 {
		writeError(w, http.StatusForbidden, "insufficient scope")
		return
	}
	if s.Live == nil {
		writeError(w, http.StatusServiceUnavailable, "live events are not enabled")
		return
	}
	policy := s.Events.orDefault()

	// Registered before anything is written, so a change committed
	// while this connection is being set up is still owed afterwards.
	sub, err := s.Live.Subscribe(tok.UserID, allowed)
	if err != nil {
		if errors.Is(err, live.ErrTooManyStreams) {
			w.Header().Set("Retry-After", strconv.Itoa(int(policy.RetryAdvice.Seconds())))
			writeError(w, http.StatusTooManyRequests, "too many live streams for this account")
			return
		}
		writeError(w, http.StatusForbidden, "insufficient scope")
		return
	}
	defer sub.Close()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	// Nginx buffers text/event-stream by default and would hold every
	// frame until the stream ended, which is the whole point missed.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	send := func(b []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(policy.WriteTimeout))
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	if !send([]byte("retry: " + strconv.Itoa(int(policy.RetryAdvice.Milliseconds())) + "\n\n")) {
		return
	}

	// The invalidation every connection opens with. A client that has
	// been offline, or that has just been told its cursor is stale,
	// needs no special case and sends no `since`.
	sub.Raise(allowed...)

	secret, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	heartbeat := time.NewTicker(policy.Heartbeat)
	defer heartbeat.Stop()
	revalidate := time.NewTicker(policy.Revalidate)
	defer revalidate.Stop()

	// A stream is not a longer-lived credential: it ends when the token
	// does, whatever it was authorized to do an hour ago.
	var expiry <-chan time.Time
	if tok.ExpiresAt != nil {
		t := time.NewTimer(time.Until(*tok.ExpiresAt))
		defer t.Stop()
		expiry = t.C
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.Done():
			return
		case <-expiry:
			return
		case <-revalidate.C:
			// Once a 200 has been written this cannot become a 401.
			// End the stream and let the reconnect be refused in the
			// ordinary way.
			if !s.liveStillAuthorized(r.Context(), secret, allowed) {
				return
			}
			if !send([]byte(": ok\n\n")) {
				return
			}
		case <-heartbeat.C:
			if !s.liveStillAuthorized(r.Context(), secret, allowed) {
				return
			}
			if !send([]byte(": ping\n\n")) {
				return
			}
		case <-sub.Wake():
			owed := sub.Take()
			if len(owed) == 0 {
				continue // spurious wake-up; an empty frame says nothing
			}
			if !s.liveStillAuthorized(r.Context(), secret, allowed) {
				return
			}
			frame, err := invalidateFrame(owed)
			if err != nil || !send(frame) {
				return
			}
		}
	}
}

// liveStillAuthorized re-reads the credential through the ordinary auth
// path, so revocation, expiry and a demoted admin all end a stream the
// same way they refuse a request.
func (s *Server) liveStillAuthorized(ctx context.Context, secret string, allowed []store.Topic) bool {
	if secret == "" {
		return false
	}
	tok, err := s.Auth.AuthenticateToken(ctx, secret)
	if err != nil {
		return false
	}
	still := liveTopicsFor(tok.Scopes)
	for _, want := range allowed {
		found := false
		for _, have := range still {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func invalidateFrame(owed []store.Topic) ([]byte, error) {
	payload, err := json.Marshal(struct {
		Topics []store.Topic `json:"topics"`
	}{owed})
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("event: invalidate\ndata: ")
	b.Write(payload)
	b.WriteString("\n\n")
	return []byte(b.String()), nil
}
