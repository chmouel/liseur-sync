package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// bookOrbitPeer speaks BookOrbit's own REST API rather than the
// KOReader protocol it also offers (ADR-0048).
//
// What that buys is fidelity: BookOrbit records an EPUB CFI, and so
// does every client this server has, so a position can cross naming
// the spot instead of the fraction. What it costs is everything else
// in this file — a session to keep alive, a book to find, and an echo
// to recognise without being told.
type bookOrbitPeer struct {
	api     *bookOrbitAPI
	cfg     config.MirrorConfig
	log     *slog.Logger
	version string
}

// BookOrbitProtocol builds the native BookOrbit protocol for an
// already-validated config.
func BookOrbitProtocol(cfg config.MirrorConfig, log *slog.Logger) Protocol {
	if log == nil {
		log = slog.Default()
	}
	return &bookOrbitPeer{api: newBookOrbitAPI(cfg, log), cfg: cfg, log: log}
}

func (b *bookOrbitPeer) Name() string { return config.ProtocolBookOrbit }

// Identity matters more here than on the KOReader path, because what
// this protocol remembers about a peer is a small integer. A
// remembered file id would still address *something* on a different
// installation, and a position would be read from and written to a
// stranger's book.
func (b *bookOrbitPeer) Identity() string {
	return peerIdentity(config.ProtocolBookOrbit, b.cfg.BaseURL, b.cfg.RemoteUser)
}

func (b *bookOrbitPeer) Authorize(ctx context.Context) error {
	// A session already in hand is reused rather than replaced. The
	// mirror re-authorizes whenever it rebuilds itself, which during
	// an outage is often, and BookOrbit keeps a session for a week:
	// signing in each time would leave a week of dead logins on
	// somebody's account.
	if _, err := b.api.token(ctx); err != nil {
		return err
	}
	version, err := b.api.appInfo(ctx)
	if err != nil {
		// Knowing the peer's version is a convenience for reading the
		// log later, not a precondition for anything.
		b.log.Debug("mirror could not read the peer version", "error", err)
		return nil
	}
	b.version = version
	b.log.Info("mirror signed in to the peer", "peer_version", version)
	return nil
}

func (b *bookOrbitPeer) Close(ctx context.Context) { b.api.logout(ctx) }

// remoteDevice is what an inbound position is attributed to. BookOrbit
// keeps one progress row per reader and file with no device on it, so
// there is nothing more specific to say and inventing something would
// be a claim about where the reading happened.
const remoteDevice = "reader"

// Pull asks the peer where it thinks the reader is.
func (b *bookOrbitPeer) Pull(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor,
) (Place, error) {
	fileID, err := b.resolve(ctx, c, cur)
	if err != nil {
		return Place{}, err
	}
	var reply progressReply
	if err := b.api.get(ctx, "/books/files/"+url.PathEscape(fileID)+"/progress", &reply); err != nil {
		if errors.Is(err, errPeerNotFound) {
			// The file this cursor remembers is gone from the peer, so
			// the remembered id is worthless. Forgetting it is what
			// lets the book be found again if it came back under a new
			// one.
			cur.RemoteFileID, cur.RemoteCheckedAt = "", nil
			return Place{}, ErrNotOnPeer
		}
		return Place{}, err
	}
	// A file the reader has never opened is answered with a default
	// body rather than a 404, and the default omits the timestamps a
	// real row carries. So the absence of a read time is how "nothing
	// here" is recognised, and it is also exactly the condition under
	// which there would be nothing to compare against anyway.
	if reply.LastReadAt == nil || reply.LastReadAt.IsZero() {
		return Place{}, ErrNoPosition
	}
	if reply.Percentage < 0 || reply.Percentage > 100 {
		return Place{}, fmt.Errorf(
			"mirror: the peer sent percentage %v, outside 0..100", reply.Percentage)
	}
	place := Place{
		Percentage: reply.Percentage / 100,
		CFI:        value(reply.CFI),
		Foreign:    value(reply.KoreaderProgress),
		Device:     remoteDevice,
		At:         reply.LastReadAt.UTC(),
	}
	// A paged book has no xpointer and a page number instead, which is
	// the same thing kosync carries for one: a bare number.
	if place.Foreign == "" && reply.PageNumber != nil && *reply.PageNumber > 0 {
		place.Foreign = strconv.Itoa(*reply.PageNumber)
	}
	place.Mark = placeMark(place)
	place.Echo = cur.PushedMark != "" && place.Mark == cur.PushedMark
	if !place.Echo {
		// The mark says what the peer's row holds because of us. It
		// plainly holds something else now, so the mark is spent:
		// keeping it would call a reader's return to that spot our own
		// writing, months later, and refuse to follow them back to it.
		cur.PushedMark = ""
	}
	return place, nil
}

