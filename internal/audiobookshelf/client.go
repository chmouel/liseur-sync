// Package audiobookshelf follows audiobooks on one Audiobookshelf server
// (ADR-0055). Listening and reading positions cross through matching
// chapters, and listening sessions are imported into statistics.
//
// It is not a mirror protocol: an audiobook and an EPUB share no bytes,
// so nothing here joins on a fingerprint, and listening never goes
// through kosync's inference path.
package audiobookshelf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/config"
)

// maxResponseBytes bounds what the server can make this one read. The
// largest reply asked for is a page of library items.
const maxResponseBytes = 8 << 20

var (
	// ErrUnauthorized is the server refusing the configured key.
	ErrUnauthorized = errors.New("audiobookshelf: the server rejected the API key")
	// ErrNotFound is an item, or an item's progress, that does not exist.
	ErrNotFound         = errors.New("audiobookshelf: not found")
	ErrMalformedSession = errors.New("audiobookshelf: malformed session")
	ErrFutureSession    = errors.New("audiobookshelf: future-dated session")
)

// Client is one Audiobookshelf server, spoken to with one API key. The
// key is only ever put in the Authorization header, never in a URL or
// an error.
type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

// NewClient builds a client for an already-validated config.
func NewClient(cfg config.AudiobookshelfConfig) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		key:     cfg.APIKey,
		http: &http.Client{
			Timeout: cfg.Timeout.Duration(),
			// A redirect would carry the key to wherever the server
			// pointed. The 3xx is surfaced as an error instead.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// PeerIdentity names one listening account on one server, as ADR-0048
// names a mirror peer: everything the integration writes is keyed by
// it, so pointing the integration at another server or account starts
// afresh instead of mixing two histories.
func PeerIdentity(baseURL, userID string) string {
	sum := sha256.Sum256([]byte("audiobookshelf\n" + strings.TrimSuffix(baseURL, "/") + "\n" + userID))
	return hex.EncodeToString(sum[:8])
}

// BaseURL is the server root this client speaks to.
func (c *Client) BaseURL() string { return c.baseURL }

// Me is the listening account and all of its progress.
type Me struct {
	ID            string     `json:"id"`
	Username      string     `json:"username"`
	MediaProgress []Progress `json:"mediaProgress"`
}

// Progress is where the account is in one item. Times are seconds;
// LastUpdate is milliseconds since the epoch.
type Progress struct {
	LibraryItemID string  `json:"libraryItemId"`
	EpisodeID     *string `json:"episodeId"`
	Duration      float64 `json:"duration"`
	Progress      float64 `json:"progress"`
	CurrentTime   float64 `json:"currentTime"`
	IsFinished    bool    `json:"isFinished"`
	LastUpdate    int64   `json:"lastUpdate"`
}

// maxClockSkew is how far into the future a remote timestamp may be
// before it is treated as nonsense rather than a fast clock.
const maxClockSkew = 10 * time.Minute

// minTimestampMs is 2000-01-01; an earlier timestamp is not a real one.
const minTimestampMs = 946684800000

const maxTimestampMs = 253402300799999

// Validate refuses a progress row that cannot be mapped honestly.
func (p Progress) Validate(now time.Time) error {
	switch {
	case p.LibraryItemID == "" || !storable(p.LibraryItemID):
		return errors.New("no item id")
	case p.EpisodeID != nil && *p.EpisodeID != "":
		return errors.New("a podcast episode")
	case !finite(p.Progress) || p.Progress < 0 || p.Progress > 1:
		return fmt.Errorf("progress %v out of range", p.Progress)
	case !finite(p.Duration) || p.Duration <= 0:
		return fmt.Errorf("duration %v", p.Duration)
	case !finite(p.CurrentTime) || p.CurrentTime < 0 || p.CurrentTime > p.Duration+1:
		return fmt.Errorf("currentTime %v outside 0..%v", p.CurrentTime, p.Duration)
	case p.LastUpdate < minTimestampMs || p.LastUpdate > now.Add(maxClockSkew).UnixMilli():
		return fmt.Errorf("lastUpdate %d out of range", p.LastUpdate)
	}
	return nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// storable says a server string can go into a PostgreSQL TEXT column,
// which refuses NUL and invalid UTF-8 that SQLite would keep.
func storable(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }

func cleanText(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "")
}

// Chapter is one chapter of an audiobook, in seconds.
type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

// AudioFile is one underlying audio file of an audiobook item.
type AudioFile struct {
	Ino      string
	Duration float64
	MimeType string
	Size     int64
	Ext      string
}

// Item is one library item with what mapping and matching need.
type Item struct {
	ID         string
	MediaType  string
	Title      string
	Subtitle   string
	Authors    []string
	Duration   float64
	Chapters   []Chapter
	AudioFiles []AudioFile
	UpdatedAt  int64
}

type itemWire struct {
	ID        string `json:"id"`
	MediaType string `json:"mediaType"`
	UpdatedAt int64  `json:"updatedAt"`
	Media     struct {
		Duration   float64   `json:"duration"`
		Chapters   []Chapter `json:"chapters"`
		AudioFiles []struct {
			Ino      string  `json:"ino"`
			Duration float64 `json:"duration"`
			MimeType string  `json:"mimeType"`
			Metadata struct {
				Size int64  `json:"size"`
				Ext  string `json:"ext"`
			} `json:"metadata"`
		} `json:"audioFiles"`
		Metadata struct {
			Title      string `json:"title"`
			Subtitle   string `json:"subtitle"`
			AuthorName string `json:"authorName"`
			Authors    []struct {
				Name string `json:"name"`
			} `json:"authors"`
		} `json:"metadata"`
	} `json:"media"`
}

func (w itemWire) item() Item {
	it := Item{
		ID: w.ID, MediaType: w.MediaType, UpdatedAt: w.UpdatedAt,
		Title: w.Media.Metadata.Title, Subtitle: w.Media.Metadata.Subtitle,
		Duration: w.Media.Duration, Chapters: w.Media.Chapters,
	}
	for _, af := range w.Media.AudioFiles {
		if af.Ino != "" {
			it.AudioFiles = append(it.AudioFiles, AudioFile{
				Ino:      af.Ino,
				Duration: af.Duration,
				MimeType: af.MimeType,
				Size:     af.Metadata.Size,
				Ext:      af.Metadata.Ext,
			})
		}
	}
	for _, a := range w.Media.Metadata.Authors {
		if a.Name != "" {
			it.Authors = append(it.Authors, a.Name)
		}
	}
	// The minified list form names authors in one comma-joined string.
	if len(it.Authors) == 0 && w.Media.Metadata.AuthorName != "" {
		for _, a := range strings.Split(w.Media.Metadata.AuthorName, ",") {
			if a = strings.TrimSpace(a); a != "" {
				it.Authors = append(it.Authors, a)
			}
		}
	}
	return it
}

// Session is one listening session. Times are seconds; StartedAt and
// UpdatedAt are milliseconds since the epoch.
type Session struct {
	ID            string  `json:"id"`
	LibraryItemID string  `json:"libraryItemId"`
	TimeListening float64 `json:"timeListening"`
	StartTime     float64 `json:"startTime"`
	CurrentTime   float64 `json:"currentTime"`
	StartedAt     int64   `json:"startedAt"`
	UpdatedAt     int64   `json:"updatedAt"`
	DeviceInfo    struct {
		DeviceID   string `json:"deviceId"`
		ClientName string `json:"clientName"`
	} `json:"deviceInfo"`
}

// Future sessions must be retried without advancing the import watermark.
func (s Session) Validate(now time.Time) error {
	switch {
	case s.ID == "" || len(s.ID) > 256:
		return fmt.Errorf("%w: no session id", ErrMalformedSession)
	case s.LibraryItemID == "" || len(s.LibraryItemID) > 256:
		return fmt.Errorf("%w: no item id", ErrMalformedSession)
	case len(s.DeviceInfo.DeviceID) > 256 || len(s.DeviceInfo.ClientName) > 256:
		return fmt.Errorf("%w: device info too long", ErrMalformedSession)
	case !storable(s.ID) || !storable(s.LibraryItemID) || !storable(s.DeviceInfo.DeviceID) || !storable(s.DeviceInfo.ClientName):
		return fmt.Errorf("%w: text is not valid UTF-8", ErrMalformedSession)
	case !finite(s.TimeListening) || s.TimeListening < 0 || s.TimeListening > 24*60*60:
		return fmt.Errorf("%w: timeListening %v", ErrMalformedSession, s.TimeListening)
	case !finite(s.StartTime) || !finite(s.CurrentTime) || s.StartTime < 0 || s.CurrentTime < 0:
		return fmt.Errorf("%w: times out of range", ErrMalformedSession)
	case s.StartedAt < minTimestampMs || s.UpdatedAt < s.StartedAt || s.UpdatedAt > maxTimestampMs:
		return fmt.Errorf("%w: timestamps %d..%d out of range", ErrMalformedSession, s.StartedAt, s.UpdatedAt)
	case s.TimeListening > float64(s.UpdatedAt-s.StartedAt)/1000+60:
		return fmt.Errorf("%w: listening time exceeds session span", ErrMalformedSession)
	case s.UpdatedAt > now.Add(maxClockSkew).UnixMilli():
		return fmt.Errorf("%w: updatedAt %d", ErrFutureSession, s.UpdatedAt)
	}
	return nil
}

// SessionPage is one page of an item's sessions, newest updatedAt first.
type SessionPage struct {
	Total    int       `json:"total"`
	NumPages int       `json:"numPages"`
	Page     int       `json:"page"`
	Sessions []Session `json:"sessions"`
}

// ProgressUpdate is what is written back.
type ProgressUpdate struct {
	CurrentTime float64 `json:"currentTime"`
	Duration    float64 `json:"duration"`
	Progress    float64 `json:"progress"`
	IsFinished  bool    `json:"isFinished"`
}

// Version reads the server version. The API is unversioned, so this is
// only logged.
func (c *Client) Version(ctx context.Context) (string, error) {
	var st struct {
		ServerVersion string `json:"serverVersion"`
	}
	err := c.getJSON(ctx, "/status", &st)
	return st.ServerVersion, err
}

// Me reads the account and all of its progress in one request.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var me Me
	if err := c.getJSON(ctx, "/api/me", &me); err != nil {
		return Me{}, err
	}
	if me.ID == "" {
		return Me{}, errors.New("audiobookshelf: /api/me named no user")
	}
	return me, nil
}

