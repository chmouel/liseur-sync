//go:build linux

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// uploadFixture is a server with a real store and a real CAS, because the
// upload path's whole purpose is to move bytes between the two.
type uploadFixture struct {
	ts *httptest.Server
	// srv is kept so a test can turn on something the default fixture
	// leaves off. Routes() closes over the pointer, so a field set after
	// the server is up is read on the next request.
	srv     *Server
	handler http.Handler
	st      store.Store
	cas     *content.CAS
	user    store.User
	other   store.User
	token   string
	library string
	root    string
}

func newUploadFixture(t *testing.T) *uploadFixture {
	t.Helper()
	root := t.TempDir()
	st, err := sqlite.Open(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cas, err := content.Open(filepath.Join(root, "content"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cas.Close() })

	cfg := config.Default()
	cfg.InsecureHTTP = true
	srv := &Server{
		St:           st,
		Auth:         auth.NewService(st),
		Cfg:          cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		Content:      cas,
		Blobs:        cas,
		Covers:       cas,
	}
	handler := srv.Routes()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	f := &uploadFixture{
		root: root,
		ts:   ts, srv: srv, handler: handler, st: st, cas: cas,
		user:    storetest.MkUser(t, st, "uploader"),
		other:   storetest.MkUser(t, st, "stranger"),
		library: "lib-1",
	}
	if err := st.CreateLibrary(t.Context(), store.Library{
		ID: f.library, OwnerUserID: f.user.ID, QuotaUserID: f.user.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Books", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	f.token = f.mintToken(t, f.user.ID, store.ScopeLibraryManage)
	return f
}

func (f *uploadFixture) mintToken(t *testing.T, userID string, scope store.Scope) string {
	t.Helper()
	return f.mintScopes(t, userID, string(scope), scope)
}

// mintScopes issues a token carrying more than one capability, which is
// what a real client holds: the same device browses the catalog and syncs.
func (f *uploadFixture) mintScopes(
	t *testing.T, userID, name string, scopes ...store.Scope,
) string {
	t.Helper()
	secret, _, err := auth.NewService(f.st).MintToken(t.Context(), userID,
		"device-"+name+"-"+userID, store.ScopeSet(scopes), nil)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

// upload posts one multipart body and returns the decoded job resource.
func (f *uploadFixture) upload(
	t *testing.T, token, library, key string, body []byte,
) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "book.epub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost,
		f.ts.URL+"/v1/libraries/"+library+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestUploadCreatesAStagedJob is the claim the whole endpoint exists for:
// bytes arrive over HTTP and become a durable job the workers can pick up.
func TestUploadCreatesAStagedJob(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("not really an epub, but bytes are bytes")

	code, out := f.upload(t, f.token, f.library, "key-1", body)
	if code != http.StatusAccepted {
		t.Fatalf("upload: %d %v", code, out)
	}
	if out["state"] != string(store.IngestStaged) {
		t.Fatalf("state = %v, want staged: %v", out["state"], out)
	}
	if out["library_id"] != f.library {
		t.Fatalf("library_id = %v", out["library_id"])
	}

	jobID, _ := out["job_id"].(string)
	job, err := f.st.IngestJobByID(t.Context(), f.user.ID, jobID)
	if err != nil {
		t.Fatalf("job lookup: %v", err)
	}
	if job.State != store.IngestStaged {
		t.Fatalf("persisted state = %s", job.State)
	}
	if job.OriginalFilename != "book.epub" {
		t.Fatalf("filename = %q, want book.epub", job.OriginalFilename)
	}
	if job.ContentSHA256 == nil {
		t.Fatal("job has no content digest")
	}
	if job.BytesReceived != int64(len(body)) {
		t.Fatalf("bytes = %d, want %d", job.BytesReceived, len(body))
	}
	// The digest must describe what was sent, not merely be present.
	if got := *job.ContentSHA256; got != sha256Hex(body) {
		t.Fatalf("digest = %s, want %s", got, sha256Hex(body))
	}
	// And the staged bytes must be readable back at that identity.
	if _, err := f.cas.InspectArtifact(t.Context(), *job.StagingPath,
		*job.ContentSHA256, job.BytesReceived); err != nil {
		t.Fatalf("staged artifact does not verify: %v", err)
	}
}

// TestUploadReplaysAnIdempotencyKey covers the retry a client makes when it
// never saw our response. Uploading twice must leave one job and one book's
// worth of bytes, not two.
func TestUploadReplaysAnIdempotencyKey(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("the same book, sent twice")

	code, first := f.upload(t, f.token, f.library, "key-retry", body)
	if code != http.StatusAccepted {
		t.Fatalf("first upload: %d %v", code, first)
	}
	code, second := f.upload(t, f.token, f.library, "key-retry", body)
	if code != http.StatusOK {
		t.Fatalf("replay should be 200, got %d %v", code, second)
	}
	if first["job_id"] != second["job_id"] {
		t.Fatalf("replay created a second job: %v vs %v",
			first["job_id"], second["job_id"])
	}
	// The id is derived from the key, not assigned by the database, so a
	// client that lost our response can still address its own job. The
	// store's unique index on client_key would make the retry idempotent
	// either way; this pins the stronger property.
	if want := uploadJobID(f.user.ID, f.library, "key-retry"); first["job_id"] != want {
		t.Fatalf("job_id = %v, want the derived %s", first["job_id"], want)
	}

	jobs, err := f.st.ListIngestJobs(t.Context(), f.user.ID, f.library, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job after a retry, got %d", len(jobs))
	}
}

// TestUploadDistinctKeysCreateDistinctJobs is the other half of idempotency:
// a client that deliberately uploads the same file twice under two keys wants
// two catalog references, and must get them.
func TestUploadDistinctKeysCreateDistinctJobs(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("deduplicated bytes, two references")

	_, first := f.upload(t, f.token, f.library, "key-a", body)
	_, second := f.upload(t, f.token, f.library, "key-b", body)
	if first["job_id"] == second["job_id"] {
		t.Fatal("two keys collapsed into one job")
	}
	if first["sha256"] != second["sha256"] {
		t.Fatalf("identical bytes hashed differently: %v vs %v",
			first["sha256"], second["sha256"])
	}
}

// TestUploadKeysAreScopedPerUser covers two managers of one shared library
// who happen to pick the same idempotency key. Keys are client-chosen, so
// collisions between users are ordinary, not adversarial — and neither
// upload may be refused or absorbed into the other's job.
func TestUploadKeysAreScopedPerUser(t *testing.T) {
	f := newUploadFixture(t)
	if err := f.st.GrantLibraryAccess(t.Context(), f.user.ID, f.library,
		f.other.ID, store.LibraryRoleManage, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	otherToken := f.mintToken(t, f.other.ID, store.ScopeLibraryManage)

	code, mine := f.upload(t, f.token, f.library, "shared-key", []byte("my book"))
	if code != http.StatusAccepted {
		t.Fatalf("owner upload: %d %v", code, mine)
	}
	code, theirs := f.upload(t, otherToken, f.library, "shared-key", []byte("their book"))
	if code != http.StatusAccepted {
		t.Fatalf("second manager upload: %d %v", code, theirs)
	}
	if mine["job_id"] == theirs["job_id"] {
		t.Fatal("two users' uploads collapsed into one job")
	}
	if mine["sha256"] == theirs["sha256"] {
		t.Fatalf("distinct uploads share a digest: %v", mine["sha256"])
	}
	// Each job records the user who sent it. Visibility is a separate
	// question: IngestJobByID is scoped by manage access to the library,
	// so co-managers can see each other's jobs there by design — see
	// TestIngestJobRequiresLibraryAccess for the boundary that matters.
	mineJob, err := f.st.IngestJobByID(t.Context(), f.user.ID, mine["job_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	theirsJob, err := f.st.IngestJobByID(t.Context(), f.other.ID, theirs["job_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if mineJob.UserID != f.user.ID || theirsJob.UserID != f.other.ID {
		t.Fatalf("job ownership crossed over: %s and %s",
			mineJob.UserID, theirsJob.UserID)
	}
}

func TestUploadRejectsUnauthorizedAndMalformedRequests(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("payload")
	readToken := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	strangerToken := f.mintToken(t, f.other.ID, store.ScopeLibraryManage)

	t.Run("no token", func(t *testing.T) {
		if code, _ := f.upload(t, "", f.library, "k", body); code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", code)
		}
	})
	t.Run("read scope cannot write", func(t *testing.T) {
		if code, _ := f.upload(t, readToken, f.library, "k", body); code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403", code)
		}
	})
	t.Run("another user's library", func(t *testing.T) {
		code, _ := f.upload(t, strangerToken, f.library, "k", body)
		if code != http.StatusNotFound {
			t.Fatalf("code = %d, want 404", code)
		}
	})
	t.Run("unknown library", func(t *testing.T) {
		if code, _ := f.upload(t, f.token, "nope", "k", body); code != http.StatusNotFound {
			t.Fatalf("code = %d, want 404", code)
		}
	})
	t.Run("missing idempotency key", func(t *testing.T) {
		if code, _ := f.upload(t, f.token, f.library, "", body); code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", code)
		}
	})
	t.Run("not multipart", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost,
			f.ts.URL+"/v1/libraries/"+f.library+"/upload", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Idempotency-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("code = %d, want 415", resp.StatusCode)
		}
	})
	t.Run("multipart without a file part", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("notthefile", "x")
		mw.Close()
		req, _ := http.NewRequest(http.MethodPost,
			f.ts.URL+"/v1/libraries/"+f.library+"/upload", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Idempotency-Key", "no-file")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", resp.StatusCode)
		}
	})
}