// Push writes a position to the peer, and remembers what it said.
func (b *bookOrbitPeer) Push(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor, p Place,
) error {
	fileID, err := b.resolve(ctx, c, cur)
	if err != nil {
		return err
	}
	if p.Percentage < 0 || p.Percentage > 1 {
		return fmt.Errorf("mirror: percentage %v is outside 0..1", p.Percentage)
	}
	write := progressWrite{
		Source:     "text",
		Percentage: round(p.Percentage*100, 4),
	}
	if p.CFI != "" {
		write.CFI = &p.CFI
	}
	// An engine position goes out as what it is. A KOReader xpointer is
	// something BookOrbit understands and stores beside the CFI; a bare
	// page number is a page number. Neither is translated into the
	// other, and a position that is only a fraction stays one.
	if foreign := strings.TrimSpace(p.Foreign); foreign != "" {
		if page, err := strconv.Atoi(foreign); err == nil && page > 0 {
			write.PageNumber = &page
		} else {
			write.KoreaderProgress = &foreign
		}
	}
	if err := b.api.post(ctx,
		"/books/files/"+url.PathEscape(fileID)+"/progress", write, nil); err != nil {
		return err
	}
	cur.PushedMark = placeMark(Place{
		Percentage: p.Percentage, CFI: p.CFI, Foreign: strings.TrimSpace(p.Foreign),
	})
	return nil
}

// placeMark identifies a position by its content, so this server can
// recognise its own writing coming back from a peer whose replies
// carry no device.
//
// A CFI is the whole of the mark when there is one: it is long, exact
// and reproduced verbatim, so two positions sharing one are the same
// position. Failing that, an engine position is the next most exact
// thing either side holds. The fraction is the last resort, rounded to
// the place where a float that has been through a database and a JSON
// number still compares equal.
//
// The mark misjudging is cheap in both directions. A position the peer
// rewrote is taken back once and then held off by the timestamp guard;
// a position a reader independently reached that is identical to ours
// is a position we already have.
func placeMark(p Place) string {
	switch {
	case p.CFI != "":
		return "cfi:" + p.CFI
	case p.Foreign != "":
		// A KOReader position is exact in its own way, and two of them
		// at the same percentage are two different places. Falling
		// through to the fraction here would read a device's new
		// position as this server's own writing and never take it.
		return "pos:" + p.Foreign
	default:
		return "pct:" + strconv.FormatFloat(round(p.Percentage, 6), 'f', -1, 64)
	}
}

func round(v float64, places int) float64 {
	scale := math.Pow(10, float64(places))
	return math.Round(v*scale) / scale
}

