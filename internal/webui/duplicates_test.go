package webui

import (
	"reflect"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestGroupDuplicatesKeepsRunsTogetherAndDropsAHalfOne covers the
// grouping the duplicate report is built from. Two cases matter and
// neither is visible from the page: a run must not be broken by the
// first book carrying an empty digest, and the limit must not leave a
// trailing group of one, which would read as a book duplicating itself.
func TestGroupDuplicatesKeepsRunsTogetherAndDropsAHalfOne(t *testing.T) {
	book := func(id, title, sha string) store.DuplicateContentBook {
		return store.DuplicateContentBook{
			Book:   store.CatalogBook{ID: id, Title: title},
			SHA256: sha,
		}
	}
	for _, tc := range []struct {
		name  string
		books []store.DuplicateContentBook
		want  []DuplicateGroup
	}{
		{name: "nothing"},
		{
			name: "one run",
			books: []store.DuplicateContentBook{
				book("a", "First", "aa"), book("b", "Second", "aa"),
			},
			want: []DuplicateGroup{{Titles: []string{"First", "Second"}}},
		},
		{
			name: "two runs",
			books: []store.DuplicateContentBook{
				book("a", "First", "aa"), book("b", "Second", "aa"),
				book("c", "Third", "bb"), book("d", "Fourth", "bb"),
			},
			want: []DuplicateGroup{
				{Titles: []string{"First", "Second"}},
				{Titles: []string{"Third", "Fourth"}},
			},
		},
		{
			// What the limit does when it lands mid-run.
			name: "trailing half group",
			books: []store.DuplicateContentBook{
				book("a", "First", "aa"), book("b", "Second", "aa"),
				book("c", "Third", "bb"),
			},
			want: []DuplicateGroup{{Titles: []string{"First", "Second"}}},
		},
		{
			// A single book cut off from its partner is the whole report,
			// and reporting it alone would be a lie about that book.
			name:  "only a half group",
			books: []store.DuplicateContentBook{book("a", "Lonely", "aa")},
		},
		{
			// An empty digest is not a group boundary in itself: the run
			// has to start at the first book whatever it carries.
			name: "empty digest run",
			books: []store.DuplicateContentBook{
				book("a", "First", ""), book("b", "Second", ""),
			},
			want: []DuplicateGroup{{Titles: []string{"First", "Second"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := groupDuplicates(tc.books)
			if len(got) != len(tc.want) {
				t.Fatalf("groups = %+v, want %+v", got, tc.want)
			}
			if len(got) > 0 && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("groups = %+v, want %+v", got, tc.want)
			}
		})
	}
}
