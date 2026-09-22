package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
)

// This file is BookOrbit's own REST API: its shapes, its credential and
// its routes. It is kept apart from the protocol built on it because
// it is the part that moves. BookOrbit versions its API as a fixed
// /api/v1 prefix and has shipped breaking changes under it within a
// release, so everything here decodes leniently — unknown fields are
// ignored, absent fields are absent rather than zero, and no reply is
// trusted to have the shape it had last week.

// maxCatalogBytes bounds a catalog reply. A page of book cards carries
// every piece of metadata BookOrbit holds, which is far more than a
// position, so it gets a far wider bound than maxResponseBytes.
const maxCatalogBytes = 8 << 20

// accessSkew is how long before an access token actually expires it is
// treated as expired. BookOrbit issues them for fifteen minutes, and a
// request that leaves here valid and arrives expired costs a needless
// round trip through the refusal path.
const accessSkew = time.Minute

// errPeerNotFound is a 404 from the peer, whatever it meant.
var errPeerNotFound = errors.New("the peer has no such route or record")

// bookOrbitAPI holds one peer's session.
//
// The credential it starts from is the peer account's password, which
// is what BookOrbit offers a native client and is the reason this
// protocol is not the default. Everything derived from it — the access
// token, the refresh token, the session id — lives in this struct and
// nowhere else: not in the database, not in a log line, not in an
// error returned to a caller.
type bookOrbitAPI struct {
	baseURL  string
	user     string
	password string
	label    string
	http     *http.Client
	log      *slog.Logger

	// renewing serializes getting a new session. The refresh token
	// rotates on every use, so two callers spending the same one
	// leaves at most one of them with a session and the peer with a
	// login it will not honour again.
	renewing sync.Mutex

	mu         sync.Mutex
	access     string
	accessExp  time.Time
	refresh    string
	refreshExp time.Time
	session    json.Number
}

