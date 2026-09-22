package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Syncer exchanges reading positions with one peer, for one account.
//
// A pass walks the bounded set of works that carry a KOReader
// fingerprint and have been read recently, and for each of them holds
// the same short conversation: ask the peer where it thinks the reader
// is, take that position if it is newer than ours, send ours if it is
// newer than theirs. Nothing about it is transactional; a pass that
// dies halfway leaves a cursor that makes the next one redo a little
// work, which is the whole cost.
type Syncer struct {
	Store store.Store
	Proto Protocol
	// Peer names the peer. It keys the bookkeeping and prefixes the
	// device id inbound positions are filed under.
	Peer string
	// UserID is the one account this mirror belongs to. Everything the
	// syncer reads and writes is scoped to it.
	UserID string
	// ActiveWindow bounds which works are considered: those read within
	// it. The peer has no push, so this is the cost control.
	ActiveWindow time.Duration
	// MaxPerPass bounds one pass, so a large library cannot turn into
	// an unbounded burst of requests at somebody else's server. Zero
	// takes defaultMaxPerPass.
	MaxPerPass int

	Log *slog.Logger
}

// Report is what one pass did, for the log and for the panel.
type Report struct {
	Considered int
	Pushed     int
	Pulled     int
	// Skipped counts works the pass deliberately left alone: our own
	// writing coming back, a reset the peer is holding, and positions
	// the two sides already agree on.
	Skipped int
	Failed  int
	// FirstError is the first thing that went wrong, kept so a caller
	// has something to show without holding every failure.
	FirstError error
}

func (r Report) String() string {
	return fmt.Sprintf("considered=%d pushed=%d pulled=%d skipped=%d failed=%d",
		r.Considered, r.Pushed, r.Pulled, r.Skipped, r.Failed)
}

func (s *Syncer) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// devicePrefix is what an inbound device id is filed under, so a reader
// looking at their devices can see which positions came from the peer,
// and so a later pass can tell them from something read here.
func (s *Syncer) devicePrefix() string { return s.Peer + ":" }

// defaultMaxPerPass is how many works one pass will talk about when
// nothing says otherwise. The store reads a limit below one as "no
// works", so a syncer built without this knob set would poll nothing
// and report a clean pass, which is the worst way for a background job
// to be broken.
const defaultMaxPerPass = 200

