package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
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

// legacyIdentities is what a peer says an earlier build folded for it,
// empty for a protocol that has nothing to add.
func (s *Syncer) legacyIdentities() []string {
	if p, ok := s.Proto.(legacyIdentity); ok {
		return p.legacyIdentities()
	}
	return nil
}

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
	legacy := s.legacyIdentities()
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
		//
		// An identity an earlier build folded for this same peer is
		// the same library under another spelling of its address, so
		// the cursor keeps its watermarks and adopts the current one.
		if cur.PeerIdentity != identity {
			if slices.Contains(legacy, cur.PeerIdentity) {
				cur.PeerIdentity = identity
			} else {
				cur = store.MirrorCursor{
					WorkID: c.WorkID, Document: c.Document, PeerIdentity: identity,
				}
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
//
// The request happens outside the account lock: a slow peer must not hold
// another worker of the same account up. What the answer means is decided
// under that lock, from a snapshot of the op log read at that moment
// rather than the one the candidate carried into the request.
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

	push, out, err := s.settle(ctx, c, cur, remote, have, fresh)
	if err != nil || push == nil {
		return out, err
	}
	out, err = s.push(ctx, c, *push, cur)
	if err != nil {
		return out, err
	}
	// The peer's clock is a watermark of its own, and like the other
	// one it records something that crossed. A push that failed leaves
	// both alone, so the retry it owes is still owed (ADR-0048).
	if have {
		cur.RemoteTS = remote.At.Unix()
	}
	return out, nil
}

// accountLocks holds one mutex per mirrored account, for the life of the
// process. Peers of one account read and write the same op log, and a
// decision taken from a snapshot read before a network call can commit a
// position a newer peer result has already superseded. The lock spans the
// re-read and the commit, never the request: event delivery is local to
// this process (docs/deployment.md), so one mutex is the whole of the
// coordination.
var accountLocks sync.Map

func accountLock(userID string) *sync.Mutex {
	mu, _ := accountLocks.LoadOrStore(userID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// settle decides what crosses for one work, holding the account lock from
// the moment the latest op is read to the moment the answer is committed.
// A push comes back as the op to send rather than being performed here:
// the request leaves the lock, and it sends what was just read.
func (s *Syncer) settle(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor,
	remote Place, have, fresh bool,
) (*store.Op, outcome, error) {
	mu := accountLock(s.UserID)
	mu.Lock()
	defer mu.Unlock()

	ops, err := s.Store.Positions(ctx, s.UserID, c.WorkID, 1)
	if err != nil {
		return nil, outcomeNothing, err
	}
	if len(ops) == 0 {
		return nil, outcomeNothing, nil
	}
	local := ops[0]

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
			return nil, outcomeNothing, nil
		}
		return &local, outcomeNothing, nil

	case remote.Reset:
		// A standing instruction to go back to the beginning, not a
		// position anybody read to. It is never applied. A push still
		// goes out if we have something new: it updates our own device
		// row on the peer and cannot make the reset worse.
		s.log().Info("mirror ignored a reset the peer is holding",
			"peer", s.Peer, "work", c.WorkID)
		cur.PulledAt = ptr(time.Now().UTC())
		if haveNewLocal && !localFromPeer {
			return &local, outcomeNothing, nil
		}
		cur.PushedSeq = local.Seq
		return nil, outcomeNothing, nil

	case remote.Echo:
		// Our own writing, come back around. The peer is up to date by
		// definition, so the only question is whether anything has
		// happened here since.
		cur.PulledAt = ptr(time.Now().UTC())
		if haveNewLocal && !localFromPeer {
			return &local, outcomeNothing, nil
		}
		cur.RemoteTS = remote.At.Unix()
		cur.PushedSeq = local.Seq
		return nil, outcomeNothing, nil
	}

	place := comparePlaces(local, remote, protocolCarriesCFI(s.Proto))
	switch place {
	case agree:
		// The peer is saying the place this server already holds, in
		// its own words and on its own clock. That is agreement, not
		// news: applying it would append an op that says nothing new,
		// and with more than one peer a position can be restamped on
		// its way around a loop that never changes it. Two protocols
		// reading one library — BookOrbit's own API and its KOReader
		// route, say — are exactly that loop without this.
		cur.RemoteTS = remote.At.Unix()
		cur.PulledAt = ptr(time.Now().UTC())
		cur.PushedSeq = local.Seq
		return nil, outcomeNothing, nil

	case localFiner:
		// Same place, and the spot this server names is one the peer
		// lacks. Sending it once is the only way that detail can
		// cross; a push appends nothing here, and the watermark makes
		// it once per op.
		//
		// Nothing is recorded until the push has happened, because a
		// failed one must not spend either watermark: the first
		// exchange with a peer is read off them, and a position the
		// peer never received would then look like its own and never
		// be offered again.
		cur.PulledAt = ptr(time.Now().UTC())
		if haveNewLocal && !localFromPeer {
			return &local, outcomeNothing, nil
		}
		cur.RemoteTS = remote.At.Unix()
		cur.PushedSeq = local.Seq
		return nil, outcomeNothing, nil
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
			return nil, outcomeNothing, nil
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
			return nil, outcomeNothing, nil
		}
		out, err := s.apply(ctx, c, remote, cur)
		return nil, out, err
	case localTime.After(remoteTime):
		if place == peerFiner {
			// The peer holds the same place with a spot this server
			// lacks. Pushing the coarser copy would erase it, so the
			// answer is recorded and nothing goes out.
			cur.RemoteTS = remote.At.Unix()
			cur.PushedSeq = local.Seq
			return nil, outcomeNothing, nil
		}
		if !haveNewLocal || localFromPeer {
			cur.PushedSeq = local.Seq
			return nil, outcomeNothing, nil
		}
		return &local, outcomeNothing, nil
	default:
		cur.RemoteTS = remote.At.Unix()
		cur.PushedSeq = local.Seq
		return nil, outcomeNothing, nil
	}
}

