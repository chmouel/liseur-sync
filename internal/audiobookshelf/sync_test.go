package audiobookshelf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

type absFixture struct {
	*fixture
	service  *Service
	mu       sync.Mutex
	progress *Progress
	sessions []Session
	patches  int
	failPage int
	bookID   string
	audio    []Chapter
}

func newABSFixture(t *testing.T, bookTitles, audioTitles []string) *absFixture {
	t.Helper()
	f := &absFixture{fixture: newFixture(t), failPage: -1, audio: evenAudio(audioTitles...)}
	f.bookID = f.book(t, "f-main", "book", "The Book", "An Author")
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		write := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.Path {
		case "/status":
			write(map[string]any{"serverVersion": "test"})
		case "/api/me":
			var ps []Progress
			if f.progress != nil {
				ps = append(ps, *f.progress)
			}
			write(Me{ID: "remote-user", MediaProgress: ps})
		case "/api/libraries":
			fmt.Fprint(w, `{"libraries":[{"id":"l","mediaType":"book"}]}`)
		case "/api/libraries/l/items":
			fmt.Fprint(w, `{"total":1,"results":[{"id":"item","mediaType":"book","media":{"duration":300,"metadata":{"title":"The Book","authorName":"An Author"}}}]}`)
		case "/api/items/item":
			write(map[string]any{"id": "item", "mediaType": "book", "media": map[string]any{
				"duration": float64(len(f.audio) * 100), "chapters": f.audio,
				"metadata": map[string]any{"title": "The Book", "authorName": "An Author"}}})
		case "/api/me/progress/item":
			if r.Method == http.MethodPatch {
				var update ProgressUpdate
				if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
					t.Error(err)
				}
				f.patches++
				f.progress = &Progress{LibraryItemID: "item", Duration: update.Duration, CurrentTime: update.CurrentTime,
					IsFinished: update.IsFinished, LastUpdate: time.Now().UnixMilli()}
			} else if f.progress == nil {
				http.NotFound(w, r)
			} else {
				write(f.progress)
			}
		case "/api/me/item/listening-sessions/item":
			if r.URL.Query().Get("page") == fmt.Sprint(f.failPage) {
				http.Error(w, "retry", http.StatusServiceUnavailable)
				return
			}
			page := 0
			_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)
			start := min(page*100, len(f.sessions))
			end := min(start+100, len(f.sessions))
			write(SessionPage{Total: len(f.sessions), NumPages: (len(f.sessions) + 99) / 100, Page: page, Sessions: f.sessions[start:end]})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	cfg := config.Default().Audiobookshelf
	cfg.Enabled, cfg.Account, cfg.BaseURL = true, f.user.Name, client.BaseURL()
	f.service = NewService(f.st, cfg)
	f.service.Client = client
	f.service.loadBook = func(context.Context, store.CatalogBook) ([]BookChapter, []Resource, error) {
		chapters, resources := evenBook(bookTitles...)
		return chapters, resources, nil
	}
	return f
}

func (f *absFixture) link(t *testing.T) store.AudiobookLink {
	t.Helper()
	if err := f.service.Link(t.Context(), f.user.ID, "item", f.bookID); err != nil {
		t.Fatal(err)
	}
	return f.linked(t)
}
func (f *absFixture) linked(t *testing.T) store.AudiobookLink {
	t.Helper()
	links, err := f.st.AudiobookLinks(t.Context(), f.user.ID, PeerIdentity(f.service.Cfg.BaseURL, "remote-user"))
	if err != nil || len(links) != 1 {
		t.Fatalf("links: %+v %v", links, err)
	}
	return links[0]
}
func (f *absFixture) read(t *testing.T, l store.AudiobookLink, p float64, at time.Time) {
	t.Helper()
	_, err := f.st.AppendOps(t.Context(), f.user.ID, "phone", []store.Op{{OpID: fmt.Sprintf("local:%v:%v", p, at),
		WorkID: l.WorkID, EditionSHA: &l.EditionSHA, Progression: p, ClientTS: at, Origin: store.OriginNative}})
	if err != nil {
		t.Fatal(err)
	}
}
func (f *absFixture) pass(t *testing.T) Report {
	t.Helper()
	rep, err := f.service.Pass(t.Context())
	if err != nil || rep.FirstError != nil {
		t.Fatalf("pass: %+v %v", rep, err)
	}
	return rep
}
func (f *absFixture) remote(at time.Time, p float64, finished bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	duration := float64(len(f.audio) * 100)
	f.progress = &Progress{LibraryItemID: "item", Duration: duration, CurrentTime: p * duration, IsFinished: finished, LastUpdate: at.UnixMilli()}
}

