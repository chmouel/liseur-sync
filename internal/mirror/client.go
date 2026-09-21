// Package mirror speaks KOReader's sync protocol outwards, to a peer
// server that serves the same library from the same disk (ADR-0047).
//
// It is the mirror image of internal/adapter/kosync, and the same rule
// holds in reverse: nothing legacy is stored. A position that arrives
// from the peer becomes a native op before it touches the store, and a
// position that leaves is built from one.
//
// The client below speaks only the three calls stock kosync defines —
// authenticate, put a position, get a position. That is deliberate.
// BookOrbit has a richer surface behind the same credential, but those
// routes are its own and move with it, while these three are KOReader's
// contract and are therefore the part any peer can be relied on to
// implement.
package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
)

// maxResponseBytes bounds what a peer can make this server read. A
// kosync position is a few hundred bytes; anything approaching this is
// not a reply to the call we made.
const maxResponseBytes = 1 << 20

var (
	// ErrUnauthorized is the peer refusing the configured credential.
	// It is not retried differently from any other failure, but it is
	// worth naming, because it is the one failure an operator can fix.
	ErrUnauthorized = errors.New("mirror: the peer rejected the credential")
	// ErrNoPosition is the peer having nothing for a document. It is an
	// ordinary answer, not a failure: most books are known to only one
	// side.
	ErrNoPosition = errors.New("mirror: the peer has no position for this document")
)

// Position is kosync's position, in both directions. The percentage is
// the fraction of the book, 0..1 — KOReader's scale, and the only part
// of a position that survives the crossing. The progress string is
// whatever the writing engine uses to mean a place: an xpointer for a
// reflowable document, a bare page number for a paged one. This server
// carries it verbatim and never interprets it.
type Position struct {
	Document   string  `json:"document"`
	Progress   string  `json:"progress"`
	Percentage float64 `json:"percentage"`
	Device     string  `json:"device,omitempty"`
	DeviceID   string  `json:"device_id,omitempty"`
	Timestamp  int64   `json:"timestamp,omitempty"`
}

// Time is when the peer says the position was reached.
func (p Position) Time() time.Time { return time.Unix(p.Timestamp, 0).UTC() }

// resetPositions are the two position strings BookOrbit answers with
// while a reading reset is outstanding: the first fragment of a
// reflowable document, or page one of a paged one.
var resetPositions = map[string]bool{
	"/body/DocFragment[1]/body": true,
	"1":                         true,
}

// IsReset reports whether this reply is a standing instruction to go
// back to the beginning rather than a position somebody read to.
//
// The peer answers a pull this way while a reading reset is
// outstanding, and keeps answering it until every device it knows about
// has pushed back at the start. It stamps the reply with the current
// time, so the newest-wins rule would hand it the argument every time,
// and it attributes the reply to its own web reader — the same device
// and device_id a genuine web position carries. So there is nothing in
// the envelope to tell the two apart, and the only signal left is the
// position itself: the very start of the book, at zero.
//
// A mirror that applied this would send the reader to page one and go
// on doing it on every poll. Refusing it costs at most a real reset
// that has to be made twice, once on each side. That is the cheaper
// mistake by a wide margin, so this is refused rather than guessed at.
func (p Position) IsReset() bool {
	return p.Percentage == 0 && resetPositions[strings.TrimSpace(p.Progress)]
}

// Client is one peer. It holds a credential it must present rather than
// one it verifies, so the credential is in memory in the clear; it is
// never logged, never returned and never put in a URL.
type Client struct {
	baseURL  string
	user     string
	key      string
	deviceID string
	http     *http.Client
}