func value(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// resolveRetry is how long a book the peer does not appear to hold is
// left alone before being looked for again. Resolution costs two
// requests and the answer almost never changes; asking every poll
// would spend most of the mirror's budget on books it cannot mirror.
const resolveRetry = 24 * time.Hour

// resolve finds what the peer calls this book, and remembers it.
//
// This is the price of not being joined on the document fingerprint.
// BookOrbit stores that fingerprint — the same KOReader partial MD5
// this server computes — on every file, and exposes it nowhere, and
// has no route that maps a hash, a path or a filename to a file id. So
// the book is searched for by title to narrow the field, and then
// decided on the bytes: a file whose size is ours, whose name is ours,
// and whose path is ours when the operator has said how the two
// machines spell the same directory.
//
// More than one match is refused rather than guessed, for the reason
// ADR-0047 refuses an ambiguous fingerprint: the cost of being wrong is
// a reader's place in the wrong book.
func (b *bookOrbitPeer) resolve(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor,
) (string, error) {
	if cur.RemoteFileID != "" {
		return cur.RemoteFileID, nil
	}
	if cur.RemoteCheckedAt != nil && time.Since(*cur.RemoteCheckedAt) < resolveRetry {
		return "", ErrNotOnPeer
	}
	// A work can carry a fingerprint no catalog book holds, because a
	// KOReader device said so. There is nothing to search for.
	if c.RelativePath == "" || c.SizeBytes <= 0 {
		cur.RemoteCheckedAt = ptr(time.Now().UTC())
		return "", ErrNotOnPeer
	}
	cur.RemoteCheckedAt = ptr(time.Now().UTC())

	cards, err := b.search(ctx, c.Title)
	if err != nil {
		// A failed search is not an answer about the book, so it must
		// not be remembered as one.
		cur.RemoteCheckedAt = nil
		return "", err
	}
	if len(cards) >= searchPageSize {
		// A full page is not a shortlist: the match this picked might
		// have a twin on the page nobody asked for. Paging through to
		// find out would be a lot of requests to answer a question
		// whose only useful answer is "too many", since more than one
		// match is refused anyway.
		b.log.Info("mirror declined to identify a book from a crowded search",
			"work", c.WorkID, "results", len(cards))
		return "", ErrNotOnPeer
	}
	bookID, err := b.matchBySize(cards, c)
	if err != nil {
		b.log.Info("mirror could not identify a book on the peer",
			"work", c.WorkID, "reason", err)
		return "", ErrNotOnPeer
	}
	fileID, err := b.confirm(ctx, bookID, c)
	if err != nil {
		cur.RemoteCheckedAt = nil
		return "", err
	}
	if fileID == "" {
		b.log.Info("mirror declined a book on the peer that did not confirm",
			"work", c.WorkID, "peer_book", bookID)
		return "", ErrNotOnPeer
	}
	cur.RemoteFileID = fileID
	b.log.Info("mirror identified a book on the peer",
		"work", c.WorkID, "peer_book", bookID, "peer_file", fileID)
	return fileID, nil
}

// matchBySize picks the one card holding a file of exactly our size and
// format. The title got us the shortlist; it decides nothing.
func (b *bookOrbitPeer) matchBySize(cards []bookCard, c store.MirrorCandidate) (string, error) {
	want := strings.ToLower(strings.TrimPrefix(path.Ext(c.RelativePath), "."))
	var found []string
	for _, card := range cards {
		if card.ID.String() == "" {
			continue
		}
		for _, f := range card.Files {
			if f.SizeBytes != c.SizeBytes {
				continue
			}
			if want != "" && f.Format != "" && !strings.EqualFold(f.Format, want) {
				continue
			}
			found = append(found, card.ID.String())
			break
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no book on the peer has a file of %d bytes", c.SizeBytes)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%d books on the peer have a file of %d bytes", len(found), c.SizeBytes)
	}
}

// confirm checks the shortlisted book really is ours and returns the
// id of the file to sync against, or "" if it is not.
func (b *bookOrbitPeer) confirm(
	ctx context.Context, bookID string, c store.MirrorCandidate,
) (string, error) {
	var detail bookDetail
	if err := b.api.get(ctx, "/books/"+url.PathEscape(bookID), &detail); err != nil {
		if errors.Is(err, errPeerNotFound) {
			return "", nil
		}
		return "", err
	}
	base := path.Base(c.RelativePath)
	var matched string
	for _, f := range detail.Files {
		if f.SizeBytes != c.SizeBytes || f.ID.String() == "" {
			continue
		}
		name := f.Filename
		if name == "" {
			name = path.Base(f.AbsolutePath)
		}
		if !strings.EqualFold(name, base) {
			continue
		}
		if !b.pathAgrees(f.AbsolutePath, c) {
			continue
		}
		if matched != "" {
			// Two files of one book, the same size and the same name.
			// Nothing here can say which one the reader meant.
			return "", nil
		}
		matched = f.ID.String()
	}
	return matched, nil
}

// pathAgrees checks the peer's idea of where the file is against ours,
// when the operator has said how to translate one into the other.
//
// The peer reports the path it sees from inside its own container,
// which is not the path this server sees even when both are reading
// the same bytes off the same disk. Naming both roots turns that into
// the strongest confirmation available; leaving them unset means the
// size and the filename are the whole of the match, which is what a
// peer that is not a container needs anyway.
func (b *bookOrbitPeer) pathAgrees(remote string, c store.MirrorCandidate) bool {
	if b.cfg.PeerPathPrefix == "" || b.cfg.LocalPathPrefix == "" || remote == "" {
		return true
	}
	peerRoot := strings.TrimSuffix(b.cfg.PeerPathPrefix, "/")
	localRoot := strings.TrimSuffix(b.cfg.LocalPathPrefix, "/")
	rest, ok := strings.CutPrefix(remote, peerRoot+"/")
	if !ok {
		return false
	}
	return path.Join(localRoot, rest) == path.Join(c.RootPath, c.RelativePath)
}

// searchPageSize bounds one catalog reply. It is large enough that a
// title shared by a handful of editions comes back in one request and
// small enough that a title shared by a hundred is refused as the
// ambiguity it is rather than paged through.
const searchPageSize = 50

// search asks the peer for books whose metadata mentions this title.
// BookOrbit's free-text search covers title, author, series and
// narrator and never touches a filename, so this narrows and nothing
// more; an empty title narrows nothing and is not worth a request.
func (b *bookOrbitPeer) search(ctx context.Context, title string) ([]bookCard, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	query := map[string]any{
		"q":          title,
		"sort":       []map[string]string{{"field": "addedAt", "dir": "desc"}},
		"pagination": map[string]int{"page": 1, "size": searchPageSize},
	}
	var raw json.RawMessage
	if err := b.api.post(ctx, "/books/query", query, &raw); err != nil {
		return nil, err
	}
	return decodeCards(raw)
}

// decodeCards finds the book cards in a search reply.
//
// The reply is a page of results in an envelope BookOrbit is free to
// rename, and has: the catalog routes change in most of its releases.
// So rather than pin a field name that will move, this takes the
// reply's first array that decodes into something with a book's shape.
// An envelope that changes around the cards costs nothing; one that
// changes the cards themselves is a real break and is reported.
func decodeCards(raw json.RawMessage) ([]bookCard, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if cards, ok := asCards(raw); ok {
		return cards, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("mirror: unreadable search reply from the peer: %w", err)
	}
	for _, key := range []string{"items", "data", "results", "books", "content", "rows"} {
		if cards, ok := asCards(envelope[key]); ok {
			return cards, nil
		}
	}
	for _, field := range envelope {
		if cards, ok := asCards(field); ok {
			return cards, nil
		}
	}
	// The peer answered something, and nothing in it is a list of
	// books. That is the peer having changed in a way this does not
	// understand, which is a failure to be retried and logged — not a
	// library with nothing in it, which would quietly hide every book
	// for a day.
	return nil, fmt.Errorf("mirror: the peer's search reply holds no books this understands")
}

// asCards reports whether raw is an array of things shaped like a book
// card. An empty array counts: a search that found nothing is an
// answer, and the alternative is treating it as a broken envelope and
// rummaging through the rest of the reply for something that looks
// better.
func asCards(raw json.RawMessage) ([]bookCard, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var cards []bookCard
	if err := json.Unmarshal(raw, &cards); err != nil {
		return nil, false
	}
	for _, c := range cards {
		if c.ID.String() == "" {
			return nil, false
		}
	}
	return cards, true
}

// The shapes this protocol reads. Everything BookOrbit sends that is
// not named here is ignored on purpose: this needs a book's identity
// and its files' bytes, and nothing else it holds is any of this
// server's business.
type (
	bookCard struct {
		ID    json.Number `json:"id"`
		Title string      `json:"title"`
		Files []bookFile  `json:"files"`
	}
	bookDetail struct {
		ID    json.Number `json:"id"`
		Title string      `json:"title"`
		Files []bookFile  `json:"files"`
	}
	// bookFile is a file of a book. The id is BookOrbit's own integer
	// row id, which is what its progress routes take; filename and
	// absolutePath appear only on the detail route.
	bookFile struct {
		ID           json.Number `json:"id"`
		Format       string      `json:"format"`
		SizeBytes    int64       `json:"sizeBytes"`
		Filename     string      `json:"filename"`
		AbsolutePath string      `json:"absolutePath"`
	}
	// progressReply is a reading position. Every pointer field is a
	// field BookOrbit may answer with null, and null is not zero: a
	// page number that is absent must not read as page zero, and a
	// read time that is absent is how a book nobody has opened is
	// recognised.
	progressReply struct {
		BookFileID       *json.Number `json:"bookFileId"`
		CFI              *string      `json:"cfi"`
		PageNumber       *int         `json:"pageNumber"`
		Percentage       float64      `json:"percentage"`
		KoreaderProgress *string      `json:"koreaderProgress"`
		// LastReadAt is when the reader last read this file from any
		// client. It is the one to compare on: BookOrbit deliberately
		// freezes updatedAt on its own KOReader sync path, so that one
		// is stale for exactly the positions a mirror cares about.
		LastReadAt *time.Time `json:"lastReadAt"`
		UpdatedAt  *time.Time `json:"updatedAt"`
	}
	// progressWrite is a position going the other way. Percentage is
	// 0..100 here, which is BookOrbit's scale and not this server's.
	progressWrite struct {
		Source           string  `json:"source"`
		CFI              *string `json:"cfi,omitempty"`
		PageNumber       *int    `json:"pageNumber,omitempty"`
		Percentage       float64 `json:"percentage"`
		KoreaderProgress *string `json:"koreaderProgress,omitempty"`
	}
)