// Item reads one item with its chapters.
func (c *Client) Item(ctx context.Context, id string) (Item, error) {
	var w itemWire
	if err := c.getJSON(ctx, "/api/items/"+url.PathEscape(id)+"?expanded=1", &w); err != nil {
		return Item{}, err
	}
	return w.item(), nil
}

// Items lists every audiobook in every book library the key can see.
// The list form carries no chapters; Item reads them.
func (c *Client) Items(ctx context.Context) ([]Item, error) {
	var libs struct {
		Libraries []struct {
			ID        string `json:"id"`
			MediaType string `json:"mediaType"`
		} `json:"libraries"`
	}
	if err := c.getJSON(ctx, "/api/libraries", &libs); err != nil {
		return nil, err
	}
	const perPage = 100
	var out []Item
	for _, lib := range libs.Libraries {
		if lib.MediaType != "book" {
			continue
		}
		for page := 0; ; page++ {
			var resp struct {
				Total   int        `json:"total"`
				Results []itemWire `json:"results"`
			}
			path := "/api/libraries/" + url.PathEscape(lib.ID) + "/items?limit=" +
				strconv.Itoa(perPage) + "&page=" + strconv.Itoa(page)
			if err := c.getJSON(ctx, path, &resp); err != nil {
				return nil, err
			}
			for _, w := range resp.Results {
				if it := w.item(); it.ID != "" && storable(it.ID) && it.MediaType == "book" && it.Duration > 0 {
					out = append(out, it)
				}
			}
			if len(resp.Results) < perPage || (page+1)*perPage >= resp.Total {
				break
			}
		}
	}
	return out, nil
}

