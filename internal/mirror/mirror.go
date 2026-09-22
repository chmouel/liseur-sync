package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Place is a reading position as the mirror carries it across the
// border, in whatever fidelity the protocol on the other side managed.
//
// It exists so that the decision of *when* a position crosses —
// ADR-0047's newest-wins, its refusals, its bookkeeping — is written
// once and is not entangled with how any one peer spells a position.
// A field a protocol cannot carry is left empty rather than invented:
// kosync has no CFI and BookOrbit's native API has no device, and both
// gaps are answered by leaving the field alone.
type Place struct {
	// Percentage is the fraction of the book, 0..1, always. It is the
	// one thing every protocol carries, and the scale is KOReader's
	// because that is the scale this server's ops already use.
	// BookOrbit's native API counts to a hundred; that conversion
	// belongs at its edge and nowhere else.
	Percentage float64
	// CFI is an EPUB canonical fragment identifier, when the peer had
	// one. It is the whole reason the native protocol is worth having:
	// it names a spot rather than a fraction.
	CFI string
	// Foreign is an engine position carried verbatim and never
	// interpreted: a KOReader xpointer for a reflowable document, a
	// bare page number for a paged one.
	Foreign string
	// Device is what the peer says wrote this position, when the peer
	// has a notion of one. It becomes part of the device id a reader
	// sees, so they can tell where a position came from.
	Device string
	// At is when the peer says the position was reached. It is the
	// only clock the two servers share.
	At time.Time
	// Mark identifies this position by its content. A protocol that
	// cannot tell this server's own writing apart by device says so by
	// filling this in on both the way out and the way back, and the
	// syncer remembers the last one it sent.
	Mark string
	// Echo is the protocol reporting that this position is this
	// server's own writing, come back around.
	Echo bool
	// Reset is the protocol reporting that this reply is a standing
	// instruction to go back to the beginning rather than a position
	// somebody read to. It is never applied (ADR-0047).
	Reset bool
}

// ErrNotOnPeer is a protocol reporting that it cannot say which book
// on the peer this one is, so there is nothing to sync against.
//
// It is an answer, not a failure. A peer holds its own library and
// most of the overlap with this one is partial; a book only this side
// has is the normal case and must not count against the backoff or
// show up as an error in the log every pass.
var ErrNotOnPeer = errors.New("mirror: the peer does not hold this book")

// Protocol is one way of holding the conversation with a peer.
//
// Implementations own everything that is theirs: the wire shape, the
// credential, how a book on this disk is named over there, and the two
// judgements no generic code could make — whether a reply is this
// server's own echo, and whether it is a reset rather than a position.
// Everything else is the syncer's.
type Protocol interface {
	// Name is how the protocol is spelled in configuration.
	Name() string
	// Identity is which peer this is, as opposed to which mirror. A
	// mirror is named once by an operator and that name outlives a
	// change of address or of account, while everything a cursor
	// remembers — how far the two sides had got, what the peer calls
	// this book — is true of one particular library and meaningless
	// about another. A cursor carries this so the difference can be
	// noticed.
	Identity() string
	// Authorize checks the credential. It is called once before the
	// first pass and again whenever the mirror has to rebuild itself,
	// so a bad credential is a line in the log at startup rather than
	// something inferred from a string of failed passes.
	Authorize(ctx context.Context) error
	// Pull asks the peer where it thinks the reader is. ErrNoPosition
	// means the peer has nothing to say about this book, which is an
	// ordinary answer rather than a failure: most books are known to
	// only one side.
	//
	// The cursor is passed because a protocol may have to look the
	// book up on the peer first and has nowhere else to remember the
	// answer. Whatever it writes there is persisted by the caller,
	// including after a failure.
	Pull(ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor) (Place, error)
	// Push sends a local position out. It may record on the cursor
	// whatever it needs to recognise that position coming back.
	Push(ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor, p Place) error
	// Close releases whatever the protocol holds open. It is given its
	// own context because the one that ended the mirror is already
	// cancelled and a polite goodbye still has to travel.
	Close(ctx context.Context)
}

// cfiFromLocator digs the EPUB CFI out of an op's locator, if it has
// one.
//
// Every client this server has writes a Readium locator, and a Readium
// locator puts a CFI in locations.fragments beside whatever else the
// engine knew. The locator is otherwise an opaque envelope here and
// stays that way: this reads one well-known field and ignores
// everything else, including a locator that is not an object at all.
func cfiFromLocator(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var envelope struct {
		Locations struct {
			Fragments []string `json:"fragments"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	for _, fragment := range envelope.Locations.Fragments {
		if strings.HasPrefix(fragment, "epubcfi(") {
			return fragment
		}
	}
	return ""
}

// locatorWithCFI builds the locator an inbound position is stored
// under.
//
// It names no resource, because the peer did not say which one and
// guessing would file the position against the wrong chapter. That
// costs less than it looks: this server's reader offers a stored CFI
// before anything else and resolves the spine step itself, so a
// position with a CFI and no href still reopens on the exact spot.
// Readium on a phone wants the resource and falls back to the
// fraction, which is where the kosync bridge left it anyway.
func locatorWithCFI(cfi string, progression float64) []byte {
	if cfi == "" {
		return nil
	}
	raw, err := json.Marshal(map[string]any{
		"locations": map[string]any{
			"fragments":        []string{cfi},
			"totalProgression": progression,
		},
	})
	if err != nil {
		return nil
	}
	return raw
}

// peerIdentity folds what makes a peer that peer into a short digest:
// the protocol spoken, the address and the account. It is stored on
// every cursor, so it is kept short, and it is not a secret, so the
// credential is deliberately not part of it — a rotated password is
// the same library and the cursor should survive it.
func peerIdentity(protocol, baseURL, remoteUser string) string {
	sum := sha256.Sum256([]byte(
		protocol + "\n" + strings.TrimSuffix(baseURL, "/") + "\n" + remoteUser))
	return hex.EncodeToString(sum[:8])
}