// TestUploadRejectsAnOversizeFile pins the request bound that ADR-0005
// requires and that nothing enforced before this endpoint existed: the EPUB
// validator's limits only apply to bytes that are already on disk.
func TestUploadRejectsAnOversizeFile(t *testing.T) {
	f := newUploadFixture(t)
	f.setMaxUpload(t, 64)

	code, out := f.upload(t, f.token, f.library, "too-big", bytes.Repeat([]byte("x"), 4096))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413: %v", code, out)
	}
	// The rejected job must not be left claiming bytes it does not have.
	job, err := f.st.IngestJobByID(t.Context(), f.user.ID,
		uploadJobID(f.user.ID, f.library, "too-big"))
	if err != nil {
		t.Fatalf("job lookup: %v", err)
	}
	if job.State != store.IngestReceived {
		t.Fatalf("state = %s, want received", job.State)
	}
	if job.ContentSHA256 != nil {
		t.Fatalf("rejected upload recorded a digest: %v", *job.ContentSHA256)
	}
}

// TestUploadRetriesAfterAnOversizeRejection proves the bound is not a dead
// end: the job stays in `received`, so the same key can carry a smaller file.
func TestUploadRetriesAfterAnOversizeRejection(t *testing.T) {
	f := newUploadFixture(t)
	f.setMaxUpload(t, 64)
	if code, _ := f.upload(t, f.token, f.library, "retry", bytes.Repeat([]byte("x"), 4096)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload: %d", code)
	}

	f.setMaxUpload(t, 1<<20)
	code, out := f.upload(t, f.token, f.library, "retry", []byte("small enough"))
	if code != http.StatusAccepted {
		t.Fatalf("retry: %d %v", code, out)
	}
	if out["state"] != string(store.IngestStaged) {
		t.Fatalf("state = %v, want staged", out["state"])
	}
}