func newBookOrbitAPI(cfg config.MirrorConfig, log *slog.Logger) *bookOrbitAPI {
	if log == nil {
		log = slog.Default()
	}
	return &bookOrbitAPI{
		baseURL:  strings.TrimSuffix(cfg.BaseURL, "/"),
		user:     cfg.RemoteUser,
		password: cfg.RemotePassword,
		label:    cfg.DeviceID,
		log:      log,
		http: &http.Client{
			Timeout: cfg.Timeout.Duration(),
			// A redirect would re-send the bearer token to wherever
			// the peer pointed, which is not a decision a peer gets to
			// make.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// credentials is what BookOrbit answers a native login or refresh
// with. A web client gets cookies and a thinner body; asking for
// clientKind "native" is what makes the refresh token something this
// server can hold rather than something a browser jar holds.
type credentials struct {
	AccessToken           string      `json:"accessToken"`
	AccessTokenExpiresAt  time.Time   `json:"accessTokenExpiresAt"`
	RefreshToken          string      `json:"refreshToken"`
	RefreshTokenExpiresAt time.Time   `json:"refreshTokenExpiresAt"`
	SessionID             json.Number `json:"sessionId"`
}

// login exchanges the password for a session. It is called once at
// startup and again whenever a refresh is refused, which is the only
// way back from a rotated-out or revoked session.
func (a *bookOrbitAPI) login(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{
		"username":    a.user,
		"password":    a.password,
		"clientKind":  "native",
		"deviceLabel": a.label,
	})
	if err != nil {
		return err
	}
	var creds credentials
	if err := a.call(ctx, http.MethodPost, "/auth/login", body, &creds, ""); err != nil {
		return err
	}
	return a.keep(creds)
}

// renew trades the refresh token for a new pair. BookOrbit rotates
// the refresh token on every use, so the reply's token replaces the
// one just spent and losing the reply loses the session.
func (a *bookOrbitAPI) renew(ctx context.Context, token string) error {
	body, err := json.Marshal(map[string]string{"refreshToken": token})
	if err != nil {
		return err
	}
	var creds credentials
	if err := a.call(ctx, http.MethodPost, "/auth/refresh", body, &creds, ""); err != nil {
		return err
	}
	return a.keep(creds)
}

func (a *bookOrbitAPI) keep(c credentials) error {
	if c.AccessToken == "" {
		return fmt.Errorf("mirror: the peer returned no access token")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.access, a.accessExp = c.AccessToken, c.AccessTokenExpiresAt
	if c.RefreshToken != "" {
		a.refresh, a.refreshExp = c.RefreshToken, c.RefreshTokenExpiresAt
	}
	if c.SessionID != "" {
		a.session = c.SessionID
	}
	return nil
}

// token returns an access token that is good now, getting one however
// it has to: the one in hand, a refresh of it, or a fresh login.
func (a *bookOrbitAPI) token(ctx context.Context) (string, error) {
	if access, ok := a.current(); ok {
		return access, nil
	}
	a.renewing.Lock()
	defer a.renewing.Unlock()
	// Somebody may have got one while this was waiting its turn.
	if access, ok := a.current(); ok {
		return access, nil
	}

	a.mu.Lock()
	refresh, refreshExp := a.refresh, a.refreshExp
	a.mu.Unlock()

	now := time.Now()
	if refresh != "" && (refreshExp.IsZero() || now.Before(refreshExp)) {
		if err := a.renew(ctx, refresh); err == nil {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.access, nil
		} else if ctx.Err() != nil {
			return "", err
		}
		// A refused refresh is ordinary: the session may have been
		// revoked, or rotated past this server by a lost reply. The
		// password is still good, so start again rather than give up.
		a.log.Info("mirror refreshing the peer session failed, signing in again")
	}
	if err := a.login(ctx); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.access, nil
}

// redact removes anything secret from text on its way to a log line,
// an error string or the cursor. It is a last line rather than a first
// one: nothing here is supposed to quote a credential in the first
// place, and this is what makes that true of text this server did not
// write.
func (a *bookOrbitAPI) redact(text string) string {
	a.mu.Lock()
	secrets := []string{a.access, a.refresh}
	a.mu.Unlock()
	secrets = append(secrets, a.password)
	// Every secret, however short. A short password would mangle the
	// message it appears in, which is a poor error and a fair trade:
	// the alternative is a password in the log and in the database
	// because somebody chose a six-letter one.
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	return text
}

// current returns the access token in hand, if it is still good for
// long enough to be worth sending.
func (a *bookOrbitAPI) current() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.access == "" {
		return "", false
	}
	if !a.accessExp.IsZero() && !time.Now().Add(accessSkew).Before(a.accessExp) {
		return "", false
	}
	return a.access, true
}

// logout ends the session on the peer, so restarts do not leave a
// trail of live sessions on somebody's account.
func (a *bookOrbitAPI) logout(ctx context.Context) {
	a.mu.Lock()
	token := a.refresh
	a.access, a.refresh, a.session = "", "", ""
	a.mu.Unlock()
	if token == "" {
		return
	}
	body, err := json.Marshal(map[string]string{"refreshToken": token})
	if err != nil {
		return
	}
	if err := a.call(ctx, http.MethodPost, "/auth/logout", body, nil, ""); err != nil {
		a.log.Debug("mirror could not sign out of the peer", "error", err)
	}
}

// appInfo is a cheap "which BookOrbit is this" once a session exists.
// It is not a public route, so it cannot be a preflight; it is read
// after signing in so that a pass broken by the peer changing under us
// has the peer's version beside it in the log.
func (a *bookOrbitAPI) appInfo(ctx context.Context) (string, error) {
	var info struct {
		Version string `json:"version"`
	}
	if err := a.get(ctx, "/app-info", &info); err != nil {
		return "", err
	}
	return info.Version, nil
}

func (a *bookOrbitAPI) get(ctx context.Context, path string, out any) error {
	return a.authed(ctx, http.MethodGet, path, nil, out)
}

func (a *bookOrbitAPI) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return a.authed(ctx, http.MethodPost, path, raw, out)
}

// authed makes one request with a bearer token, and makes it exactly
// twice if the first is refused: a token can expire between the check
// and the call, and one retry with a fresh session is the difference
// between a pass that works and a pass that fails every time the
// fifteen minutes happen to land badly.
func (a *bookOrbitAPI) authed(ctx context.Context, method, path string, body []byte, out any) error {
	token, err := a.token(ctx)
	if err != nil {
		return err
	}
	err = a.call(ctx, method, path, body, out, token)
	if err == nil || !errors.Is(err, ErrUnauthorized) {
		return err
	}
	a.mu.Lock()
	// Only the token that was refused: another caller may have
	// already replaced it with one that works, and throwing that away
	// would start a second session to answer a first one's problem.
	if a.access == token {
		a.access, a.accessExp = "", time.Time{}
	}
	a.mu.Unlock()
	if token, err = a.token(ctx); err != nil {
		return err
	}
	return a.call(ctx, method, path, body, out, token)
}

// call makes one request and decodes the reply into out, which may be
// nil for a route that answers with nothing worth reading.
func (a *bookOrbitAPI) call(
	ctx context.Context, method, path string, body []byte, out any, token string,
) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "liseur-sync")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := a.http.Do(req)
	if err != nil {
		// A transport error can name the request URL but never a
		// header or a body, so there is no credential in here.
		return fmt.Errorf("mirror: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))

	switch {
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		// Left as its own error rather than translated here: a book
		// the peer does not have and a base URL that is wrong both
		// arrive as a 404, and only the caller knows which it asked
		// for. Deciding it down here would turn a misconfigured peer
		// into a mirror that quietly finds nothing, forever.
		return fmt.Errorf("mirror: %s %s: %w", method, path, errPeerNotFound)
	case resp.StatusCode == http.StatusTooManyRequests:
		// Only BookOrbit's sign-in routes are throttled, so this is
		// the password path being retried too fast. It is a plain
		// failure and the mirror's backoff is what answers it.
		return fmt.Errorf("mirror: %s %s: the peer is rate limiting sign-in", method, path)
	case resp.StatusCode >= 300:
		// A peer's own message about what it disliked is usually the
		// fastest way to the answer, so it is quoted — but never from
		// a sign-in, whose request carried the password, and never
		// with a secret left in it. This error is logged and stored on
		// the cursor, and a peer or a proxy that echoes what it was
		// sent must not be able to put a credential in either.
		if strings.HasPrefix(path, "/auth/") {
			return fmt.Errorf("mirror: %s %s: peer answered %s", method, path, resp.Status)
		}
		return fmt.Errorf("mirror: %s %s: peer answered %s: %s",
			method, path, resp.Status, a.redact(firstLine(raw)))
	}
	if readErr != nil {
		return fmt.Errorf("mirror: %s %s: %w", method, path, readErr)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("mirror: %s %s: unreadable reply from the peer: %w", method, path, err)
	}
	return nil
}