// Progress reads the account's progress in one item, ErrNotFound when
// there is none.
func (c *Client) Progress(ctx context.Context, itemID string) (Progress, error) {
	var p Progress
	err := c.getJSON(ctx, "/api/me/progress/"+url.PathEscape(itemID), &p)
	return p, err
}

// UpdateProgress writes the account's progress in one item. The server
// answers without the stored row, so a caller that needs the server's
// lastUpdate reads it back with Progress.
func (c *Client) UpdateProgress(ctx context.Context, itemID string, u ProgressUpdate) error {
	if !finite(u.CurrentTime) || !finite(u.Duration) || u.Duration <= 0 ||
		u.CurrentTime < 0 || u.CurrentTime > u.Duration {
		return fmt.Errorf("audiobookshelf: refusing to write currentTime %v of %v", u.CurrentTime, u.Duration)
	}
	body, err := json.Marshal(u)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPatch, "/api/me/progress/"+url.PathEscape(itemID), body)
	return err
}

// ItemSessions reads one page of the account's sessions for an item.
func (c *Client) ItemSessions(ctx context.Context, itemID string, page, perPage int) (SessionPage, error) {
	var p SessionPage
	err := c.getJSON(ctx, "/api/me/item/listening-sessions/"+url.PathEscape(itemID)+
		"?itemsPerPage="+strconv.Itoa(perPage)+"&page="+strconv.Itoa(page), &p)
	return p, err
}