// TestUploadRejectsAQuotaOverrun keeps a principal from filling the disk
// through repeated uploads, each individually under the size bound.
func TestUploadRejectsAQuotaOverrun(t *testing.T) {
	f := newUploadFixture(t)
	f.setQuota(t, 32)

	code, out := f.upload(t, f.token, f.library, "over-quota",
		bytes.Repeat([]byte("y"), 512))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413: %v", code, out)
	}
	// The rejected bytes must not stay on disk. Nothing in the database
	// points at them once the commit fails, so if they survive here no
	// recovery or GC pass will ever find them, and a user already at
	// quota could fill the disk by looping with fresh keys.
	if n := stagedFiles(t, f.root); n != 0 {
		t.Fatalf("%d staged file(s) left behind by a rejected upload", n)
	}
}

// TestUploadAfterAQuotaRejectionStoresTheNewFile is the reason the leak
// above matters beyond disk: a surviving stage is keyed by job id, and
// CAS.Stage replays it without reading the body. The retry would be
// answered 202 while storing the *first* upload's bytes.
func TestUploadAfterAQuotaRejectionStoresTheNewFile(t *testing.T) {
	f := newUploadFixture(t)
	f.setQuota(t, 32)
	first := bytes.Repeat([]byte("y"), 512)
	if code, _ := f.upload(t, f.token, f.library, "recover", first); code != http.StatusRequestEntityTooLarge {
		t.Fatal("expected the first upload to be refused")
	}

	f.setQuota(t, 0)
	second := []byte("a different book")
	code, out := f.upload(t, f.token, f.library, "recover", second)
	if code != http.StatusAccepted {
		t.Fatalf("retry: %d %v", code, out)
	}
	sum := sha256.Sum256(second)
	if got := out["sha256"]; got != hex.EncodeToString(sum[:]) {
		t.Fatalf("stored the wrong content: sha256 = %v, want the retry's %s",
			got, hex.EncodeToString(sum[:]))
	}
}

