//go:build linux

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	ts      *httptest.Server
	st      store.Store
	cas     *content.CAS
	user    store.User
	other   store.User
	token   string
	library string
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
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	f := &uploadFixture{
		ts: ts, st: st, cas: cas,
		user:    storetest.MkUser(t, st, "uploader"),
		other:   storetest.MkUser(t, st, "stranger"),
		library: "lib-1",
	}
	if err := st.CreateLibrary(t.Context(), store.Library{
		ID: f.library, OwnerUserID: f.user.ID, QuotaUserID: f.user.ID,
		Kind: store.LibraryManaged, Name: "Books", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	f.token = f.mintToken(t, f.user.ID, store.ScopeLibraryManage)
	return f
}

func (f *uploadFixture) mintToken(t *testing.T, userID string, scope store.Scope) string {
	t.Helper()
	secret, _, err := auth.NewService(f.st).MintToken(t.Context(), userID,
		"device-"+string(scope)+"-"+userID, store.ScopeSet{scope}, nil)
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
		f.ts.URL+"/v1/library/"+library+"/upload", &buf)
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
			f.ts.URL+"/v1/library/"+f.library+"/upload", bytes.NewReader(body))
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
			f.ts.URL+"/v1/library/"+f.library+"/upload", &buf)
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
}

func TestIngestJobRequiresLibraryAccess(t *testing.T) {
	f := newUploadFixture(t)
	_, out := f.upload(t, f.token, f.library, "job-status", []byte("bytes"))
	jobID, _ := out["job_id"].(string)

	readToken := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	code, got := getJSON(t, f.ts.URL+"/v1/library/jobs/"+jobID, readToken)
	if code != http.StatusOK {
		t.Fatalf("job status: %d %v", code, got)
	}
	if got["job_id"] != jobID || got["state"] != string(store.IngestStaged) {
		t.Fatalf("job resource = %v", got)
	}

	// A token with the right scope but no access to the job's library
	// must not see it: the capability and the ACL are separate checks.
	strangerToken := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)
	code, _ = getJSON(t, f.ts.URL+"/v1/library/jobs/"+jobID, strangerToken)
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
	}
	f.ts.Close()
	f.ts = httptest.NewServer(srv.Routes())
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