func TestPositionExchange(t *testing.T) {
	for _, tc := range []struct {
		name            string
		local, remote   float64
		newer, finished bool
		pushed, pulled  int
		want            float64
	}{
		{"pull newer", .2, .6, true, false, 0, 1, .6},
		{"push newer", .6, .2, false, false, 1, 0, .6},
		{"zero guard", .6, 0, true, false, 0, 0, .6},
		{"finished zero guard", .6, 0, true, true, 0, 0, .6},
		{"agreement", .6, .6, true, false, 0, 0, .6},
		{"finish", .6, .6, true, true, 0, 1, 1},
		{"local finish", .999, .8, false, false, 1, 0, .999},
		{"unfinish", 1, .5, true, false, 0, 1, .5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			titles := []string{"Chapter 1", "Chapter 2", "Chapter 3"}
			f := newABSFixture(t, titles, titles)
			l := f.link(t)
			now := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
			f.read(t, l, tc.local, now)
			remoteAt := now.Add(-time.Second)
			if tc.newer {
				remoteAt = now.Add(time.Second)
			}
			f.remote(remoteAt, tc.remote, tc.finished)
			rep := f.pass(t)
			if rep.Pushed != tc.pushed || rep.Pulled != tc.pulled {
				t.Fatalf("report: %+v", rep)
			}
			head, err := f.service.head(t.Context(), f.user.ID, l.WorkID)
			if err != nil || !nearProgress(head.Progression, tc.want) {
				t.Fatalf("head: %+v %v", head, err)
			}
			before := f.patches
			f.pass(t)
			if f.patches != before {
				t.Fatal("repeated pass wrote an echo")
			}
		})
	}
	t.Run("edition mismatch is not pushed", func(t *testing.T) {
		f, _, at := editionMismatch(t)
		f.remote(at.Add(-time.Second), .2, false)
		rep := f.pass(t)
		if rep.Pushed != 0 || rep.Pulled != 0 || rep.Skipped != 1 || f.patches != 0 {
			t.Fatalf("edition mismatch: %+v patches=%d", rep, f.patches)
		}
	})
	t.Run("edition mismatch still pulls", func(t *testing.T) {
		f, l, at := editionMismatch(t)
		f.remote(at.Add(time.Minute), .2, false)
		rep := f.pass(t)
		if rep.Pushed != 0 || rep.Pulled != 1 || f.patches != 0 {
			t.Fatalf("edition mismatch pull: %+v patches=%d", rep, f.patches)
		}
		head, err := f.service.head(t.Context(), f.user.ID, l.WorkID)
		if err != nil || !nearProgress(head.Progression, .2) {
			t.Fatalf("head: %+v %v", head, err)
		}
	})
}

func TestPartialRecordingNeverCompletesEbook(t *testing.T) {
	for _, backMatter := range []bool{false, true} {
		t.Run(fmt.Sprint(backMatter), func(t *testing.T) {
			book := []string{"Chapter 1", "Chapter 2", "Chapter 3"}
			if backMatter {
				book = append(book, "Credits")
			}
			f := newABSFixture(t, book, []string{"Chapter 2", "Chapter 3"})
			l := f.link(t)
			f.read(t, l, .1, time.Now().Add(-time.Hour))
			f.remote(time.Now().Add(-time.Minute), 1, true)
			if rep := f.pass(t); rep.Pulled != 1 {
				t.Fatalf("pull: %+v", rep)
			}
			head, _ := f.service.head(t.Context(), f.user.ID, l.WorkID)
			if head.Progression > .998 {
				t.Fatalf("partial completion: %v", head.Progression)
			}
			f.pass(t)
			if f.patches != 0 {
				t.Fatal("capped position pushed back")
			}
		})
	}
}

type lostABSCursor struct {
	store.Store
	fail bool
}

