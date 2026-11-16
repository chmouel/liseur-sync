//go:build linux

package webui_test

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// statusBook names one seeded book and the work behind it.
type statusBook struct {
	book, work string
}

// seedStatusBook puts a book in the catalog and resolves a work for it,
// so a status act has something to hang from. at > 0 records a position
// first, the way a device would.
func seedStatusBook(t *testing.T, f *booksFixture, now time.Time, name string, at float64) statusBook {
	t.Helper()
	book := f.addBook(t, name, bytes.Repeat([]byte(name), 50))
	work := "w-" + name
	if _, err := f.st.ResolveCatalogBookWork(t.Context(), "u1", book,
		store.Work{ID: work, UserID: "u1", Title: name, CreatedAt: now},
		nil, nil, true, now); err != nil {
		t.Fatal(err)
	}
	if at > 0 {
		progressOn(t, f, work, "pos-"+name, at, now)
	}
	return statusBook{book: book, work: work}
}

func (f *booksFixture) shelf(t *testing.T, filter string) string {
	t.Helper()
	_, page := f.get(t, "/ui/library?folder="+f.folder+"&filter="+filter, f.cookie)
	return grid(page)
}

// TestLibraryShelvesOwnEveryStatus pins, per chip, which resolved statuses
// it holds. A book the reader asked to read later is on the unread pile;
// a paused or abandoned one is on neither the unread pile nor the reading
// one (ADR-0067), and an explicit reading is being read before it has a
// position.
func TestLibraryShelvesOwnEveryStatus(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()

	want := seedStatusBook(t, f, now, "wanted", 0)
	statusOn(t, f, want.work, "act-wanted", store.ReadingStatusWantToRead, 0, now)

	explicit := seedStatusBook(t, f, now, "explicit-reading", 0)
	statusOn(t, f, explicit.work, "act-explicit", store.ReadingStatusReading, 0, now)

	seedStatusBook(t, f, now, "midway", 0.4)

	paused := seedStatusBook(t, f, now, "paused", 0.8)
	statusOn(t, f, paused.work, "act-paused", store.ReadingStatusPaused, 0.8, now)

	abandoned := seedStatusBook(t, f, now, "abandoned", 0.3)
	statusOn(t, f, abandoned.work, "act-abandoned", store.ReadingStatusDidNotFinish, 0.3, now)

	pile := seedStatusBook(t, f, now, "pile", 0.99)
	statusOn(t, f, pile.work, "act-pile", store.ReadingStatusUnread, 0.99, now)

	seedStatusBook(t, f, now, "done", 1)

	// A catalog book nobody has opened: no work behind it at all.
	f.addBook(t, "never-opened", bytes.Repeat([]byte("never"), 50))

	for _, tc := range []struct {
		filter string
		want   []string
		absent []string
	}{
		{
			filter: "unread",
			// The never-opened book is here too: absence of a position
			// is what "unread" means for a book nobody has touched.
			want:   []string{">wanted<", ">pile<", ">never-opened<"},
			absent: []string{">paused<", ">abandoned<", ">midway<", ">done<"},
		},
		{
			filter: "reading",
			want:   []string{">explicit-reading<", ">midway<"},
			absent: []string{">paused<", ">abandoned<", ">wanted<", ">pile<", ">done<"},
		},
		{filter: "want_to_read", want: []string{">wanted<"}, absent: []string{">paused<", ">midway<"}},
		{filter: "paused", want: []string{">paused<"}, absent: []string{">wanted<", ">abandoned<", ">midway<"}},
		{filter: "did_not_finish", want: []string{">abandoned<"}, absent: []string{">paused<", ">midway<"}},
		{filter: "finished", want: []string{">done<"}, absent: []string{">paused<", ">abandoned<", ">midway<"}},
	} {
		t.Run(tc.filter, func(t *testing.T) {
			body := f.shelf(t, tc.filter)
			for _, name := range tc.want {
				if !strings.Contains(body, name) {
					t.Errorf("%s chip is missing %s:\n%s", tc.filter, name, body)
				}
			}
			for _, name := range tc.absent {
				if strings.Contains(body, name) {
					t.Errorf("%s chip holds %s:\n%s", tc.filter, name, body)
				}
			}
		})
	}

	// The details page and the chip must not disagree about a book the
	// reader asked to read later.
	if _, page := f.get(t, "/ui/books/"+want.book, f.cookie); !strings.Contains(page, ">Want to read<") {
		t.Errorf("a want-to-read book is not labelled so:\n%s", page)
	}
}