// stagedFiles counts artifacts sitting in the CAS staging directory.
func stagedFiles(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "content", ".incoming"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		// Lock files are permanent fixtures, not artifacts.
		if strings.HasPrefix(e.Name(), ".lock-") {
			continue
		}
		n++
	}
	return n
}

// TestUploadNeverAnswers5xxForMalformedInput covers the repo rule that
// malformed input is always a precise 4xx. The long-library-id case is the
// one that broke it: the id goes into the request fingerprint, which the
// store rejects with an error the handler cannot classify.
func TestUploadNeverAnswers5xxForMalformedInput(t *testing.T) {
	f := newUploadFixture(t)
	for _, tc := range []struct {
		name    string
		library string
		want    int
	}{
		{"unknown library", "no-such-library", http.StatusNotFound},
		{"absurdly long library id", strings.Repeat("a", 600), http.StatusNotFound},
		{"library id just over the bound", strings.Repeat("b", 129), http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := f.upload(t, f.token, tc.library, "malformed", []byte("x"))
			if code != tc.want {
				t.Fatalf("code = %d, want %d: %v", code, tc.want, out)
			}
		})
	}

	// A body that is not multipart at all, and one whose boundary lies.
	req, _ := http.NewRequest(http.MethodPost,
		f.ts.URL+"/v1/libraries/"+f.library+"/upload",
		strings.NewReader("--nope\r\nnot really multipart"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=nope")
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Idempotency-Key", "truncated")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		t.Fatalf("truncated multipart body answered %d", resp.StatusCode)
	}
}

