package audiobookshelf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type Report struct {
	Considered int
	Pushed     int
	Pulled     int
	Imported   int
	Skipped    int
	Malformed  int
	Failed     int
	FirstError error
}

func (r *Report) fail(err error) {
	r.Failed++
	if r.FirstError == nil {
		r.FirstError = err
	}
}

func (s *Service) Pass(ctx context.Context) (Report, error) {
	var rep Report
	u, err := s.account(ctx, "")
	if err != nil {
		return rep, err
	}
	me, err := s.Client.Me(ctx)
	if err != nil {
		return rep, err
	}
	peer := PeerIdentity(s.Cfg.BaseURL, me.ID)
	links, err := s.links(ctx, u.ID, peer)
	if err != nil {
		return rep, err
	}
	items, err := s.Client.Items(ctx)
	if err != nil {
		return rep, err
	}
	progress := make(map[string]Progress, len(me.MediaProgress))
	now := time.Now().UTC()
	for _, p := range me.MediaProgress {
		if err := p.Validate(now); err != nil {
			rep.Malformed++
			continue
		}
		progress[p.LibraryItemID] = p
	}
	seen := make(map[string]bool)
	for _, listed := range items {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		seen[listed.ID] = true
		if err := s.processItem(ctx, u.ID, peer, listed, progress, me.MediaProgress, now, &rep); err != nil {
			rep.fail(err)
		}
	}

	for id, l := range links {
		if !seen[id] && l.State != store.AudiobookLinkSuspended {
			l.State = store.AudiobookLinkSuspended
			if err := s.lock(ctx); err != nil {
				return rep, err
			}
			if err := s.Store.UpdateAudiobookLink(ctx, u.ID, l); err != nil {
				rep.fail(err)
			}
			s.unlock()
		}
	}
	return rep, nil
}

func progressMark(p Progress) string { return fmt.Sprintf("%.0f:%t", p.CurrentTime, p.IsFinished) }
func peerPrefix(peer string) string  { return "abs-" + peer[:8] + ":" }

func (s *Service) exchange(ctx context.Context, userID string, l *store.AudiobookLink, m Mapping, local store.Op, remote Progress, have bool) (string, error) {
	if s.Cfg.ActiveDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -s.Cfg.ActiveDays)
		if local.ClientTS.Before(cutoff) && (!have || remote.LastUpdate < cutoff.UnixMilli()) {
			return "skipped", nil
		}
	}
	sameEdition := local.EditionSHA == nil || *local.EditionSHA == l.EditionSHA
	remoteTime := remote.CurrentTime
	_, audioEnd, _, _ := m.Coverage()
	if remote.IsFinished {
		remoteTime = audioEnd
	}
	p, seg, mapped := m.ToBook(remoteTime)
	if remote.IsFinished && m.FullNarrative {
		p, mapped = 1, true
	}
	if have && mapped && local.Seq > 0 && sameEdition {
		tolerance := (seg.BookEnd - seg.BookStart) / (seg.AudioEnd - seg.AudioStart)
		complete := remote.IsFinished == (local.Progression >= 0.999 && m.CoversLast)
		if !m.FullNarrative && remote.IsFinished && nearProgress(local.Progression, 0.998) {
			complete = true
		}
		if math.Abs(local.Progression-p) <= math.Max(tolerance, 1e-9) && complete {
			l.PushedSeq, l.RemoteUpdatedMs = local.Seq, remote.LastUpdate
			return "skipped", nil
		}
	}
	echo := have && l.PushedMark != "" && progressMark(remote) == l.PushedMark &&
		remote.LastUpdate <= l.RemoteUpdatedMs
	if !echo {
		l.PushedMark = ""
	}
	if echo && local.Seq <= l.PushedSeq {
		return "skipped", nil
	}
	pull := have && !echo && (local.Seq == 0 || remote.LastUpdate > local.ClientTS.UnixMilli())
	if pull {
		if remote.LastUpdate <= l.RemoteUpdatedMs || !mapped || (remote.CurrentTime == 0 && local.Progression > 0) {
			return "skipped", nil
		}
		if err := s.ready(ctx, userID, *l, local.Seq); err != nil {
			return "", err
		}
		loc, err := m.LocatorAt(p, seg, remoteTime == seg.AudioStart)
		if err != nil {
			return "", err
		}
		raw := fmt.Sprintf("%s:%s:%s:%d:%.17g:%t", l.PeerIdentity, l.ItemID, l.Generation, remote.LastUpdate, remote.CurrentTime, remote.IsFinished)
		sum := sha256.Sum256([]byte(raw))
		op := store.Op{OpID: "abs:" + l.PeerIdentity + ":" + hex.EncodeToString(sum[:16]), WorkID: l.WorkID,
			EditionSHA: &l.EditionSHA, ClientTS: time.UnixMilli(remote.LastUpdate), Progression: p,
			LocatorJSON: loc, Origin: store.OriginAudiobookshelf}
		results, err := s.Store.AppendOps(ctx, userID, peerPrefix(l.PeerIdentity)+"audiobookshelf", []store.Op{op})
		if err != nil {
			return "", err
		}
		if len(results) != 1 || (results[0].Status != "applied" && results[0].Status != "duplicate") {
			return "", store.ErrConflict
		}
		l.PushedSeq, l.RemoteUpdatedMs = results[0].Seq, remote.LastUpdate
		return "pulled", nil
	}
	if local.Seq == 0 || !sameEdition || strings.HasPrefix(local.DeviceID, peerPrefix(l.PeerIdentity)) {
		return "skipped", nil
	}
	at, mapped := m.ToAudio(local.Progression)
	finished := local.Progression >= 0.999 && m.CoversLast
	if finished {
		at, mapped = audioEnd, true
	}
	if !mapped {
		return "skipped", nil
	}
	if err := s.ready(ctx, userID, *l, local.Seq); err != nil {
		return "", err
	}
	at = math.Min(at, m.Duration)
	if err := s.Client.UpdateProgress(ctx, l.ItemID, ProgressUpdate{CurrentTime: at, Duration: m.Duration,
		Progress: at / m.Duration, IsFinished: finished}); err != nil {
		return "", err
	}
	// Persist the mark even if the readback fails so the next pass recognizes the echo
	l.PushedSeq, l.PushedMark = local.Seq, progressMark(Progress{CurrentTime: at, IsFinished: finished})
	readback, err := s.Client.Progress(ctx, l.ItemID)
	if err != nil {
		return "pushed", err
	}
	if err := readback.Validate(time.Now()); err != nil {
		return "pushed", err
	}
	if progressMark(readback) == l.PushedMark {
		l.RemoteUpdatedMs = readback.LastUpdate
	}
	return "pushed", nil
}