// FileRange reads an inclusive byte range [start, end] of an item's audio file.
func (c *Client) FileRange(ctx context.Context, itemID, ino string, start, end int64) ([]byte, error) {
	if start < 0 || end < start || end-start+1 > maxResponseBytes {
		return nil, fmt.Errorf("audiobookshelf: invalid byte range %d..%d", start, end)
	}
	path := "/api/items/" + url.PathEscape(itemID) + "/file/" + url.PathEscape(ino)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("User-Agent", "liseur-sync")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audiobookshelf: GET %s: %w", path, unwrapURLError(err))
	}
	defer resp.Body.Close()
	want := end - start + 1
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, want+1))
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("audiobookshelf: GET %s: server answered %s", path, cleanText(resp.Status))
	}
	if readErr != nil {
		return nil, fmt.Errorf("audiobookshelf: GET %s: %w", path, readErr)
	}
	if resp.StatusCode == http.StatusOK && int64(len(raw)) > want {
		raw = raw[:want]
	}
	return raw, nil
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("audiobookshelf: GET %s: unreadable reply: %w", pathOnly(path), err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "liseur-sync")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audiobookshelf: %s %s: %w", method, pathOnly(path), unwrapURLError(err))
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("audiobookshelf: %s %s: server answered %s: %s",
			method, pathOnly(path), cleanText(resp.Status), firstLine(c.scrub(string(raw))))
	}
	if readErr != nil {
		return nil, fmt.Errorf("audiobookshelf: %s %s: %w", method, pathOnly(path), readErr)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("audiobookshelf: %s %s: reply larger than %d bytes", method, pathOnly(path), maxResponseBytes)
	}
	return raw, nil
}

// scrub keeps the key out of an error even if a server echoes it.
func (c *Client) scrub(s string) string {
	if c.key == "" {
		return s
	}
	return strings.ReplaceAll(s, c.key, "[redacted]")
}

func pathOnly(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

// unwrapURLError drops url.Error's wrapper, which repeats the full URL.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// firstLine runs after scrub, so cutting cannot split the key and leave
// half of it unredacted.
func firstLine(s string) string {
	s = strings.TrimSpace(cleanText(s))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		n := 200
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		s = s[:n] + "…"
	}
	if s == "" {
		return "(empty body)"
	}
	return s
}
