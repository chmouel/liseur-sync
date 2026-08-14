//go:build linux

package content

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// fakeLibraryReader answers library reads the way the catalog does: the ACL
// decides visibility, so an unreadable library is indistinguishable from a
// missing one.
type fakeLibraryReader struct {
	config map[string][]byte
	err    error
	reads  []string
}

func (f *fakeLibraryReader) LibraryByID(
	_ context.Context, userID, libraryID string, required store.LibraryRole,
) (store.AccessibleLibrary, error) {
	f.reads = append(f.reads, userID+"/"+libraryID+"/"+string(required))
	if f.err != nil {
		return store.AccessibleLibrary{}, f.err
	}
	config, ok := f.config[libraryID]
	if !ok {
		return store.AccessibleLibrary{}, store.ErrNotFound
	}
	return store.AccessibleLibrary{
		Library: store.Library{ID: libraryID, ConfigJSON: config},
		Role:    store.LibraryRoleManage,
	}, nil
}

func TestLibraryPatternsReadsTheConfiguredLayouts(t *testing.T) {
	reader := &fakeLibraryReader{config: map[string][]byte{
		"lib-1": []byte(`{"path_patterns":["series/author-title"]}`),
		"lib-2": nil,
	}}
	resolver := NewLibraryPatterns(reader)

	got, err := resolver.PatternsFor(context.Background(), "user-1", "lib-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []metadata.PathPattern{metadata.PatternSeriesAuthorTitle}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// An unconfigured library gets the defaults rather than nothing.
	got, err = resolver.PatternsFor(context.Background(), "user-1", "lib-2")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !reflect.DeepEqual(got, metadata.DefaultPathPatterns()) {
		t.Fatalf("got %v, want defaults", got)
	}
}

// The lookup must stay inside the ACL that let the job exist. A resolver
// that read libraries unscoped would be the one place a worker could see
// across tenants.
func TestLibraryPatternsReadsUnderTheJobsOwnAccess(t *testing.T) {
	reader := &fakeLibraryReader{config: map[string][]byte{"lib-1": nil}}
	if _, err := NewLibraryPatterns(reader).PatternsFor(
		context.Background(), "user-1", "lib-1"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []string{"user-1/lib-1/read"}
	if !reflect.DeepEqual(reader.reads, want) {
		t.Fatalf("reads = %v, want %v", reader.reads, want)
	}
}

func TestLibraryPatternsReportsAnUnreadableConfiguration(t *testing.T) {
	reader := &fakeLibraryReader{config: map[string][]byte{
		"lib-1": []byte(`{"path_patterns":["author/isbn"]}`),
	}}
	got, err := NewLibraryPatterns(reader).PatternsFor(
		context.Background(), "user-1", "lib-1")
	if !errors.Is(err, metadata.ErrInvalidLibraryConfig) {
		t.Fatalf("err = %v, want ErrInvalidLibraryConfig", err)
	}
	if got != nil {
		t.Fatalf("returned %v alongside an error", got)
	}
}

func TestLibraryPatternsPropagatesAReadFailure(t *testing.T) {
	sentinel := errors.New("database is gone")
	if _, err := NewLibraryPatterns(&fakeLibraryReader{err: sentinel}).PatternsFor(
		context.Background(), "user-1", "lib-1"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestLibraryPatternsRejectsAnUnusableResolver(t *testing.T) {
	var nilResolver *LibraryPatterns
	if _, err := nilResolver.PatternsFor(
		context.Background(), "user-1", "lib-1"); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("err = %v, want invalid transition", err)
	}
	if _, err := NewLibraryPatterns(nil).PatternsFor(
		context.Background(), "user-1", "lib-1"); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("err = %v, want invalid transition", err)
	}
}

func TestFixedPatternsAnswersEveryLibraryTheSame(t *testing.T) {
	fixed := FixedPatterns(metadata.DefaultPathPatterns())
	for _, library := range []string{"lib-1", "lib-2"} {
		got, err := fixed.PatternsFor(context.Background(), "user-1", library)
		if err != nil {
			t.Fatalf("%s: %v", library, err)
		}
		if !reflect.DeepEqual(got, metadata.DefaultPathPatterns()) {
			t.Fatalf("%s: got %v, want defaults", library, got)
		}
	}
}

// A batch is usually one library's backlog, so resolving per job would read
// the same row once per file.
func TestMemoizeReadsOneLibraryOnce(t *testing.T) {
	reader := &fakeLibraryReader{config: map[string][]byte{
		"lib-1": []byte(`{"path_patterns":["author/title"]}`),
		"lib-2": nil,
	}}
	memo := memoize(NewLibraryPatterns(reader))
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := memo.PatternsFor(ctx, "user-1", "lib-1"); err != nil {
			t.Fatal(err)
		}
	}
	if len(reader.reads) != 1 {
		t.Fatalf("reads = %v, want one", reader.reads)
	}
	if _, err := memo.PatternsFor(ctx, "user-1", "lib-2"); err != nil {
		t.Fatal(err)
	}
	if len(reader.reads) != 2 {
		t.Fatalf("reads = %v, want a second library read", reader.reads)
	}
}

// The answer is ACL-scoped, so it belongs to the user who asked. Caching it
// by library alone would let one user's access decide another's.
func TestMemoizeKeysOnTheUserAsWellAsTheLibrary(t *testing.T) {
	reader := &fakeLibraryReader{config: map[string][]byte{"lib-1": nil}}
	memo := memoize(NewLibraryPatterns(reader))
	ctx := context.Background()
	if _, err := memo.PatternsFor(ctx, "user-1", "lib-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := memo.PatternsFor(ctx, "user-2", "lib-1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"user-1/lib-1/read", "user-2/lib-1/read"}
	if !reflect.DeepEqual(reader.reads, want) {
		t.Fatalf("reads = %v, want %v", reader.reads, want)
	}
}

// A failure must not be remembered as an answer: the next job would inherit
// a resolution that never happened.
func TestMemoizeDoesNotCacheAFailure(t *testing.T) {
	reader := &fakeLibraryReader{err: errors.New("database is gone")}
	memo := memoize(NewLibraryPatterns(reader))
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := memo.PatternsFor(ctx, "user-1", "lib-1"); err == nil {
			t.Fatal("resolve succeeded, want the read failure")
		}
	}
	if len(reader.reads) != 2 {
		t.Fatalf("reads = %v, want both attempts", reader.reads)
	}
}

