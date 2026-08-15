package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// reviewListLimit bounds one listing. A review queue that needs paging is
// a sign something is wrong with the root rather than with the queue, and
// printing ten thousand lines would bury that signal.
const reviewListLimit = 200

// listReview shows the books a watched sweep refused to decide about.
//
// Without this the review status would be a state the server can enter
// and nobody can observe. The sweep deliberately does not act on a
// changed path — it cannot know whether the new bytes are a better scan
// of the same book or an entirely different one — so somebody has to be
// told there is a question waiting.
func listReview(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: list-review <actor> <library-id>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	books, err := st.ListBooksInReview(ctx, u.ID, args[1], reviewListLimit)
	if err != nil {
		return err
	}
	if len(books) == 0 {
		fmt.Println("nothing awaiting review")
		return nil
	}
	for _, b := range books {
		fmt.Printf("%s  %-24s %-20s %s\n",
			b.ID, b.UpdatedAt.UTC().Format(time.RFC3339),
			b.ReviewReason, b.Title)
	}
	if len(books) == reviewListLimit {
		fmt.Printf("(showing the first %d)\n", reviewListLimit)
	}
	return nil
}

// ClearBookReview records that somebody has looked at a book and is
// content with the copy the catalog is serving. It reports whether the
// book was in review at all, so a surface can tell "cleared" from
// "there was nothing to clear" rather than claiming both.
//
// It clears the flag and nothing else: the book returns to `missing`
// and the availability pass, which is the only thing allowed to call a
// book servable, restores it on its next run. Re-ingesting the changed
// file instead would be this guessing the answer to the question review
// exists to ask.
func ClearBookReview(ctx context.Context, st store.Store, libraryID, bookID string) (bool, error) {
	return st.SetCatalogBookReview(ctx, libraryID, bookID, "", time.Now().UTC())
}

// clearReview records that an administrator has looked at a book and is
// content with the copy the catalog is serving.
//
// It clears the flag and nothing else: the book returns to `missing` and
// the availability pass, which is the only thing allowed to call a book
// servable, restores it on its next run. Re-ingesting the changed file
// instead would be this command guessing the answer to the question it
// exists to ask.
func clearReview(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 3 {
		return errors.New("usage: clear-review <actor> <library-id> <book-id>")
	}
	if _, err := st.UserByName(ctx, args[0]); err != nil {
		return err
	}
	changed, err := ClearBookReview(ctx, st, args[1], args[2])
	if err != nil {
		return err
	}
	if !changed {
		fmt.Println("that book was not awaiting review")
		return nil
	}
	fmt.Println("cleared; the book returns to the catalog on the next " +
		"availability pass if it still has a servable file")
	return nil
}
