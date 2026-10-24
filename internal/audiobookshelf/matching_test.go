package audiobookshelf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

func TestTitleKey(t *testing.T) {
	for in, want := range map[string]string{
		"Disquiet Gods: The Sun Eater: Book Six": "disquiet gods",
		"Shadows upon Time: Sun Eater, Book 7":   "shadows upon time",
		"Disquiet Gods (The Sun Eater #6)":       "disquiet gods",
		"Les Dieux inquiets, tome 6":             "les dieux inquiets",
		"Les Dieux Inquiets":                     "les dieux inquiets",
		"Élan vital [Unabridged]":                "elan vital",
		"The Book Thief":                         "the book thief",
		"Example, Book 6":                        "example",
		"Example, book 6":                        "example",
		"Example, Book VI":                       "example",
		"The Book Thief: A Novel":                "the book thief",
	} {
		if got := titleKey(in); got != want {
			t.Errorf("titleKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchFindsExactlyOneVisibleBook(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	want := f.book(t, "f-main", "dg", "Disquiet Gods", "Christopher Ruocchio")
	f.book(t, "f-main", "sut", "Shadows upon Time", "Christopher Ruocchio")
	f.book(t, "f-main", "other", "Disquiet Gods", "Somebody Else")

	item := Item{Title: "Disquiet Gods: The Sun Eater: Book Six", Authors: []string{"Christopher Ruocchio"}}
	got, err := Match(ctx, f.st, f.user.ID, item)
	if err != nil || got.ID != want {
		t.Fatalf("Match = %v %v, want %s", got.ID, err, want)
	}

	// No author, no match: a title alone is not enough.
	if _, err := Match(ctx, f.st, f.user.ID, Item{Title: "Disquiet Gods"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("authorless: %v", err)
	}

	// A copy in a folder the account cannot see is not a candidate.
	other := f.addUser(t, "other")
	f.addFolder(t, "f-hidden", other.ID)
	f.book(t, "f-hidden", "dg-hidden", "Disquiet Gods", "Christopher Ruocchio")
	if got, err := Match(ctx, f.st, f.user.ID, item); err != nil || got.ID != want {
		t.Fatalf("hidden copy changed the answer: %v %v", got.ID, err)
	}
	// The other account sees only its copy.
	if got, err := Match(ctx, f.st, other.ID, item); err != nil || got.ID != f.ids["dg-hidden"] {
		t.Fatalf("other account: %v %v", got.ID, err)
	}

	// A second visible copy makes it ambiguous.
	f.book(t, "f-main", "dg2", "Disquiet Gods", "Christopher Ruocchio")
	if _, err := Match(ctx, f.st, f.user.ID, item); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("two copies: %v", err)
	}
}

func TestMatchRefusesTruncatedSearch(t *testing.T) {
	f := newFixture(t)
	var observed []store.ObservedBook
	for i := 0; i < store.MaxSearchLimit+2; i++ {
		title := "Disquiet Gods"
		if i > 0 {
			title = fmt.Sprintf("Disquiet Gods Reference %d", i)
		}
		if i == store.MaxSearchLimit+1 {
			title = "Disquiet Gods: " + strings.Repeat("Extended subtitle ", 300)
		}
		observed = append(observed, store.ObservedBook{RelativePath: fmt.Sprintf("book-%d.epub", i), Title: title,
			SizeBytes: 1000, MTime: f.now, ContentSHA256: fmt.Sprintf("%064x", i+1),
			Contributors: []store.ObservedContributor{{Name: "Christopher Ruocchio", Role: "author"}}})
	}
	if _, err := f.st.ReconcileFolder(t.Context(), f.folder.ID, observed, true, f.now); err != nil {
		t.Fatal(err)
	}
	res, err := f.st.SearchCatalogBooks(t.Context(), store.SearchQuery{UserID: f.user.ID, FolderID: f.folder.ID, Text: "disquiet gods", Limit: store.MaxSearchLimit})
	if err != nil || !res.Truncated {
		t.Fatalf("fixture is not truncated: %v", err)
	}
	exact := 0
	for _, b := range res.Books {
		if titleKey(b.Title) == "disquiet gods" {
			exact++
		}
	}
	if exact != 1 {
		t.Fatalf("fixture must hide the second exact match beyond the cut; got %d", exact)
	}
	item := Item{Title: "Disquiet Gods", Authors: []string{"Christopher Ruocchio"}}
	if _, err := Match(context.Background(), f.st, f.user.ID, item); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("truncated search: %v", err)
	}
}