// TestEmptyStatusChipsExplainThemselves: an empty chip says what it holds
// rather than falling back to "nothing here yet, add books", which would
// read as an empty library instead of an empty shelf.
func TestEmptyStatusChipsExplainThemselves(t *testing.T) {
	f := newBooksFixture(t)
	for chip, want := range map[string]string{
		"want_to_read":   "Nothing waiting to be read",
		"paused":         "Nothing paused",
		"did_not_finish": "Nothing put aside",
	} {
		_, page := f.get(t, "/ui/library?folder="+f.folder+"&filter="+chip, f.cookie)
		if !strings.Contains(page, want) {
			t.Errorf("%s empty state does not say %q:\n%s", chip, want, page)
		}
	}
}

// TestStatusChipsSurviveCursorPaging: a chip about the reader's own
// status is answered from the reading half, so it is complete before any
// paging — a match older than the first catalog page is on the first
// answer. And a stale cursor arriving with a chip must not turn it back
// into the unfiltered catalog.
func TestStatusChipsSurviveCursorPaging(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()

	// Three matches, seeded first so they are the oldest rows in a
	// library larger than one page.
	for _, tc := range []struct {
		chip   string
		name   string
		status store.ReadingStatus
	}{
		{"want_to_read", "wanted-old", store.ReadingStatusWantToRead},
		{"paused", "paused-old", store.ReadingStatusPaused},
		{"did_not_finish", "abandoned-old", store.ReadingStatusDidNotFinish},
	} {
		sb := seedStatusBook(t, f, now, tc.name, 0.3)
		statusOn(t, f, sb.work, "act-"+tc.name, tc.status, 0.3, now)
	}
	for i := 0; i < 30; i++ {
		name := "filler-" + i2(i)
		f.addBook(t, name, bytes.Repeat([]byte(name), 20))
	}

	// One page of the whole catalog is not everything, and its
	// continuation carries the filter it was asked under.
	_, first := f.get(t, "/ui/library?folder="+f.folder+"&filter=all", f.cookie)
	next := libraryNextURL(t, first)
	if !strings.Contains(next, "filter=all") {
		t.Fatalf("continuation dropped the filter: %s", next)
	}
	parsed, err := url.Parse(next)
	if err != nil {
		t.Fatal(err)
	}
	cursor := parsed.Query().Get("cursor")
	if cursor == "" {
		t.Fatalf("continuation has no cursor: %s", next)
	}
	_, second := f.get(t, "/ui/library?"+parsed.RawQuery, f.cookie)
	if !strings.Contains(grid(second), ">wanted-old<") {
		t.Fatalf("the second catalog page did not reach the oldest rows:\n%s", grid(second))
	}

	for _, tc := range []struct{ chip, name string }{
		{"want_to_read", "wanted-old"},
		{"paused", "paused-old"},
		{"did_not_finish", "abandoned-old"},
	} {
		t.Run(tc.chip, func(t *testing.T) {
			body := f.shelf(t, tc.chip)
			if !strings.Contains(body, ">"+tc.name+"<") {
				t.Fatalf("%s omitted a match beyond the first catalog page:\n%s", tc.chip, body)
			}
			// Its answer is complete, so there is nothing to page to.
			if strings.Contains(body, "cursor") {
				t.Fatalf("%s asked the reader to page a complete answer:\n%s", tc.chip, body)
			}
			// A cursor that arrived from somewhere else does not widen
			// the chip into the whole catalog.
			_, stale := f.get(t,
				"/ui/library?folder="+f.folder+"&filter="+tc.chip+"&cursor="+url.QueryEscape(cursor),
				f.cookie)
			staleBody := grid(stale)
			if !strings.Contains(staleBody, ">"+tc.name+"<") {
				t.Errorf("%s lost its match under a stale cursor:\n%s", tc.chip, staleBody)
			}
			if strings.Contains(staleBody, ">filler-") {
				t.Errorf("%s became the catalog under a stale cursor:\n%s", tc.chip, staleBody)
			}
		})
	}
}