// TestUploadBoundsWhatItReadsFromTheNetwork pins that max_upload_bytes
// bounds the transport and not merely the stored file. multipart.Part.Close
// drains the remainder of a part, and skipping a non-file part reads all of
// it, so without a transport bound a client could make the server read an
// unlimited number of bytes it had already refused to store.
func TestUploadBoundsWhatItReadsFromTheNetwork(t *testing.T) {
	f := newUploadFixture(t)
	f.setMaxUpload(t, 1024)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// A junk part the handler skips, far larger than the file bound.
	junk, err := mw.CreateFormField("junk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := junk.Write(bytes.Repeat([]byte("j"), 4<<20)); err != nil {
		t.Fatal(err)
	}
	file, err := mw.CreateFormFile("file", "book.epub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("tiny")); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost,
		f.ts.URL+"/v1/libraries/"+f.library+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Idempotency-Key", "junk-parts")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("a 4 MiB envelope under a 1 KiB limit answered %d", resp.StatusCode)
	}
	if n := stagedFiles(t, f.root); n != 0 {
		t.Fatalf("%d staged file(s) left behind", n)
	}
}

// TestConcurrentUploadsOfOneKeyKeepTheirBytes is the race the commit-error
// cleanup has to survive. Two requests carrying the same idempotency key
// enter the same job and stage the same path; one commits, the other loses
// on revision. The loser must not delete the stage, because it is the very
// file the winner just recorded.
func TestConcurrentUploadsOfOneKeyKeepTheirBytes(t *testing.T) {
	f := newUploadFixture(t)
	body := bytes.Repeat([]byte("concurrent"), 512)
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])

	const racers = 4
	var wg sync.WaitGroup
	codes := make([]int, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i], _ = f.upload(t, f.token, f.library, "raced", body)
		}()
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		if code < 200 || code >= 300 {
			t.Fatalf("racer %d got %d, want a 2xx", i, code)
		}
	}

	job, err := f.st.IngestJobByID(t.Context(), f.user.ID,
		uploadJobID(f.user.ID, f.library, "raced"))
	if err != nil {
		t.Fatal(err)
	}
	if job.ContentSHA256 == nil || *job.ContentSHA256 != want {
		t.Fatalf("digest = %v, want %s", job.ContentSHA256, want)
	}
	if job.StagingPath == nil {
		t.Fatal("committed job has no staging path")
	}
	// The recorded bytes must still be there and still be what we sent.
	if _, err := f.cas.InspectArtifact(t.Context(), *job.StagingPath, want, job.BytesReceived); err != nil {
		t.Fatalf("a losing racer destroyed the winner's content: %v", err)
	}
}

func TestIngestJobRequiresLibraryAccess(t *testing.T) {
	f := newUploadFixture(t)
	_, out := f.upload(t, f.token, f.library, "job-status", []byte("bytes"))
	jobID, _ := out["job_id"].(string)

	readToken := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	code, got := getJSON(t, f.ts.URL+"/v1/ingest/jobs/"+jobID, readToken)
	if code != http.StatusOK {
		t.Fatalf("job status: %d %v", code, got)
	}
	if got["job_id"] != jobID || got["state"] != string(store.IngestStaged) {
		t.Fatalf("job resource = %v", got)
	}

	// A token with the right scope but no access to the job's library
	// must not see it: the capability and the ACL are separate checks.
	strangerToken := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)
	code, _ = getJSON(t, f.ts.URL+"/v1/ingest/jobs/"+jobID, strangerToken)
	if code != http.StatusNotFound {
		t.Fatalf("a user outside the library read the job: %d", code)
	}
}

// setMaxUpload and setQuota rebuild the server, since Routes captures the
// config by value.
func (f *uploadFixture) setMaxUpload(t *testing.T, n int64) {
	t.Helper()
	f.rebuild(t, func(c *config.Config) { c.Content.MaxUploadBytes = n })
}

func (f *uploadFixture) setQuota(t *testing.T, n int64) {
	t.Helper()
	f.rebuild(t, func(c *config.Config) { c.Content.QuotaBytes = n })
}

func (f *uploadFixture) rebuild(t *testing.T, apply func(*config.Config)) {
	t.Helper()
	cfg := config.Default()
	cfg.InsecureHTTP = true
	apply(&cfg)
	srv := &Server{
		St: f.st, Auth: auth.NewService(f.st), Cfg: cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		Content:      f.cas,
		Blobs:        f.cas,
		Covers:       f.cas,
	}
	f.ts.Close()
	f.handler = srv.Routes()
	f.ts = httptest.NewServer(f.handler)
	t.Cleanup(f.ts.Close)
}

