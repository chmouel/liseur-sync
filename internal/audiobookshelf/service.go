package audiobookshelf

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

type Service struct {
	Store    store.Store
	Client   *Client
	Cfg      config.AudiobookshelfConfig
	gate     chan struct{}
	loadBook func(context.Context, store.CatalogBook) ([]BookChapter, []Resource, error)
}

type ItemView struct {
	BookTitle        string
	BookSubtitle     string
	BookAuthors      []string
	BookCanRead      bool
	BookPresent      bool
	Item             Item
	Link             *store.AudiobookLink
	Mapping          Mapping
	LinearAllowed    bool
	RemoteProgress   *Progress
	LocalProgression *float64
}

func NewService(st store.Store, cfg config.AudiobookshelfConfig) *Service {
	return &Service{Store: st, Client: NewClient(cfg), Cfg: cfg, gate: make(chan struct{}, 1)}
}

func (s *Service) account(ctx context.Context, userID string) (store.User, error) {
	if !s.Cfg.Enabled {
		return store.User{}, store.ErrNotFound
	}
	u, err := s.Store.UserByName(ctx, s.Cfg.Account)
	if err != nil {
		return u, err
	}
	if u.DisabledAt != nil || (userID != "" && userID != u.ID) {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

func (s *Service) links(ctx context.Context, userID, peer string) (map[string]store.AudiobookLink, error) {
	list, err := s.Store.AudiobookLinks(ctx, userID, peer)
	out := make(map[string]store.AudiobookLink, len(list))
	for _, l := range list {
		out[l.ItemID] = l
	}
	return out, err
}

func (s *Service) chapters(ctx context.Context, book store.CatalogBook) ([]BookChapter, []Resource, error) {
	if s.loadBook != nil {
		return s.loadBook(ctx, book)
	}
	f, size, err := content.NewFiles(s.Store).OpenBook(ctx, book)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	pub, err := epub.OpenPublication(ctx, f, size, epub.DefaultLimits())
	if err != nil {
		return nil, nil, err
	}
	return BookAnchors(pub.Index(ctx, 100000), func(href string) ([]byte, error) { return pub.Resource(ctx, href) })
}

func (s *Service) mapping(ctx context.Context, book store.CatalogBook, item Item, linear bool) (Mapping, bool, error) {
	if err := validateAudio(item.Chapters, item.Duration); err != nil {
		return Mapping{}, false, err
	}
	chapters, resources, err := s.chapters(ctx, book)
	if err != nil {
		return Mapping{}, false, err
	}
	m, err := Align(chapters, resources, item.Chapters, item.Duration)
	allowed := errors.Is(err, ErrNoAlignment) && finite(item.Duration) && item.Duration > 0
	if linear {
		if !allowed {
			return Mapping{}, false, store.ErrInvalidInput
		}
		return LinearMapping(resources, item.Duration), allowed, nil
	}
	return m, allowed, err
}

func (s *Service) View(ctx context.Context, userID string) ([]ItemView, error) {
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()
	if _, err := s.account(ctx, userID); err != nil {
		return nil, err
	}
	me, err := s.Client.Me(ctx)
	if err != nil {
		return nil, err
	}
	peer := PeerIdentity(s.Cfg.BaseURL, me.ID)
	links, err := s.links(ctx, userID, peer)
	if err != nil {
		return nil, err
	}
	items, err := s.Client.Items(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	progressByItem := make(map[string]Progress, len(me.MediaProgress))
	for _, p := range me.MediaProgress {
		if p.Validate(now) == nil {
			progressByItem[p.LibraryItemID] = p
		}
	}
	out := make([]ItemView, 0, len(items))
	seen := make(map[string]bool)
	var bookIDs []string
	for _, item := range items {
		seen[item.ID] = true
		row := ItemView{Item: item}
		if p, ok := progressByItem[item.ID]; ok {
			pCopy := p
			row.RemoteProgress = &pCopy
		}
		if l, ok := links[item.ID]; ok {
			row.Link = &l
			if local, err := s.head(ctx, userID, l.WorkID); err == nil && local.Seq > 0 {
				prog := local.Progression
				row.LocalProgression = &prog
			}
			if book, err := s.Store.CatalogBookByID(ctx, userID, l.BookID); err == nil {
				row.BookTitle = book.Title
				row.BookSubtitle = book.Subtitle
				row.BookPresent = book.Status == store.BookActive
				row.BookCanRead = book.Status == store.BookActive && book.MediaType == "application/epub+zip"
				bookIDs = append(bookIDs, book.ID)
			}
			if len(l.Mapping) > 0 {
				_ = json.Unmarshal(l.Mapping, &row.Mapping)
			}
			if l.State == store.AudiobookLinkNeedsMapping {
				book, e := s.Store.CatalogBookByID(ctx, userID, l.BookID)
				if e == nil {
					expanded, e := s.Client.Item(ctx, item.ID)
					if e == nil {
						if len(row.Item.Chapters) == 0 {
							row.Item.Chapters = expanded.Chapters
						}
						if row.Item.Duration == 0 {
							row.Item.Duration = expanded.Duration
						}
						var m Mapping
						m, row.LinearAllowed, e = s.mapping(ctx, book, expanded, l.ConfirmedLinear)
						if e == nil || errors.Is(e, ErrNoAlignment) {
							row.Mapping = m
						}
					}
				}
			}
		}
		out = append(out, row)
	}
	for id, l := range links {
		if !seen[id] {
			l.State = store.AudiobookLinkSuspended
			row := ItemView{Item: Item{ID: id, Title: "Unavailable audiobook"}, Link: &l}
			if local, err := s.head(ctx, userID, l.WorkID); err == nil && local.Seq > 0 {
				prog := local.Progression
				row.LocalProgression = &prog
			}
			if book, err := s.Store.CatalogBookByID(ctx, userID, l.BookID); err == nil {
				row.BookTitle = book.Title
				row.BookSubtitle = book.Subtitle
				row.BookPresent = book.Status == store.BookActive
				row.BookCanRead = book.Status == store.BookActive && book.MediaType == "application/epub+zip"
				bookIDs = append(bookIDs, book.ID)
			}
			out = append(out, row)
		}
	}
	if len(bookIDs) > 0 {
		if rel, err := s.Store.CatalogBookRelationsForBooks(ctx, userID, bookIDs); err == nil {
			for i := range out {
				if out[i].Link == nil {
					continue
				}
				for _, c := range rel.Contributors[out[i].Link.BookID] {
					if c.Name != "" {
						out[i].BookAuthors = append(out[i].BookAuthors, c.Name)
					}
				}
			}
		}
	}
	return out, nil
}

func (s *Service) Link(ctx context.Context, userID, itemID, bookID string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if _, err := s.account(ctx, userID); err != nil {
		return err
	}
	me, err := s.Client.Me(ctx)
	if err != nil {
		return err
	}
	item, err := s.Client.Item(ctx, itemID)
	if err != nil {
		return err
	}
	if item.ID != itemID || !storable(item.ID) || item.MediaType != "book" || !finite(item.Duration) || item.Duration <= 0 {
		return store.ErrInvalidInput
	}
	book, err := s.Store.CatalogBookByID(ctx, userID, bookID)
	if err != nil {
		return err
	}
	l, err := s.newLink(ctx, userID, PeerIdentity(s.Cfg.BaseURL, me.ID), book, item, "manual")
	if err != nil {
		return err
	}
	// Recheck the grant after opening the publication and resolving its work
	if _, err := s.Store.CatalogBookByID(ctx, userID, book.ID); err != nil {
		return err
	}
	if _, err := s.account(ctx, userID); err != nil {
		return err
	}
	return s.Store.CreateAudiobookLink(ctx, userID, l)
}

func (s *Service) newLink(ctx context.Context, userID, peer string, book store.CatalogBook, item Item, source string) (store.AudiobookLink, error) {
	if book.Status != store.BookActive {
		return store.AudiobookLink{}, store.ErrNotFound
	}
	m, _, err := s.mapping(ctx, book, item, false)
	if err != nil && !errors.Is(err, ErrNoAlignment) {
		return store.AudiobookLink{}, err
	}
	state := store.AudiobookLinkActive
	var raw []byte
	if err != nil {
		state = store.AudiobookLinkNeedsMapping
	} else {
		raw, err = json.Marshal(m)
		if err != nil {
			return store.AudiobookLink{}, err
		}
	}
	work, err := ResolveWork(ctx, s.Store, userID, book, time.Now().UTC())
	if err != nil {
		return store.AudiobookLink{}, err
	}
	return store.AudiobookLink{PeerIdentity: peer, ItemID: item.ID, WorkID: work, BookID: book.ID,
		EditionSHA: book.ContentSHA256, Source: source, State: state, Mapping: raw,
		MappingKey: ChaptersKey(book.ContentSHA256, item.Chapters, item.Duration, false)}, nil
}

func (s *Service) Mutate(ctx context.Context, userID, itemID, generation, action string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if _, err := s.account(ctx, userID); err != nil {
		return err
	}
	me, err := s.Client.Me(ctx)
	if err != nil {
		return err
	}
	peer := PeerIdentity(s.Cfg.BaseURL, me.ID)
	links, err := s.links(ctx, userID, peer)
	if err != nil {
		return err
	}
	l, ok := links[itemID]
	if !ok {
		return store.ErrNotFound
	}
	if l.Generation != generation {
		return store.ErrConflict
	}
	if action == "unlink" {
		return s.Store.DeleteAudiobookLink(ctx, userID, peer, itemID, generation, true)
	}
	if action != "confirm" && action != "linear" {
		return store.ErrInvalidInput
	}
	if l.State != store.AudiobookLinkNeedsMapping {
		return store.ErrConflict
	}
	linear := action == "linear" || (action == "confirm" && l.ConfirmedLinear)
	book, err := s.Store.CatalogBookByID(ctx, userID, l.BookID)
	if err != nil {
		return err
	}
	if book.Status != store.BookActive {
		return store.ErrNotFound
	}
	item, err := s.Client.Item(ctx, itemID)
	if err != nil {
		return err
	}
	if l.MappingKey != ChaptersKey(book.ContentSHA256, item.Chapters, item.Duration, l.ConfirmedLinear) {
		return store.ErrConflict
	}
	m, _, err := s.mapping(ctx, book, item, linear)
	if err != nil {
		return err
	}
	if _, err := s.Store.CatalogBookByID(ctx, userID, book.ID); err != nil {
		return err
	}
	l.Mapping, err = json.Marshal(m)
	if err != nil {
		return err
	}
	l.ConfirmedLinear = linear
	l.MappingKey = ChaptersKey(book.ContentSHA256, item.Chapters, item.Duration, l.ConfirmedLinear)
	l.EditionSHA, l.State = book.ContentSHA256, store.AudiobookLinkActive
	if _, err := s.account(ctx, userID); err != nil {
		return err
	}
	return s.Store.UpdateAudiobookLink(ctx, userID, l)
}

func (s *Service) ready(ctx context.Context, userID string, l store.AudiobookLink, expectedSeq int64) error {
	return s.readyBook(ctx, userID, l, expectedSeq, true)
}

// readyBook rechecks everything a write depends on. Opening the ebook to
// re-derive its anchors is only needed before a position exchange; a session
// import already used the mapping validated earlier in the pass.
func (s *Service) readyBook(ctx context.Context, userID string, l store.AudiobookLink, expectedSeq int64, anchors bool) error {
	if _, err := s.account(ctx, userID); err != nil {
		return err
	}
	links, err := s.links(ctx, userID, l.PeerIdentity)
	if err != nil {
		return err
	}
	current, ok := links[l.ItemID]
	if !ok || current.Generation != l.Generation || current.State != store.AudiobookLinkActive {
		return store.ErrConflict
	}
	book, err := s.Store.CatalogBookByID(ctx, userID, l.BookID)
	if err != nil {
		return err
	}
	if book.Status != store.BookActive || book.ContentSHA256 != l.EditionSHA {
		return store.ErrConflict
	}
	if anchors {
		if _, _, err := s.chapters(ctx, book); err != nil {
			return err
		}
	}
	if expectedSeq >= 0 {
		local, err := s.head(ctx, userID, l.WorkID)
		if err != nil {
			return err
		}
		if local.Seq != expectedSeq {
			return store.ErrConflict
		}
	}
	return nil
}

func (s *Service) head(ctx context.Context, userID, workID string) (store.Op, error) {
	ops, err := s.Store.Positions(ctx, userID, workID, 1)
	if err != nil || len(ops) == 0 {
		return store.Op{}, err
	}
	return ops[0], nil
}

func (s *Service) revalidate(ctx context.Context, userID string, l store.AudiobookLink, item Item) (store.AudiobookLink, error) {
	book, err := s.Store.CatalogBookByID(ctx, userID, l.BookID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && book.Status != store.BookActive) {
		ids, e := s.Store.WorkBookIDs(ctx, userID, l.WorkID)
		if e != nil {
			return l, e
		}
		for _, id := range ids {
			candidate, e := s.Store.CatalogBookByID(ctx, userID, id)
			if e == nil && candidate.Status == store.BookActive {
				book, err = candidate, nil
				break
			}
		}
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return l, err
	}
	state := l.State
	changed := false
	if err != nil || book.Status != store.BookActive {
		state = store.AudiobookLinkSuspended
	} else {
		key := ChaptersKey(book.ContentSHA256, item.Chapters, item.Duration, l.ConfirmedLinear)
		_, _, e := s.mapping(ctx, book, item, l.ConfirmedLinear)
		if e != nil && !errors.Is(e, ErrNoAlignment) {
			state = store.AudiobookLinkSuspended
		} else if key != l.MappingKey || book.ID != l.BookID {
			changed = true
			l.BookID, l.EditionSHA, l.MappingKey = book.ID, book.ContentSHA256, key
			// A recomputed alignment is only a preview until the reader
			// confirms it, so it is not stored here; View derives it for
			// display and Mutate recomputes it on confirmation. Keeping it
			// would let a later suspension look like a confirmed mapping.
			l.Mapping = nil
			state = store.AudiobookLinkNeedsMapping
		} else if state == store.AudiobookLinkSuspended {
			state = store.AudiobookLinkActive
			if len(l.Mapping) == 0 {
				state = store.AudiobookLinkNeedsMapping
			}
		}
	}
	if state != l.State || changed {
		l.State = state
		if err := s.Store.UpdateAudiobookLink(ctx, userID, l); err != nil {
			return l, err
		}
		links, err := s.links(ctx, userID, l.PeerIdentity)
		if err != nil {
			return l, err
		}
		l = links[l.ItemID]
	}
	return l, nil
}

func (s *Service) lock(ctx context.Context) error {
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) unlock() { <-s.gate }
