package workident

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// backfillPage is how many books are read per query. Small enough that a
// large library does not hold one oversized result set in memory, large
// enough that the per-book metadata reads dominate the cost.
const backfillPage = 200

// Report counts what a Backfill did. Conflicted and Fuzzy are the two
// outcomes an operator has to act on, so they are reported separately
// rather than folded into a single failure count.
type Report struct {
	Books      int
	Created    int
	Linked     int
	Fuzzy      int // matched on title/author alone, so left unmapped
	Conflicted int // identifiers named more than one work; nothing written
	Skipped    int // no identifiers at all, so nothing to resolve on
}

// Backfill maps every catalog book the user can read to a sync work,
// without waiting for a client to open each one. A reader who uploads a
// library and then asks for statistics otherwise sees nothing until they
// have opened every book, because the mapping is created lazily on first
// resolve (ADR-0003).
//
// It is the same operation the resolve route performs, driven over the
// whole catalog: same evidence, same store call, so a book backfilled here
// and the same book resolved later by a client land on one work.
//
// Fuzzy title/author matches are counted, never confirmed. Confirming one
// would merge two books into a single reading history on a guess, and the
// operator running a backfill is not the person who can say whether the
// guess is right. Such a book stays resolvable — a client can still confirm
// it — and the count tells the operator how many are waiting.
//
// Resolution is per book and each one commits on its own. A conflict or a
// book that vanishes mid-run is recorded and the walk continues: the point
// of a backfill is to map what can be mapped.
func Backfill(
	ctx context.Context,
	st store.Store,
	userID string,
	newID func() (string, error),
	now func() time.Time,
) (Report, error) {
	var report Report
	libs, err := st.ListLibraries(ctx, userID, store.LibraryRoleRead)
	if err != nil {
		return report, fmt.Errorf("list libraries: %w", err)
	}
	for _, lib := range libs {
		var cursor *store.CatalogBookCursor
		for {
			books, err := st.ListCatalogBooks(ctx, userID, lib.Library.ID, cursor, backfillPage)
			if err != nil {
				return report, fmt.Errorf("list books in %s: %w", lib.Library.ID, err)
			}
			if len(books) == 0 {
				break
			}
			for _, book := range books {
				if err := backfillBook(ctx, st, userID, book.ID, newID, now, &report); err != nil {
					return report, err
				}
			}
			last := books[len(books)-1]
			cursor = &store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
	}
	return report, nil
}

func backfillBook(
	ctx context.Context,
	st store.Store,
	userID, bookID string,
	newID func() (string, error),
	now func() time.Time,
	report *Report,
) error {
	report.Books++
	meta, err := st.CatalogBookMetadata(ctx, userID, bookID, store.LibraryRoleRead)
	if err != nil {
		// Trashed or purged since the page was read. Nothing to map.
		if errors.Is(err, store.ErrNotFound) {
			report.Skipped++
			return nil
		}
		return fmt.Errorf("metadata for %s: %w", bookID, err)
	}
	files, err := st.ListBookFiles(ctx, userID, bookID, store.LibraryRoleRead)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("files for %s: %w", bookID, err)
	}

	workID, err := newID()
	if err != nil {
		return fmt.Errorf("id generation: %w", err)
	}
	proposed, editions, ids := Plan(userID, workID, meta, files)
	if len(ids) == 0 {
		// A book with no digest, no embedded id and no title has nothing
		// a second device could recognise it by. The store would still
		// mint a work for it via the source alias, but that work could
		// never be reached from anywhere else.
		report.Skipped++
		return nil
	}
	at := now()
	proposed.CreatedAt = at

	result, err := st.ResolveCatalogBookWork(ctx, userID, bookID, proposed, editions, ids, false, at)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			report.Skipped++
			return nil
		}
		if errors.Is(err, store.ErrConflict) {
			report.Conflicted++
			return nil
		}
		return fmt.Errorf("resolve %s: %w", bookID, err)
	}
	switch {
	case len(result.ConflictingWorkIDs) > 0:
		report.Conflicted++
	// A low-confidence match rolls the transaction back, so the book is
	// counted as awaiting confirmation and not as linked.
	case result.Confidence == "low":
		report.Fuzzy++
	case result.Created:
		report.Created++
	default:
		report.Linked++
	}
	return nil
}