// NewClient builds a client for an already-validated config. The
// caller has established that the mirror is enabled and complete;
// config.Validate is where a half-configured peer is refused.
func NewClient(cfg config.MirrorConfig) *Client {
	return &Client{
		baseURL:  strings.TrimSuffix(cfg.BaseURL, "/"),
		user:     cfg.RemoteUser,
		key:      cfg.RemoteKey,
		deviceID: cfg.DeviceID,
		http: &http.Client{
			Timeout: cfg.Timeout.Duration(),
			// A redirect would re-send the credential to wherever the
			// peer pointed, which is not a decision a peer gets to
			// make. The 3xx is surfaced as an error instead.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// DeviceID is the name this server writes under on the peer, and so
// also how it recognises its own writing coming back.
func (c *Client) DeviceID() string { return c.deviceID }

// Authorize checks the credential. Worth doing once at startup: a
// mirror with a bad password otherwise fails silently in a background
// goroutine, one book at a time.
func (c *Client) Authorize(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/users/auth", nil)
	return err
}

// Push writes a position to the peer. The device fields are this
// server's own, whatever the op said: the peer is being told that
// liseur-sync moved, and attributing the write to the Android phone
// that actually did the reading would make the echo unrecognisable.
func (c *Client) Push(ctx context.Context, p Position) error {
	if strings.TrimSpace(p.Document) == "" {
		return errors.New("mirror: a position with no document")
	}
	if p.Percentage < 0 || p.Percentage > 1 {
		return fmt.Errorf("mirror: percentage %v is outside 0..1", p.Percentage)
	}
	p.Document = strings.ToLower(strings.TrimSpace(p.Document))
	p.Device = c.deviceID
	p.DeviceID = c.deviceID
	if p.Timestamp == 0 {
		p.Timestamp = time.Now().Unix()
	}
	if p.Progress == "" {
		// KOReader's own plugin feeds this value straight to the
		// reader with no percentage fallback, and a peer rejects a
		// position it cannot act on. The percentage as a string is
		// what this server already answers its own non-CRe clients
		// with, so it is a position both ends already understand.
		p.Progress = strconv.FormatFloat(p.Percentage, 'f', -1, 64)
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPut, "/syncs/progress", body)
	return err
}

// Pull asks the peer where a document is. ErrNoPosition means the peer
// knows the book but has nothing to say about it, or does not know it
// at all; the two are the same empty reply on the wire and the same
// nothing to this server.
func (c *Client) Pull(ctx context.Context, document string) (Position, error) {
	document = strings.ToLower(strings.TrimSpace(document))
	if document == "" {
		return Position{}, errors.New("mirror: a pull with no document")
	}
	raw, err := c.do(ctx, http.MethodGet,
		"/syncs/progress/"+url.PathEscape(document), nil)
	if err != nil {
		return Position{}, err
	}
	var p Position
	if err := json.Unmarshal(raw, &p); err != nil {
		return Position{}, fmt.Errorf("mirror: unreadable reply from the peer: %w", err)
	}
	// An unknown document is an empty object, not a 404.
	if p.Document == "" && p.Progress == "" && p.Percentage == 0 && p.Timestamp == 0 {
		return Position{}, ErrNoPosition
	}
	if p.Percentage < 0 || p.Percentage > 1 {
		return Position{}, fmt.Errorf("mirror: the peer sent percentage %v, outside 0..1", p.Percentage)
	}
	if p.Document == "" {
		p.Document = document
	}
	return p, nil
}

// do makes one request, presenting the credential in the two headers
// KOReader uses. It returns the body, bounded.
func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-auth-user", c.user)
	req.Header.Set("x-auth-key", c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "liseur-sync")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// A transport error can name the request URL but never a
		// header, so there is no credential in here to redact.
		return nil, fmt.Errorf("mirror: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))

	switch {
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound && method == http.MethodGet:
		return nil, ErrNoPosition
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("mirror: %s %s: peer answered %s: %s",
			method, path, resp.Status, firstLine(raw))
	}
	if readErr != nil {
		return nil, fmt.Errorf("mirror: %s %s: %w", method, path, readErr)
	}
	return raw, nil
}

// firstLine keeps a peer's error out of the log in bulk. Something is
// wrong at the other end and a line of it is the useful part.
func firstLine(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "(empty body)"
	}
	return s
}