func nearProgress(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func (s *Service) processItem(ctx context.Context, userID, peer string, listed Item, progress map[string]Progress, rawProgress []Progress, now time.Time, rep *Report) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	u, err := s.account(ctx, userID)
	if err != nil {
		return err
	}
	links, err := s.links(ctx, u.ID, peer)
	if err != nil {
		return err
	}
	rejects, err := s.Store.AudiobookRejections(ctx, u.ID, peer)
	if err != nil {
		return err
	}
	rejected := make(map[store.AudiobookRejection]bool, len(rejects))
	for _, r := range rejects {
		rejected[r] = true
	}
	reserved := make(map[string]bool)
	for _, l := range links {
		reserved[l.WorkID] = true
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rep.Considered++
	l, linked := links[listed.ID]
	if !linked {
		book, err := Match(ctx, s.Store, u.ID, listed)
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrAmbiguous) {
			rep.Skipped++
			return nil
		}
		if err != nil {
			rep.fail(err)
			return nil
		}
		work, err := ResolveWork(ctx, s.Store, u.ID, book, now)
		if err != nil {
			rep.fail(err)
			return nil
		}
		if reserved[work] || rejected[store.AudiobookRejection{WorkID: work, ItemID: listed.ID}] {
			rep.Skipped++
			return nil
		}
		item, err := s.Client.Item(ctx, listed.ID)
		if err != nil {
			rep.fail(err)
			return nil
		}
		l, err = s.newLink(ctx, u.ID, peer, book, item, "auto")
		if err != nil {
			rep.fail(err)
			return nil
		}
		if _, err := s.account(ctx, u.ID); err != nil {
			return err
		}
		if _, err := s.Store.CatalogBookByID(ctx, u.ID, book.ID); err != nil {
			rep.fail(err)
			return nil
		}
		if err := s.Store.CreateAudiobookLink(ctx, u.ID, l); err != nil {
			rep.fail(err)
			return nil
		}
		current, err := s.links(ctx, u.ID, peer)
		if err != nil {
			return err
		}
		l, reserved[work] = current[item.ID], true
	}
	item, err := s.Client.Item(ctx, l.ItemID)
	if err != nil {
		rep.fail(err)
		return nil
	}
	l, err = s.revalidate(ctx, u.ID, l, item)
	if err != nil {
		rep.fail(err)
		return nil
	}
	if l.State != store.AudiobookLinkActive {
		rep.Skipped++
		return nil
	}
	var m Mapping
	if err := json.Unmarshal(l.Mapping, &m); err != nil {
		rep.fail(err)
		return nil
	}
	p, have := progress[l.ItemID]
	skipPosition := false
	if !have {
		// A malformed row must not be mistaken for absent remote progress
		malformed := false
		for _, raw := range rawProgress {
			malformed = malformed || raw.LibraryItemID == l.ItemID
		}
		if malformed {
			skipPosition = true
		}
		p = Progress{LibraryItemID: l.ItemID, Duration: item.Duration}
	}
	local, err := s.head(ctx, u.ID, l.WorkID)
	if err != nil {
		rep.fail(err)
		return nil
	}
	outcome := "skipped"
	if !skipPosition {
		outcome, err = s.exchange(ctx, u.ID, &l, m, local, p, have)
	}
	if err != nil {
		rep.fail(err)
	} else {
		switch outcome {
		case "pushed":
			rep.Pushed++
		case "pulled":
			rep.Pulled++
		default:
			rep.Skipped++
		}
	}
	if err == nil {
		err = s.importSessions(ctx, u.ID, &l, m, now, rep)
		if err != nil {
			rep.fail(err)
		}
	}
	if err != nil {
		l.LastError = err.Error()
	} else {
		l.LastError = ""
	}
	if e := s.Store.UpdateAudiobookCursor(ctx, u.ID, l); e != nil {
		rep.fail(e)
	}

	return nil
}