// multiLibraryQueue is one worker batch spanning several libraries. Each job
// keeps its own fakePromotionStore, which is what makes it possible to ask
// whether one library's problem reached another library's job.
type multiLibraryQueue struct {
	stores []*fakePromotionStore
	listed []store.IngestState
}

func (q *multiLibraryQueue) ListIngestWorkerJobs(
	_ context.Context, state store.IngestState, limit int,
) ([]store.IngestJob, error) {
	q.listed = append(q.listed, state)
	var jobs []store.IngestJob
	for _, s := range q.stores {
		if s.job.State == state && len(jobs) < limit {
			jobs = append(jobs, s.job)
		}
	}
	return jobs, nil
}

func (q *multiLibraryQueue) forJob(jobID string) *fakePromotionStore {
	for _, s := range q.stores {
		if s.job.ID == jobID {
			return s
		}
	}
	return nil
}

func (q *multiLibraryQueue) TransitionIngestJob(
	ctx context.Context, userID, jobID string, change store.IngestJobTransition,
) (store.IngestJob, error) {
	s := q.forJob(jobID)
	if s == nil {
		return store.IngestJob{}, store.ErrNotFound
	}
	return s.TransitionIngestJob(ctx, userID, jobID, change)
}

func (q *multiLibraryQueue) CommitNewBookPromotion(
	ctx context.Context, userID, jobID string,
	request store.CommitNewBookPromotionRequest,
) (store.IngestPromotionResult, error) {
	s := q.forJob(jobID)
	if s == nil {
		return store.IngestPromotionResult{}, store.ErrNotFound
	}
	return s.CommitNewBookPromotion(ctx, userID, jobID, request)
}

func (q *multiLibraryQueue) CatalogBookMetadata(
	ctx context.Context, userID, bookID string, role store.LibraryRole,
) (store.BookMetadata, error) {
	for _, s := range q.stores {
		if got, err := s.CatalogBookMetadata(ctx, userID, bookID, role); err == nil {
			return got, nil
		}
	}
	return store.BookMetadata{}, store.ErrNotFound
}

func (q *multiLibraryQueue) ApplyCatalogBookMetadata(
	ctx context.Context, userID string, request store.ApplyBookMetadataRequest,
) (store.BookMetadata, error) {
	for _, s := range q.stores {
		if s.book.Book.ID == request.Metadata.Book.ID {
			return s.ApplyCatalogBookMetadata(ctx, userID, request)
		}
	}
	return store.BookMetadata{}, store.ErrNotFound
}

// watchedJob is a file discovered under a library root, which is the only
// kind of job whose path a layout has anything to say about.
func watchedJob(id, userID, libraryID, relative string) store.IngestJob {
	job := extractedJob()
	job.ID = id
	job.UserID = userID
	job.LibraryID = libraryID
	job.QuotaUserID = userID
	job.Source = store.IngestScanned
	job.SourceRelativePath = strptr(relative)
	job.StagingPath = strptr(contentpath.StagingPath(id))
	return job
}

