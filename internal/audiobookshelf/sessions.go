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

func sessionRevision(peer string, remote Session) (string, string) {
	key := "abs:" + peer + ":" + remote.ID
	raw, _ := json.Marshal(struct {
		Started, Updated          int64
		Listening, Start, Current float64
		Device                    string
	}{remote.StartedAt, remote.UpdatedAt, remote.TimeListening, remote.StartTime, remote.CurrentTime, remote.DeviceInfo.DeviceID})
	sum := sha256.Sum256(raw)
	return key, key + ":" + hex.EncodeToString(sum[:16])
}

func (s *Service) importSessions(ctx context.Context, userID string, l *store.AudiobookLink, mapping Mapping, now time.Time, rep *Report) error {
	overlap := s.Cfg.SessionOverlap.Duration()
	if overlap <= 0 {
		overlap = 10 * time.Minute
	}
	boundary := l.SessionsWatermarkMs - overlap.Milliseconds()
	maxUpdated := l.SessionsWatermarkMs
	const perPage = 100
	for page := 0; ; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := s.Client.ItemSessions(ctx, l.ItemID, page, perPage)
		if err != nil {
			return err
		}
		if len(batch.Sessions) == 0 && batch.Total > page*perPage {
			return errors.New("audiobookshelf: incomplete session page")
		}
		reached := false
		for _, remote := range batch.Sessions {
			if err := remote.Validate(now); err != nil {
				if errors.Is(err, ErrMalformedSession) {
					rep.Malformed++
				}
				continue
			}
			if remote.LibraryItemID != l.ItemID {
				rep.Malformed++
				continue
			}
			if remote.UpdatedAt <= boundary {
				reached = true
				continue
			}
			if err := s.importSession(ctx, userID, *l, mapping, remote, rep); err != nil {
				return err
			}
			maxUpdated = max(maxUpdated, min(remote.UpdatedAt, now.UnixMilli()))
		}
		if reached || len(batch.Sessions) < perPage || (batch.NumPages > 0 && page+1 >= batch.NumPages) {
			break
		}
	}
	l.SessionsWatermarkMs = maxUpdated
	return nil
}

func (s *Service) importSession(ctx context.Context, userID string, l store.AudiobookLink, mapping Mapping, remote Session, rep *Report) error {
	key, id := sessionRevision(l.PeerIdentity, remote)
	imported, err := s.Store.AudiobookSessionImported(ctx, userID, key, id)
	if err != nil {
		return err
	}
	if imported {
		return nil
	}
	local, err := s.head(ctx, userID, l.WorkID)
	if err != nil {
		return err
	}
	workID, edition := l.WorkID, l.EditionSHA
	client := strings.ReplaceAll(fold(remote.DeviceInfo.ClientName), " ", "-")
	if client == "" {
		client = "audiobookshelf"
	}
	if r := []rune(client); len(r) > 40 {
		client = string(r[:40])
	}
	device := peerPrefix(l.PeerIdentity) + client
	raw, err := json.Marshal(mapping)
	if err != nil {
		return err
	}
	ledger, err := s.Store.AudiobookSessionImport(ctx, userID, key)
	switch {
	case err == nil:
		current, err := s.Store.CurrentSourceSession(ctx, userID, key)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		workID, device, edition = current.WorkID, current.DeviceID, ""
		if current.EditionSHA != nil {
			edition = *current.EditionSHA
		}
		raw = ledger.Mapping
		if err := json.Unmarshal(raw, &mapping); err != nil {
			return err
		}
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	a0, a1, _, _ := mapping.Coverage()
	start := math.Max(a0, math.Min(a1, math.Min(mapping.Duration, remote.StartTime)))
	end := math.Max(a0, math.Min(a1, math.Min(mapping.Duration, remote.CurrentTime)))
	sp, _, okStart := mapping.ToBook(start)
	ep, _, okEnd := mapping.ToBook(end)
	if !okStart || !okEnd {
		sp, ep = 0, 0
	}
	active := int64(math.Round(remote.TimeListening * 1000))
	ses := store.Session{SessionID: id, WorkID: workID, EditionSHA: &edition, DeviceID: device,
		StartedAt: time.UnixMilli(remote.StartedAt), EndedAt: time.UnixMilli(remote.UpdatedAt),
		StartProg: sp, EndProg: ep, ActiveMs: &active, Origin: store.OriginAudiobookshelf, SourceKey: &key}
	if edition == "" {
		ses.EditionSHA = nil
	}
	if err := s.ready(ctx, userID, l, local.Seq); err != nil {
		return err
	}
	status, err := s.Store.AppendAudiobookSession(ctx, userID, ses, time.UnixMilli(remote.UpdatedAt), raw)
	if err != nil {
		return err
	}
	switch status {
	case "inserted", "superseded":
		rep.Imported++
	case "duplicate", "stale", "deleted":
	default:
		return fmt.Errorf("audiobookshelf: session revision conflict for %q", remote.ID)
	}
	return nil
}