func (s *lostABSCursor) UpdateAudiobookCursor(ctx context.Context, u string, l store.AudiobookLink) error {
	if s.fail {
		s.fail = false
		return errors.New("lost cursor write")
	}
	return s.Store.UpdateAudiobookCursor(ctx, u, l)
}
func TestAgreementSurvivesLostCursorWrite(t *testing.T) {
	titles := []string{"Chapter 1", "Chapter 2"}
	f := newABSFixture(t, titles, titles)
	l := f.link(t)
	f.read(t, l, .6, time.Now().Add(-time.Minute))
	f.remote(time.Now().Add(-time.Hour), .2, false)
	f.service.Store = &lostABSCursor{Store: f.st, fail: true}
	rep, err := f.service.Pass(t.Context())
	if err != nil || rep.Failed != 1 || f.patches != 1 {
		t.Fatalf("first pass: %+v %v", rep, err)
	}
	f.pass(t)
	if f.patches != 1 {
		t.Fatal("agreement did not suppress repeated PATCH")
	}
}

func TestSessionImportKeepsMappingAndAttribution(t *testing.T) {
	titles := []string{"Chapter 1", "Chapter 2", "Chapter 3"}
	f := newABSFixture(t, titles, titles)
	l := f.link(t)
	now := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	remote := Session{ID: "session", LibraryItemID: "item", TimeListening: 60, StartTime: 30, CurrentTime: 90,
		StartedAt: now.Add(-time.Minute).UnixMilli(), UpdatedAt: now.UnixMilli()}
	f.sessions = []Session{remote}
	rep := f.pass(t)
	if rep.Imported != 1 {
		t.Fatalf("import: %+v", rep)
	}
	key, id := sessionRevision(l.PeerIdentity, remote)
	ledger, err := f.st.AudiobookSessionImport(t.Context(), f.user.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteAudiobookLink(t.Context(), f.user.ID, l.PeerIdentity, l.ItemID, l.Generation, false); err != nil {
		t.Fatal(err)
	}
	otherBook := f.book(t, "f-main", "other", "Another Book", "Another Author")
	if err := f.service.Link(t.Context(), f.user.ID, "item", otherBook); err != nil {
		t.Fatal(err)
	}
	if rep = f.pass(t); rep.Imported != 0 {
		t.Fatal("relink replay imported again")
	}
	remote.UpdatedAt = now.Add(time.Minute).UnixMilli()
	remote.CurrentTime = 120
	remote.TimeListening = 120
	f.sessions = []Session{remote}
	if rep = f.pass(t); rep.Imported != 1 {
		t.Fatalf("revision: %+v", rep)
	}
	current, err := f.st.CurrentSourceSession(t.Context(), f.user.ID, key)
	if err != nil || current.WorkID != l.WorkID || current.ReportedPages != nil || current.SessionID == id {
		t.Fatalf("revision: %+v %v", current, err)
	}
	still, err := f.st.AudiobookSessionImport(t.Context(), f.user.ID, key)
	if err != nil || still.MappingKey != ledger.MappingKey {
		t.Fatal("relink lost original mapping")
	}
}

func TestPausedLinkIsNotReplacedByAutomaticMatch(t *testing.T) {
	f := newABSFixture(t, []string{"Chapter 1"}, []string{"Unnumbered Track"})
	l := f.link(t)
	if l.State != store.AudiobookLinkNeedsMapping {
		t.Fatalf("state: %+v", l)
	}
	f.pass(t)
	after := f.linked(t)
	if after.Generation != l.Generation || after.Source != "manual" {
		t.Fatal("automatic match rewrote paused link")
	}
	other := f.addUser(t, "other")
	if _, err := f.service.View(t.Context(), other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other reader: %v", err)
	}
	if err := f.service.Mutate(t.Context(), f.user.ID, l.ItemID, l.Generation, "linear"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.linked(t).Mapping), `"linear":true`) {
		t.Fatal("linear confirmation missing")
	}
}