// The point of the whole feature: one filename means different things in
// two libraries, and each library decides which.
func TestRunIngestPromotionPassAppliesEachLibrarysLayout(t *testing.T) {
	const relative = "Earthsea/Le Guin - Tehanu.epub"
	byAuthor := &fakePromotionStore{
		job: watchedJob("job-author", "user-1", "lib-author", relative)}
	bySeries := &fakePromotionStore{
		job: watchedJob("job-series", "user-1", "lib-series", relative)}
	queue := &multiLibraryQueue{stores: []*fakePromotionStore{byAuthor, bySeries}}
	reader := &fakeLibraryReader{config: map[string][]byte{
		"lib-author": nil,
		"lib-series": []byte(`{"path_patterns":["series/author-title"]}`),
	}}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(context.Background(), queue,
		&fakeBlobPromoter{}, NewLibraryPatterns(reader), fixedClock(now),
		time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Promoted != 2 || report.Misconfigured != 0 {
		t.Fatalf("report = %+v", report)
	}
	// The default layout reads the directory as an author.
	if len(byAuthor.book.Contributors) != 1 ||
		byAuthor.book.Contributors[0].Name != "Earthsea" {
		t.Fatalf("default layout contributors = %+v", byAuthor.book.Contributors)
	}
	if len(byAuthor.book.Series) != 0 {
		t.Fatalf("default layout invented a series: %+v", byAuthor.book.Series)
	}
	// The configured layout reads the same directory as a series. That
	// layout is off by default, so this is only reachable because the
	// library asked for it.
	if len(bySeries.book.Series) != 1 || bySeries.book.Series[0].Name != "Earthsea" {
		t.Fatalf("configured layout series = %+v", bySeries.book.Series)
	}
	if len(bySeries.book.Contributors) != 0 {
		t.Fatalf("configured layout kept a guessed author: %+v",
			bySeries.book.Contributors)
	}
}

// A library nobody can describe holds up its own backlog and nothing else.
// Promoting it with whatever layout happened to be compiled in would file
// its books under the wrong author, and failing the pass would let one
// library's typo stop every other library's uploads.
func TestRunIngestPromotionPassStallsOnlyTheMisconfiguredLibrary(t *testing.T) {
	good := &fakePromotionStore{
		job: watchedJob("job-good", "user-1", "lib-good", "Le Guin/Earthsea.epub")}
	broken := &fakePromotionStore{
		job: watchedJob("job-broken", "user-1", "lib-broken", "Le Guin/Tehanu.epub")}
	queue := &multiLibraryQueue{stores: []*fakePromotionStore{broken, good}}
	reader := &fakeLibraryReader{config: map[string][]byte{
		"lib-good":   nil,
		"lib-broken": []byte(`{"path_patterns":["author/isbn"]}`),
	}}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(context.Background(), queue,
		&fakeBlobPromoter{}, NewLibraryPatterns(reader), fixedClock(now),
		time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Misconfigured != 1 || report.Promoted != 1 {
		t.Fatalf("report = %+v", report)
	}
	if broken.job.State != store.IngestExtracted {
		t.Fatalf("stalled job moved to %q", broken.job.State)
	}
	if len(broken.committed) != 0 {
		t.Fatal("a book was created for the misconfigured library")
	}
	if good.job.State != store.IngestPromoted {
		t.Fatalf("healthy job state = %q", good.job.State)
	}
}

// A library the job's user can no longer read is the same situation: the
// layout is unknowable, so the job waits rather than being guessed at.
func TestRunIngestPromotionPassStallsOnAnUnreadableLibrary(t *testing.T) {
	st := &fakePromotionStore{
		job: watchedJob("job-1", "user-1", "lib-gone", "Le Guin/Earthsea.epub")}
	queue := &multiLibraryQueue{stores: []*fakePromotionStore{st}}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(context.Background(), queue,
		&fakeBlobPromoter{}, NewLibraryPatterns(&fakeLibraryReader{}),
		fixedClock(now), time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Misconfigured != 1 || report.Promoted != 0 {
		t.Fatalf("report = %+v", report)
	}
	if st.job.State != store.IngestExtracted {
		t.Fatalf("stalled job moved to %q", st.job.State)
	}
}

// A database that is failing is not a misconfiguration. Counting it as one
// would report a broken server as a set of badly configured libraries and
// let the pass keep hammering it.
func TestRunIngestPromotionPassPropagatesAResolverFailure(t *testing.T) {
	sentinel := errors.New("database is gone")
	st := &fakePromotionStore{
		job: watchedJob("job-1", "user-1", "lib-1", "Le Guin/Earthsea.epub")}
	queue := &multiLibraryQueue{stores: []*fakePromotionStore{st}}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	_, err := RunIngestPromotionPass(context.Background(), queue,
		&fakeBlobPromoter{}, NewLibraryPatterns(&fakeLibraryReader{err: sentinel}),
		fixedClock(now), time.Hour, 25)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}