// agreement is how two sides compare at one book's place.
type agreement int

const (
	// disagree: the two sides are at different places, or at places
	// that cannot be compared exactly.
	disagree agreement = iota
	// agree: neither side has anything the other could take.
	agree
	// peerFiner: same place, and the peer names a spot this server
	// lacks.
	peerFiner
	// localFiner: same place, and this server names a spot the peer
	// lacks.
	localFiner
)

// comparePlaces judges whether the peer's position is the one this server
// already holds, and which side names it more exactly.
//
// It compares the finest thing both sides can carry, in both directions.
// Two readers can sit at the same percent of a book in different
// sentences, so an exact field one side sends is compared with the same
// kind of field here when both have one; the fraction decides only when
// neither has anything finer to say. A kind of field only one side holds
// is a difference in detail rather than in place, and is reported as which
// side has the finer answer instead of as agreement or disagreement.
//
// carriesCFI says whether the protocol could have carried a CFI at all. A
// peer with nowhere to put one is not withholding detail, and this server's
// own ops may hold a CFI another protocol recorded: demanding that such a
// peer echo it would call every poll news.
func comparePlaces(local store.Op, remote Place, carriesCFI bool) agreement {
	// A position at the very start is not agreement. On one protocol it
	// is what a pending reset looks like, and the rules above already
	// decide what to do with it; taking it as a no-op here would answer
	// them before they were asked.
	if remote.Percentage == 0 && local.Progression == 0 {
		return disagree
	}

	localCFI := ""
	if carriesCFI {
		localCFI = cfiFromLocator(local.LocatorJSON)
	}
	localForeign := false
	if local.ForeignPos != nil {
		localForeign = exactForeignString(*local.ForeignPos, local.Progression)
	}
	peerCFI := remote.CFI != ""
	peerForeign := exactForeign(remote)

	// The same kind of field on both sides, saying different things, is a
	// different place however the fractions compare.
	if peerCFI && localCFI != "" && remote.CFI != localCFI {
		return disagree
	}
	if peerForeign && localForeign &&
		strings.TrimSpace(*local.ForeignPos) != remote.Foreign {
		return disagree
	}

	matched := (peerCFI && localCFI == remote.CFI) ||
		(peerForeign && localForeign &&
			strings.TrimSpace(*local.ForeignPos) == remote.Foreign)
	peerOnly := (peerCFI && localCFI == "") || (peerForeign && !localForeign)
	localOnly := (localCFI != "" && !peerCFI) || (localForeign && !peerForeign)

	if matched {
		switch {
		case peerOnly && !localOnly:
			return peerFiner
		case localOnly && !peerOnly:
			return localFiner
		default:
			return agree
		}
	}
	if round(local.Progression, 6) != round(remote.Percentage, 6) {
		return disagree
	}
	switch {
	case peerOnly && localOnly:
		// Each side names a spot the other cannot read, so neither
		// answer supersedes the other: newest wins.
		return disagree
	case peerOnly:
		return peerFiner
	case localOnly:
		return localFiner
	default:
		return agree
	}
}

// exactForeignString reports whether an engine position names something the
// fraction does not. KOReader has no locator for a client that sends none, so
// this server tells it the percentage and a peer hands that back as Progress;
// that string is the fraction written out again, not a finer answer, and
// counting it as one would make every restamped round trip look like news.
func exactForeignString(foreign string, percentage float64) bool {
	foreign = strings.TrimSpace(foreign)
	if foreign == "" {
		return false
	}
	asFloat, err := strconv.ParseFloat(foreign, 64)
	return err != nil || round(asFloat, 6) != round(percentage, 6)
}

func exactForeign(p Place) bool { return exactForeignString(p.Foreign, p.Percentage) }

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