// readingState is the status block on a book page, from its opening tag to
// the call-to-action below it.
func readingState(t *testing.T, page string) string {
	t.Helper()
	const open = `<div class="reading-state">`
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("no reading-state block:\n%s", page)
	}
	j := strings.Index(page[i:], `<div class="cta">`)
	if j < 0 {
		t.Fatalf("no call-to-action after the reading-state block:\n%s", page)
	}
	return page[i : i+j]
}

// TestBookPageNamesEveryStatus: each resolved status has its own label, so
// a paused or abandoned book is never called Reading, and an explicit
// reading before any position is not given a percentage it does not have.
// The selector marks as selected exactly one option, and it is the choice
// the reader made (ADR-0066, ADR-0067).
func TestBookPageNamesEveryStatus(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()
	ptr := func(v float64) *float64 { return &v }

	for _, tc := range []struct {
		name     string
		choice   store.ReadingStatus
		position *float64
		label    string
		selected string
		// percent is whether the status tag itself carries a fraction.
		percent bool
		// note is other text the block must keep.
		note string
	}{
		{
			name:  "no choice and no position is unread",
			label: "Unread", selected: "automatic",
		},
		{
			name:   "an explicit automatic is the same page as no choice",
			choice: store.ReadingStatusAutomatic, position: ptr(0.5),
			label: "Reading 50%", selected: "automatic", percent: true,
		},
		{
			name:   "want to read",
			choice: store.ReadingStatusWantToRead, position: ptr(0.4),
			label: "Want to read", selected: "want_to_read",
		},
		{
			name:   "explicit reading has no percentage to show yet",
			choice: store.ReadingStatusReading,
			label:  "Reading", selected: "reading",
		},
		{
			name:     "reading by position keeps its percentage",
			position: ptr(0.4),
			label:    "Reading 40%", selected: "automatic", percent: true,
		},
		{
			name:   "paused is not called reading",
			choice: store.ReadingStatusPaused, position: ptr(0.8),
			label: "Paused", selected: "paused",
		},
		{
			name:   "did not finish is not called reading",
			choice: store.ReadingStatusDidNotFinish, position: ptr(0.3),
			label: "Did not finish", selected: "did_not_finish",
		},
		{
			name:   "finished",
			choice: store.ReadingStatusFinished, position: ptr(0.4),
			label: "Finished", selected: "read",
		},
		{
			name:   "unread keeps its place without being called reading",
			choice: store.ReadingStatusUnread, position: ptr(0.6),
			label: "Unread", selected: "unread", note: "left at 60%",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "book-" + strings.ReplaceAll(tc.name, " ", "-")
			sb := seedStatusBook(t, f, now, name, 0)
			if tc.position != nil {
				progressOn(t, f, sb.work, "pos-"+name, *tc.position, now)
			}
			if tc.choice != "" {
				statusOn(t, f, sb.work, "act-"+name, tc.choice, 0, now)
			}
			_, page := f.get(t, "/ui/books/"+sb.book, f.cookie)
			state := readingState(t, page)
			if !strings.Contains(state, ">"+tc.label) {
				t.Errorf("label = %q, want %q:\n%s", state, tc.label, state)
			}
			if !strings.HasPrefix(tc.label, "Reading") && strings.Contains(state, "Reading") {
				t.Errorf("a book that is not being read is called Reading:\n%s", state)
			}
			if got := strings.Contains(state, "Reading "); got != tc.percent {
				t.Errorf("the status tag shows a percentage = %t, want %t:\n%s",
					got, tc.percent, state)
			}
			if tc.note != "" && !strings.Contains(state, tc.note) {
				t.Errorf("the block dropped %q:\n%s", tc.note, state)
			}
			if got := strings.Count(page, " selected"); got != 1 {
				t.Fatalf("%d options marked selected, want exactly one:\n%s", got, page)
			}
			if !strings.Contains(page, `value="`+tc.selected+`" selected`) {
				t.Errorf("selector does not mark %q as the current choice:\n%s",
					tc.selected, page)
			}
		})
	}
}
