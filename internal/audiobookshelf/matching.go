package audiobookshelf

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/workident"
)

// ErrAmbiguous is an audiobook more than one visible book could be.
var ErrAmbiguous = errors.New("audiobookshelf: more than one book matches")

var (
	trailingBracket = regexp.MustCompile(`\s*[(\[][^()\[\]]*[)\]]\s*$`)
	trailingVolume  = regexp.MustCompile(`[\s,.\-–—]*\b(?:book|tome|volume|vol\.?|livre)\s+\S+\s*$`)
)

// titleKey reduces a title to what an audiobook and an ebook of the same
// book can be expected to share: no subtitle, no series volume, no
// case, accents or punctuation.
func titleKey(title string) string {
	t := strings.TrimSpace(title)
	for {
		s := trailingBracket.ReplaceAllString(t, "")
		if s == t {
			break
		}
		t = s
	}
	if i := strings.IndexByte(t, ':'); i > 0 {
		t = t[:i]
	}
	t = trailingVolume.ReplaceAllString(t, "")
	return fold(t)
}

// fold lowercases, drops accents and turns punctuation into spaces.
func fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range store.FoldForSearch(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

// Match finds the one catalog book, among those viewerID can see, that
// an audiobook is: the same title once reduced, and its first author
// among the book's. It errs on the side of matching nothing; anything
// it leaves alone a reader can link by hand.
func Match(ctx context.Context, st store.Store, viewerID string, item Item) (store.CatalogBook, error) {
	key := titleKey(item.Title)
	if key == "" || len(item.Authors) == 0 {
		return store.CatalogBook{}, store.ErrNotFound
	}
	author := fold(item.Authors[0])
	var found []store.CatalogBook
	var after string
	for {
		folders, err := st.ListUserFolders(ctx, viewerID, after, 100)
		if err != nil {
			return store.CatalogBook{}, err
		}
		for _, f := range folders {
			res, err := st.SearchCatalogBooks(ctx, store.SearchQuery{
				FolderID: f.ID, UserID: viewerID, Text: key, Limit: store.MaxSearchLimit,
			})
			if err != nil {
				return store.CatalogBook{}, err
			}
			if res.Truncated {
				return store.CatalogBook{}, ErrAmbiguous
			}
			var ids []string
			for _, b := range res.Books {
				if b.Status == store.BookActive && titleKey(b.Title) == key {
					ids = append(ids, b.ID)
				}
			}
			if len(ids) == 0 {
				continue
			}
			authors, err := st.CatalogAuthorsForBooks(ctx, viewerID, ids)
			if err != nil {
				return store.CatalogBook{}, err
			}
			for _, b := range res.Books {
				for _, a := range authors[b.ID] {
					if fold(a) == author {
						found = append(found, b)
						break
					}
				}
			}
		}
		if len(folders) < 100 {
			break
		}
		after = store.FolderCursor(folders[len(folders)-1])
	}
	switch len(found) {
	case 0:
		return store.CatalogBook{}, store.ErrNotFound
	case 1:
		return found[0], nil
	}
	return store.CatalogBook{}, ErrAmbiguous
}

// ResolveWork returns the reader's work for a catalog book, creating it
// the way a first open would, so a linked audiobook and the reader's
// own devices land on the same work.
func ResolveWork(ctx context.Context, st store.Store, userID string, book store.CatalogBook, now time.Time) (string, error) {
	link, err := st.UserBookWork(ctx, userID, book.ID)
	switch {
	case err == nil:
		return link.WorkID, nil
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	bookIDs, author, err := workident.Evidence(ctx, st, userID, book.ID)
	if err != nil {
		return "", err
	}
	workID, err := auth.NewSecret()
	if err != nil {
		return "", err
	}
	proposed, editions, ids := workident.Plan(userID, workID, book, bookIDs, author)
	proposed.CreatedAt = now
	result, err := st.ResolveCatalogBookWork(ctx, userID, book.ID, proposed, editions, ids, true, now)
	if err != nil {
		return "", err
	}
	if result.WorkID == "" {
		return "", store.ErrConflict
	}
	return result.WorkID, nil
}