func TestSessionTraversalRetriesAfterAnInterruptedPage(t *testing.T) {
	titles := []string{"Chapter 1", "Chapter 2"}
	f := newABSFixture(t, titles, titles)
	l := f.link(t)
	now := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	for i := 0; i < 101; i++ {
		at := now.Add(-time.Duration(i) * 24 * time.Hour)
		f.sessions = append(f.sessions, Session{ID: fmt.Sprint("session-", i), LibraryItemID: "item", TimeListening: 30,
			StartedAt: at.Add(-time.Minute).UnixMilli(), UpdatedAt: at.UnixMilli(), StartTime: 10, CurrentTime: 20})
	}
	f.failPage = 1
	var m Mapping
	if err := json.Unmarshal(l.Mapping, &m); err != nil {
		t.Fatal(err)
	}
	var rep Report
	if err := f.service.importSessions(t.Context(), f.user.ID, &l, m, now.Add(time.Hour), &rep); err == nil {
		t.Fatal("interrupted traversal succeeded")
	}
	if l.SessionsWatermarkMs != 0 || rep.Imported != 100 {
		t.Fatalf("interrupted traversal advanced: %+v %+v", l, rep)
	}
	f.failPage = -1
	rep = Report{}
	if err := f.service.importSessions(t.Context(), f.user.ID, &l, m, now.Add(time.Hour), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Imported != 1 || l.SessionsWatermarkMs != now.UnixMilli() {
		t.Fatalf("retry: %+v %+v", l, rep)
	}
}

func TestFutureSessionIsRetriedAcrossRestart(t *testing.T) {
	titles := []string{"Chapter 1", "Chapter 2"}
	f := newABSFixture(t, titles, titles)
	l := f.link(t)
	now := time.Now().Truncate(time.Millisecond)
	f.sessions = []Session{
		{ID: "future", LibraryItemID: "item", TimeListening: 30, StartedAt: now.UnixMilli(), UpdatedAt: now.Add(2 * time.Hour).UnixMilli(), StartTime: 10, CurrentTime: 50},
		{ID: "past", LibraryItemID: "item", TimeListening: 30, StartedAt: now.Add(-2*time.Hour - time.Minute).UnixMilli(), UpdatedAt: now.Add(-2 * time.Hour).UnixMilli(), StartTime: 10, CurrentTime: 20},
	}
	var m Mapping
	if err := json.Unmarshal(l.Mapping, &m); err != nil {
		t.Fatal(err)
	}
	var rep Report
	if err := f.service.importSessions(t.Context(), f.user.ID, &l, m, now, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Imported != 1 || l.SessionsWatermarkMs != f.sessions[1].UpdatedAt {
		t.Fatalf("future advanced watermark: %+v %+v", l, rep)
	}
	if err := f.st.UpdateAudiobookCursor(t.Context(), f.user.ID, l); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(f.st, f.service.Cfg)
	restarted.Client = f.service.Client
	restarted.loadBook = f.service.loadBook
	l = f.linked(t)
	rep = Report{}
	if err := restarted.importSessions(t.Context(), f.user.ID, &l, m, now.Add(3*time.Hour), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Imported != 1 || l.SessionsWatermarkMs != f.sessions[0].UpdatedAt {
		t.Fatalf("future session lost on restart: %+v %+v", l, rep)
	}
}

func TestRevokedGrantPausesLinkWithoutWrites(t *testing.T) {
	titles := []string{"Chapter 1", "Chapter 2"}
	f := newABSFixture(t, titles, titles)
	l := f.link(t)
	f.read(t, l, .5, time.Now().Add(-time.Minute))
	f.remote(time.Now().Add(-time.Hour), .1, false)
	if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	rep := f.pass(t)
	if rep.Pushed != 0 || rep.Pulled != 0 || f.linked(t).State != store.AudiobookLinkSuspended {
		t.Fatalf("revoked grant: %+v", rep)
	}
}

// editionMismatch returns a linked fixture whose newest local op comes from
// another edition of the same work.
func editionMismatch(t *testing.T) (*absFixture, store.AudiobookLink, time.Time) {
	t.Helper()
	titles := []string{"Chapter 1", "Chapter 2", "Chapter 3"}
	f := newABSFixture(t, titles, titles)
	l := f.link(t)
	now := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	otherWork := "work-other"
	otherSHA := strings.Repeat("b", 64)
	if err := f.st.CreateWork(t.Context(),
		store.Work{ID: otherWork, UserID: f.user.ID, Title: "The Book", Author: "An Author", CreatedAt: now},
		&store.Edition{UserID: f.user.ID, SHA256: otherSHA, WorkID: otherWork},
		[]store.Identifier{{Kind: "sha256", Value: otherSHA}},
	); err != nil {
		t.Fatal(err)
	}
	if err := f.st.MergeWorks(t.Context(), f.user.ID, otherWork, l.WorkID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.AppendOps(t.Context(), f.user.ID, "phone", []store.Op{{
		OpID:        "local:edition-mismatch",
		WorkID:      l.WorkID,
		EditionSHA:  &otherSHA,
		Progression: .6,
		ClientTS:    now,
		Origin:      store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	return f, l, now
}

func TestRemoteSeekBackIsNotAnEcho(t *testing.T) {
	titles := []string{"Chapter 1", "Chapter 2"}
	f := newABSFixture(t, titles, titles)
	l := f.link(t)
	now := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	f.read(t, l, .5, now)
	f.remote(now.Add(-time.Second), .2, false)
	if rep := f.pass(t); rep.Pushed != 1 {
		t.Fatalf("push: %+v", rep)
	}
	// The reader moves on locally, then seeks back on the phone. The mark
	// matches the pushed value, but the remote timestamp is newer.
	f.read(t, l, .7, now.Add(time.Second))
	f.remote(time.Now().Add(2*time.Second), .5, false)
	rep := f.pass(t)
	if rep.Pulled != 1 || f.patches != 1 {
		t.Fatalf("seek back: %+v patches=%d", rep, f.patches)
	}
	head, err := f.service.head(t.Context(), f.user.ID, l.WorkID)
	if err != nil || !nearProgress(head.Progression, .5) {
		t.Fatalf("head: %+v %v", head, err)
	}
}

func TestSuspendedPreviewNeedsConfirmationAgain(t *testing.T) {
	f := newABSFixture(t,
		[]string{"Chapter 1", "Chapter 2", "Chapter 3"},
		[]string{"Chapter 1", "Chapter 2"})
	l := f.link(t)
	if l.State != store.AudiobookLinkActive {
		t.Fatalf("link: %+v", l)
	}
	// The recording now covers the whole book, so the alignment moves.
	f.audio = evenAudio("Chapter 1", "Chapter 2", "Chapter 3")
	f.pass(t)
	l = f.linked(t)
	if l.State != store.AudiobookLinkNeedsMapping || len(l.Mapping) != 0 {
		t.Fatalf("remap: %+v", l)
	}
	// The pending preview is still shown to the reader.
	views, err := f.service.View(t.Context(), f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	previewed := false
	for _, v := range views {
		previewed = previewed || len(v.Mapping.Segments) > 0
	}
	if !previewed {
		t.Fatal("View lost the unconfirmed preview")
	}
	if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	if rep := f.pass(t); f.linked(t).State != store.AudiobookLinkSuspended {
		t.Fatalf("suspended: %+v", rep)
	}
	if err := f.st.AssignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	if got := f.linked(t); got.State != store.AudiobookLinkNeedsMapping {
		t.Fatalf("suspension confirmed an unaccepted preview: %+v", got)
	}
	if err := f.service.Mutate(t.Context(), f.user.ID, "item", f.linked(t).Generation, "confirm"); err != nil {
		t.Fatal(err)
	}
	if got := f.linked(t); got.State != store.AudiobookLinkActive {
		t.Fatalf("confirm: %+v", got)
	}
}

func TestConfirmKeepsAConfirmedLinearMapping(t *testing.T) {
	f := newABSFixture(t, []string{"Intro", "Body"}, []string{"Intro", "Body"})
	l := f.link(t)
	if l.State != store.AudiobookLinkNeedsMapping {
		t.Fatalf("link: %+v", l)
	}
	if err := f.service.Mutate(t.Context(), f.user.ID, "item", l.Generation, "linear"); err != nil {
		t.Fatal(err)
	}
	l = f.linked(t)
	if !l.ConfirmedLinear || l.State != store.AudiobookLinkActive {
		t.Fatalf("linear: %+v", l)
	}
	f.audio = evenAudio("Intro", "Body", "Credits")
	f.pass(t)
	if l = f.linked(t); l.State != store.AudiobookLinkNeedsMapping || !l.ConfirmedLinear {
		t.Fatalf("remap: %+v", l)
	}
	if err := f.service.Mutate(t.Context(), f.user.ID, "item", l.Generation, "confirm"); err != nil {
		t.Fatalf("confirm linear: %v", err)
	}
	if got := f.linked(t); got.State != store.AudiobookLinkActive || !got.ConfirmedLinear {
		t.Fatalf("confirm: %+v", got)
	}
}
