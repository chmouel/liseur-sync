package workident

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// ErrCursorStalled reports a paged walk that stopped making progress.
// It can only come from a store whose listing disagrees with its own
// cursor, so it is a bug rather than a condition to retry.
var ErrCursorStalled = errors.New("workident: paging cursor did not advance")

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
	var folderCursor string
	for {
		folders, err := st.ListFolders(ctx, folderCursor, backfillPage)
		if err != nil {
			return report, fmt.Errorf("list folders: %w", err)
		}
		if len(folders) == 0 {
			break
		}
		for _, folder := range folders {
			var cursor *store.CatalogBookCursor
			for {
				books, err := st.ListCatalogBooks(ctx, folder.ID, cursor, backfillPage)
				if err != nil {
					return report, fmt.Errorf("list books in %s: %w", folder.ID, err)
				}
				if len(books) == 0 {
					break
				}
				for _, book := range books {
					if err := backfillBook(ctx, st, userID, book, newID, now, &report); err != nil {
						return report, err
					}
				}
				last := books[len(books)-1]
				next := store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID}
				// A page that ends where the last one ended is a walk
				// that will read the same books forever. Nothing here
				// can fix it, and a background job that spins is worse
				// than one that stops and says why.
				if cursor != nil && next.CreatedAt.Equal(cursor.CreatedAt) && next.ID == cursor.ID {
					return report, fmt.Errorf(
						"%w: books in folder %s stopped advancing at %s",
						ErrCursorStalled, folder.ID, next.ID)
				}
				cursor = &next
			}
		}
		if len(folders) < backfillPage {
			break
		}
		next := store.FolderCursor(folders[len(folders)-1])
		if next == folderCursor {
			return report, fmt.Errorf(
				"%w: folders stopped advancing at %q", ErrCursorStalled, next)
		}
		folderCursor = next
	}
	return report, nil
}

func backfillBook(
	ctx context.Context,
	st store.Store,
	userID string,
	book store.CatalogBook,
	newID func() (string, error),
	now func() time.Time,
	report *Report,
) error {
	report.Books++
	ids, author, err := Evidence(ctx, st, book.ID)
	if err != nil {
		// Replaced or its folder removed since the page was read.
		// Nothing to map.
		if errors.Is(err, store.ErrNotFound) {
			report.Skipped++
			return nil
		}
		return fmt.Errorf("evidence for %s: %w", book.ID, err)
	}

	workID, err := newID()
	if err != nil {
		return fmt.Errorf("id generation: %w", err)
	}
	proposed, editions, aliases := Plan(userID, workID, book, ids, author)
	if len(aliases) == 0 {
		// A book with no digest, no embedded id and no title has nothing
		// a second device could recognise it by. The store would still
		// mint a work for it via the source alias, but that work could
		// never be reached from anywhere else.
		report.Skipped++
		return nil
	}
	at := now()
	proposed.CreatedAt = at

	result, err := st.ResolveCatalogBookWork(ctx, userID, book.ID, proposed, editions, aliases, false, at)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			report.Skipped++
			return nil
		}
		if errors.Is(err, store.ErrConflict) {
			report.Conflicted++
			return nil
		}
		return fmt.Errorf("resolve %s: %w", book.ID, err)
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

// Evidence gathers what the catalog knows about one book beyond the book
// row itself: its publication identifiers and its primary author.
//
// It lives here so that the resolve route and the backfill collect the
// same evidence. If they collected different evidence, a book resolved
// by an operator in advance and the same book resolved by a client on
// first read could land on two different works, and the reader's
// position would silently stop following them between devices.
func Evidence(
	ctx context.Context, st store.Store, bookID string,
) ([]store.BookIdentifier, string, error) {
	ids, err := st.CatalogBookIdentifiers(ctx, bookID)
	if err != nil {
		return nil, "", err
	}
	authors, err := st.CatalogAuthorsForBooks(ctx, []string{bookID})
	if err != nil {
		return nil, "", err
	}
	var author string
	if names := authors[bookID]; len(names) > 0 {
		author = names[0]
	}
	return ids, author, nil
}