func getJSON(t *testing.T, url, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// countingReader reports how many bytes the client actually handed to the
// connection, which is the only way to observe whether the server kept
// reading a body it had already refused.
type countingReader struct {
	mu   sync.Mutex
	n    int64
	left int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	for i := range p {
		p[i] = 'x'
	}
	c.left -= int64(len(p))
	c.n += int64(len(p))
	return len(p), nil
}

func (c *countingReader) count() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestOversizedUploadIsNotDrained: refusing an upload must also stop
// reading it. multipart.Part.Close drains the remainder of a part, so
// closing the part after a size rejection would make the server read the
// very data it just declined — turning every 413 into free bandwidth and
// CPU for the sender. The handler therefore closes only once the body has
// been consumed.
//
// The handler is called directly rather than over a socket. Across a
// connection the only observable figure is what the client handed the
// transport, which runs an unbounded distance ahead of the server and
// swamps the difference being measured: the body is capped at
// max_upload_bytes + uploadEnvelopeSlack either way, so both behaviours
// land in the same range and the measurement decides nothing. Reading the
// body in-process counts exactly what the server consumed.
func TestOversizedUploadIsNotDrained(t *testing.T) {
	f := newUploadFixture(t)
	f.setMaxUpload(t, 1024)

	const sent = 8 << 20
	counter := &countingReader{left: sent}
	const boundary = "drainboundary"
	body := io.MultiReader(
		strings.NewReader("--"+boundary+"\r\n"+
			`Content-Disposition: form-data; name="file"; filename="big.epub"`+
			"\r\nContent-Type: application/octet-stream\r\n\r\n"),
		counter,
		strings.NewReader("\r\n--"+boundary+"--\r\n"),
	)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/libraries/"+f.library+"/upload", body)
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Idempotency-Key", "oversized-drain")
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	// Measured in-process the two behaviours are far apart and do not
	// move: stopping reads one 4 KiB buffer, closing the part reads the
	// whole 1 MiB transport bound. The threshold sits between them with
	// two orders of magnitude of room on the honest side.
	if got := counter.count(); got > 256<<10 {
		t.Fatalf("server read %d bytes of a %d-byte upload it refused", got, sent)
	}
}

// TestUploadIsRefusedWhenTheStagingAreaIsFull: the server is out of room,
// not the client out of line. The answer has to say "later", not "smaller",
// or a retrying client will shrink its book forever and never succeed.
func TestUploadIsRefusedWhenTheStagingAreaIsFull(t *testing.T) {
	f := newUploadFixture(t)
	// A cap of exactly one upload, the smallest the config permits, plus
	// a byte of debris already on disk: the next upload cannot fit.
	f.cas.SetStagingCap(config.Default().Content.MaxUploadBytes)
	orphan := filepath.Join(f.cas.Root(), ".incoming", "crashed.stage")
	if err := os.WriteFile(orphan, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out := f.upload(t, f.token, f.library, "key-full", []byte("hello"))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("upload into a full staging area = %d, want 503: %v", code, out)
	}

	// Once the debris is gone the same upload succeeds, so the refusal was
	// about room and nothing else.
	if err := os.Remove(orphan); err != nil {
		t.Fatal(err)
	}
	if code, out := f.upload(t, f.token, f.library, "key-full",
		[]byte("hello")); code != http.StatusAccepted {
		t.Fatalf("upload after room was freed = %d: %v", code, out)
	}
}

// TestFullStagingAreaTellsTheClientToComeBack pins the header rather than
// the status alone: without it a client has no interval to back off for.
func TestFullStagingAreaTellsTheClientToComeBack(t *testing.T) {
	rec := httptest.NewRecorder()
	writeStageError(rec, content.ErrStagingFull)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After on a transient refusal")
	}
}