// Pass runs one exchange over the active set.
func (s *Syncer) Pass(ctx context.Context) (Report, error) {
	var rep Report
	limit := s.MaxPerPass
	if limit < 1 {
		limit = defaultMaxPerPass
	}
	since := time.Now().Add(-s.ActiveWindow)
	candidates, err := s.Store.MirrorCandidates(ctx, s.UserID, since, limit)
	if err != nil {
		return rep, err
	}
	cursors, err := s.Store.MirrorCursors(ctx, s.UserID, s.Peer)
	if err != nil {
		return rep, err
	}
	identity := s.Proto.Identity()
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		rep.Considered++
		cur := cursors[c.WorkID]
		cur.WorkID = c.WorkID
		// A file whose bytes changed is a new book here and a new
		// fingerprint on both sides. Whatever was agreed under the old
		// one says nothing about this one.
		if cur.Document != c.Document {
			cur = store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
		}
		// Neither does anything agreed with a different peer. A
		// mirror's name is chosen once by an operator and nothing
		// stops it outliving a change of address or of account, so
		// the cursor records which library it is about. Where the two
		// sides had got to, what the peer called this book, what was
		// last said to it: every word of it is about one library.
		if cur.PeerIdentity != identity {
			cur = store.MirrorCursor{
				WorkID: c.WorkID, Document: c.Document, PeerIdentity: identity,
			}
		}

		outcome, err := s.exchange(ctx, c, &cur)
		if err != nil {
			rep.Failed++
			if rep.FirstError == nil {
				rep.FirstError = err
			}
			cur.LastError = err.Error()
			cur.LastErrorAt = ptr(time.Now().UTC())
			s.log().Warn("mirror exchange failed",
				"peer", s.Peer, "work", c.WorkID, "err", err)
		} else {
			cur.LastError, cur.LastErrorAt = "", nil
			switch outcome {
			case outcomePushed:
				rep.Pushed++
			case outcomePulled:
				rep.Pulled++
			default:
				rep.Skipped++
			}
		}
		if err := s.Store.PutMirrorCursor(ctx, s.UserID, s.Peer, cur); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

type outcome int

const (
	outcomeNothing outcome = iota
	outcomePushed
	outcomePulled
)

// exchange is one work's conversation. It mutates the cursor in place;
// the caller writes it back either way, so a failure still records that
// it happened.
func (s *Syncer) exchange(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor,
) (outcome, error) {
	// Whether anything has ever crossed for this book and this peer.
	// It is read off the cursor rather than remembered from the moment
	// the cursor was reset, because a first pass that finds nothing —
	// a peer that does not hold the book yet, a peer that is down, a
	// push that failed halfway — is not a first exchange, and the one
	// thing this answer is used for would otherwise be spent without
	// the exchange ever happening.
	//
	// The two watermarks and nothing else. Both are written when a
	// position actually crosses or is deliberately refused, and
	// neither is written by a failure. `PulledAt` looks like it
	// belongs here and does not: it is set the moment the peer answers
	// at all, which is before the push that answer may call for.
	fresh := cur.PushedSeq == 0 && cur.RemoteTS == 0

	remote, err := s.Proto.Pull(ctx, c, cur)
	have := true
	switch {
	case errors.Is(err, ErrNotOnPeer):
		// The peer does not hold this book, so neither direction has
		// anywhere to go. Not a failure and not worth a push attempt
		// that would fail the same way.
		return outcomeNothing, nil
	case errors.Is(err, ErrNoPosition):
		have, remote = false, Place{}
	case err != nil:
		return outcomeNothing, err
	}

	local := c.Latest
	// A position filed under the peer's device prefix came from a
	// peer, and sending it straight back would be writing somebody's
	// own position to them under our name. Unless this is a peer we
	// have not spoken to before: to a new library that position is
	// simply one this server holds, and withholding it would leave the
	// book behind until the reader happened to open it again.
	localFromPeer := !fresh && strings.HasPrefix(local.DeviceID, s.devicePrefix())

	// Everything this server knows that the peer does not.
	haveNewLocal := local.Seq > cur.PushedSeq

	switch {
	case !have:
		// The peer has nothing. Ours is the only position there is,
		// unless ours came from the peer in the first place — in which
		// case sending it back would be writing their own position to
		// them under our name.
		if !haveNewLocal || localFromPeer {
			cur.PushedSeq = local.Seq
			return outcomeNothing, nil
		}
		return s.push(ctx, c, local, cur)

	case remote.Reset:
		// A standing instruction to go back to the beginning, not a
		// position anybody read to. It is never applied. A push still
		// goes out if we have something new: it updates our own device
		// row on the peer and cannot make the reset worse.
		s.log().Info("mirror ignored a reset the peer is holding",
			"peer", s.Peer, "work", c.WorkID)
		cur.PulledAt = ptr(time.Now().UTC())
		if haveNewLocal && !localFromPeer {
			return s.push(ctx, c, local, cur)
		}
		cur.PushedSeq = local.Seq
		return outcomeNothing, nil

	case remote.Echo:
		// Our own writing, come back around. The peer is up to date by
		// definition, so the only question is whether anything has
		// happened here since.
		cur.RemoteTS = remote.At.Unix()
		cur.PulledAt = ptr(time.Now().UTC())
		if haveNewLocal && !localFromPeer {
			return s.push(ctx, c, local, cur)
		}
		cur.PushedSeq = local.Seq
		return outcomeNothing, nil
	}

	cur.PulledAt = ptr(time.Now().UTC())

	// Newest wins, on the only clock both sides share: seconds. A tie
	// leaves the local position alone, because the two sides agreeing
	// to the second is far likelier to be the same reading than two
	// different ones.
	remoteTime := remote.At.UTC().Truncate(time.Second)
	localTime := local.ClientTS.UTC().Truncate(time.Second)
	switch {
	case remoteTime.After(localTime):
		if remote.At.Unix() <= cur.RemoteTS {
			// Already taken. The peer repeats itself on every poll
			// until something changes there.
			cur.PushedSeq = local.Seq
			return outcomeNothing, nil
		}
		// A position at the very start never displaces a real one. On
		// the kosync path that judgement is the peer's, which answers
		// a pending reset with a recognisable synthetic reply; a
		// protocol whose reply carries no such tell has only the
		// position itself to go on, and the cost of being wrong is a
		// reader sent back to page one on every poll.
		if remote.Percentage == 0 && local.Progression > 0 {
			s.log().Info("mirror declined a start-of-book position from the peer",
				"peer", s.Peer, "work", c.WorkID)
			cur.RemoteTS = remote.At.Unix()
			cur.PushedSeq = local.Seq
			return outcomeNothing, nil
		}
		return s.apply(ctx, c, remote, cur)
	case localTime.After(remoteTime):
		if !haveNewLocal || localFromPeer {
			cur.PushedSeq = local.Seq
			return outcomeNothing, nil
		}
		return s.push(ctx, c, local, cur)
	default:
		cur.RemoteTS = remote.At.Unix()
		cur.PushedSeq = local.Seq
		return outcomeNothing, nil
	}
}

// push sends a local position out. Everything the op knows goes into
// the Place and the protocol takes what it can carry: the engine
// position verbatim when the op has one, and the CFI a Readium locator
// records, which is nothing to kosync and the exact spot to a peer
// that speaks CFIs. Nothing is invented in between, and a protocol
// that cannot carry a field drops it rather than translating it.
func (s *Syncer) push(ctx context.Context, c store.MirrorCandidate, local store.Op, cur *store.MirrorCursor) (outcome, error) {
	p := Place{
		Percentage: local.Progression,
		CFI:        cfiFromLocator(local.LocatorJSON),
		At:         local.ClientTS.UTC(),
	}
	if local.ForeignPos != nil {
		p.Foreign = *local.ForeignPos
	}
	if err := s.Proto.Push(ctx, c, cur, p); err != nil {
		if errors.Is(err, ErrNotOnPeer) {
			return outcomeNothing, nil
		}
		return outcomeNothing, err
	}
	cur.PushedSeq = local.Seq
	cur.PushedAt = ptr(time.Now().UTC())
	s.log().Info("mirror pushed a position",
		"peer", s.Peer, "work", c.WorkID, "progression", local.Progression)
	return outcomePushed, nil
}

// apply takes a position from the peer and makes it a native op. It
// goes in as a kosync record because that is what it is whichever
// protocol carried it: a position from another server, joined on the
// KOReader fingerprint, with no locator of this server's own making.
// Reusing the existing origin keeps insights grouping, rollups and
// compaction working on it without knowing the mirror exists, which is
// the reason ADR-0047 gave and it does not change with the wire shape.
func (s *Syncer) apply(ctx context.Context, c store.MirrorCandidate, remote Place, cur *store.MirrorCursor) (outcome, error) {
	device := remote.Device
	if device == "" {
		device = "unknown"
	}
	device = s.devicePrefix() + device

	alias := "partial-md5:" + c.Document
	op := store.Op{
		OpID:        s.opID(c.Document, remote),
		DeviceID:    device,
		ClientTS:    remote.At.UTC(),
		Progression: remote.Percentage,
		Origin:      store.OriginKosync,
		OriginAlias: &alias,
		LocatorJSON: locatorWithCFI(remote.CFI, remote.Percentage),
	}
	if remote.Foreign != "" {
		foreign := remote.Foreign
		op.ForeignPos = &foreign
	}
	res, err := s.Store.AppendKosyncOp(ctx, s.UserID, c.Document, device, op)
	if err != nil {
		return outcomeNothing, err
	}
	if res.Status == "conflict" {
		// The same position id with a different payload. The op log is
		// append-only and this is not something to retry, so it is
		// recorded and the cursor moves past it.
		cur.RemoteTS = remote.At.Unix()
		return outcomeNothing, fmt.Errorf(
			"mirror: the peer's position for %s conflicts with one already recorded", c.Document)
	}
	cur.RemoteTS = remote.At.Unix()
	s.log().Info("mirror took a position from the peer",
		"peer", s.Peer, "work", c.WorkID, "device", device,
		"progression", remote.Percentage)
	return outcomePulled, nil
}

// opID derives a stable id from the position itself, so the same remote
// position pulled twice is recognised as the duplicate it is rather
// than appended again. The op log has no other defence: it is
// append-only and idempotent on the id, and the peer has no id of its
// own to offer.
func (s *Syncer) opID(document string, p Place) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		s.Peer, document,
		strconv.FormatInt(p.At.Unix(), 10),
		strconv.FormatFloat(p.Percentage, 'f', -1, 64),
		p.Foreign, p.CFI, p.Device,
	}, "\x1f")))
	return "mirror-" + hex.EncodeToString(sum[:16])
}

func ptr[T any](v T) *T { return &v }
