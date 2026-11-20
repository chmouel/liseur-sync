package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// metadataPNG is a real image, so a test can tell a re-rendered cover from
// the bytes it sent.
func metadataPNG(t *testing.T, width, height int, shade uint8) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			picture.Set(x, y, color.RGBA{R: shade, G: uint8(x), B: uint8(y), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func workCoverRequest(
	t *testing.T, method, url, token, ifNoneMatch string, body []byte,
) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func metadataFields(t *testing.T, out map[string]any) map[string]map[string]any {
	t.Helper()
	fields := map[string]map[string]any{}
	for _, raw := range out["fields"].([]any) {
		field := raw.(map[string]any)
		fields[field["field"].(string)] = field
	}
	return fields
}

func TestWorkMetadataRoutes(t *testing.T) {
	ts, st := testServer(t)
	token, work := annotationTestUser(t, ts.URL, st, "u1", "alice")
	other, _ := annotationTestUser(t, ts.URL, st, "u2", "bob")
	url := ts.URL + "/v1/works/" + work + "/metadata"

	// Nothing has been said about this work yet, and saying so is not an
	// error: an empty list is "no choice", which is what a fresh device
	// needs to be told.
	code, out := get(t, url, token)
	if code != http.StatusOK {
		t.Fatalf("empty read: %d %v", code, out)
	}
	if fields := out["fields"].([]any); len(fields) != 0 {
		t.Fatalf("fresh work already has overrides: %v", out)
	}
	if _, exists := out["cover"]; exists {
		t.Fatalf("fresh work reports a cover choice: %v", out)
	}

	for _, item := range []struct {
		field string
		value any
	}{
		{"title", "Dune"},
		{"author", "Frank Herbert"},
		{"description", "A desert planet."},
	} {
		if code, out := post(t, url, token, map[string]any{
			"write_id": "w-" + item.field, "writer_id": "installation",
			"revision": 1, "field": item.field, "value": item.value,
		}); code != http.StatusOK {
			t.Fatalf("write %s: %d %v", item.field, code, out)
		}
	}
	code, out = get(t, url, token)
	if code != http.StatusOK {
		t.Fatalf("read: %d %v", code, out)
	}
	fields := metadataFields(t, out)
	if len(fields) != 3 || fields["title"]["value"] != "Dune" ||
		fields["author"]["value"] != "Frank Herbert" ||
		fields["description"]["value"] != "A desert planet." {
		t.Fatalf("fields: %v", out)
	}
	if fields["title"]["write_id"] != "w-title" || fields["title"]["updated_at"] == "" {
		t.Fatalf("field provenance: %v", fields["title"])
	}

	// An empty string is a value somebody typed, and only null unsets.
	if code, out := post(t, url, token, map[string]any{
		"write_id": "empty-title", "writer_id": "installation", "revision": 2,
		"field": "title", "value": "",
	}); code != http.StatusOK {
		t.Fatalf("explicit empty title: %d %v", code, out)
	}
	_, out = get(t, url, token)
	if fields := metadataFields(t, out); fields["title"]["value"] != "" {
		t.Fatalf("explicit empty title was lost: %v", out)
	}
	if code, out := post(t, url, token, map[string]any{
		"write_id": "unset-title", "writer_id": "installation", "revision": 3,
		"field": "title", "value": nil,
	}); code != http.StatusOK {
		t.Fatalf("unset title: %d %v", code, out)
	}
	_, out = get(t, url, token)
	fields = metadataFields(t, out)
	if _, exists := fields["title"]; exists || len(fields) != 2 {
		t.Fatalf("null left an override behind: %v", out)
	}

	// Isolation: the work belongs to one account, so another does not
	// have it at all — reading or writing.
	if code, out := get(t, url, other); code != http.StatusNotFound {
		t.Fatalf("other read: %d %v", code, out)
	}
	if code, out := post(t, url, other, map[string]any{
		"write_id": "other-title", "writer_id": "installation", "revision": 1,
		"field": "title", "value": "Mine",
	}); code != http.StatusNotFound {
		t.Fatalf("cross-user write: %d %v", code, out)
	}

	// The same write id keeps meaning the same request, and means nothing
	// else.
	if code, out := post(t, url, token, map[string]any{
		"write_id": "w-author", "writer_id": "installation", "revision": 1,
		"field": "author", "value": "Frank Herbert",
	}); code != http.StatusOK {
		t.Fatalf("identical retry: %d %v", code, out)
	}
	if code, out := post(t, url, token, map[string]any{
		"write_id": "w-author", "writer_id": "installation", "revision": 1,
		"field": "author", "value": "Someone else",
	}); code != http.StatusConflict {
		t.Fatalf("reused write id: %d %v", code, out)
	}

	// A newer revision from the same writer wins, and an older request
	// arriving afterwards is acknowledged without undoing it.
	if code, out := post(t, url, token, map[string]any{
		"write_id": "author-new", "writer_id": "installation", "revision": 5,
		"field": "author", "value": "F. Herbert",
	}); code != http.StatusOK {
		t.Fatalf("newer author: %d %v", code, out)
	}
	if code, out := post(t, url, token, map[string]any{
		"write_id": "author-old", "writer_id": "installation", "revision": 2,
		"field": "author", "value": "Early name",
	}); code != http.StatusOK {
		t.Fatalf("late older author: %d %v", code, out)
	}
	_, out = get(t, url, token)
	if fields := metadataFields(t, out); fields["author"]["value"] != "F. Herbert" {
		t.Fatalf("an older revision undid a newer one: %v", out)
	}

	// Unknown works, and malformed input, never reach a 5xx.
	if code, _ := get(t, ts.URL+"/v1/works/no-such-work/metadata", token); code != http.StatusNotFound {
		t.Fatalf("unknown work read: %d", code)
	}
	if code, out := post(t, ts.URL+"/v1/works/no-such-work/metadata", token, map[string]any{
		"write_id": "missing", "writer_id": "installation", "revision": 1,
		"field": "title", "value": "Nowhere",
	}); code != http.StatusNotFound {
		t.Fatalf("unknown work write: %d %v", code, out)
	}
	for _, bad := range []map[string]any{
		{"write_id": "bad", "writer_id": "installation", "revision": 1, "field": "title", "value": 9},
		{"write_id": "bad", "writer_id": "installation", "revision": 1, "field": "title", "value": map[string]any{}},
		{"write_id": "bad", "writer_id": "installation", "revision": 1, "field": "title", "value": strings.Repeat("x", 513)},
		{"write_id": "bad", "writer_id": "installation", "revision": 1, "field": "author", "value": strings.Repeat("é", 257)},
		{"write_id": "bad", "writer_id": "installation", "revision": 1, "field": "description", "value": strings.Repeat("x", 16385)},
		{"write_id": "bad", "writer_id": "installation", "revision": 1, "field": "title", "value": "a\x00b"},
		{"write_id": "bad", "writer_id": "installation", "revision": 1, "field": "subtitle", "value": "Title"},
		{"write_id": "bad", "writer_id": "installation", "revision": 0, "field": "title", "value": "Title"},
		{"write_id": "bad", "writer_id": "", "revision": 1, "field": "title", "value": "Title"},
		{"write_id": "", "writer_id": "installation", "revision": 1, "field": "title", "value": "Title"},
	} {
		if code, out := post(t, url, token, bad); code != http.StatusBadRequest {
			t.Fatalf("accepted %v: %d %v", bad, code, out)
		}
	}
	// The limits are bytes, not characters: 512 bytes of multi-byte text
	// is a 512-byte author.
	if code, out := post(t, url, token, map[string]any{
		"write_id": "wide-author", "writer_id": "installation", "revision": 1,
		"field": "author", "value": strings.Repeat("é", 256),
	}); code != http.StatusOK {
		t.Fatalf("512-byte author refused: %d %v", code, out)
	}
	// A body past the limit is refused before it is parsed.
	if code, out := post(t, url, token, map[string]any{
		"write_id": "huge", "writer_id": "installation", "revision": 1,
		"field": "description", "value": strings.Repeat("x", 70000),
	}); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d %v", code, out)
	}
}

func TestWorkMetadataRejectsMalformedUTF8(t *testing.T) {
	ts, st := testServer(t)
	token, work := annotationTestUser(t, ts.URL, st, "u1", "alice")
	url := ts.URL + "/v1/works/" + work + "/metadata"
	body := `{"write_id":"invalid","writer_id":"phone","revision":1,"field":"title","value":"` +
		string([]byte{0xff}) + `"}`
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed UTF-8: got %d, want 400", resp.StatusCode)
	}
}

func TestWorkCoverRoutes(t *testing.T) {
	ts, st := testServer(t)
	token, work := annotationTestUser(t, ts.URL, st, "u1", "alice")
	other, _ := annotationTestUser(t, ts.URL, st, "u2", "bob")
	url := ts.URL + "/v1/works/" + work + "/cover"

	if code, _ := get(t, url, token); code != http.StatusNotFound {
		t.Fatalf("a work with no chosen cover: %d, want 404", code)
	}

	source := metadataPNG(t, 1600, 2400, 10)
	resp, raw := workCoverRequest(t, http.MethodPut, url, token, "", source)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: %d %s", resp.StatusCode, raw)
	}
	applied := decodeJSON(t, raw)
	digest, _ := applied["sha256"].(string)
	if applied["status"] != "applied" || len(digest) != 64 || applied["updated_at"] == "" {
		t.Fatalf("put response: %v", applied)
	}

	resp, body := workCoverRequest(t, http.MethodGet, url, token, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, max-age=3600" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := resp.Header.Get("ETag"); got != `"`+digest+`"` {
		t.Fatalf("ETag = %q, want %q", got, digest)
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != digest {
		t.Fatal("the served bytes are not the digest the server named")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "jpeg" {
		t.Fatalf("served cover: %v %q", err, format)
	}
	if config.Width >= 1600 || config.Height >= 2400 {
		t.Fatalf("the cover was not re-rendered: %dx%d", config.Width, config.Height)
	}

	// Revalidation is a 304 with no body, and another size is a smaller
	// image under the same digest.
	resp, revalidated := workCoverRequest(t, http.MethodGet, url, token, `"`+digest+`"`, nil)
	if resp.StatusCode != http.StatusNotModified || len(revalidated) != 0 {
		t.Fatalf("revalidation: %d, %d bytes", resp.StatusCode, len(revalidated))
	}
	resp, thumbnail := workCoverRequest(t, http.MethodGet, url+"?size=thumbnail", token, "", nil)
	if resp.StatusCode != http.StatusOK || len(thumbnail) == 0 || len(thumbnail) >= len(body) {
		t.Fatalf("thumbnail: %d, %d bytes", resp.StatusCode, len(thumbnail))
	}
	if code, _ := get(t, url+"?size=enormous", token); code != http.StatusBadRequest {
		t.Fatalf("unknown size: %d, want 400", code)
	}

	// The choice shows up in the metadata read beside the fields, and only
	// to the account that made it.
	code, out := get(t, ts.URL+"/v1/works/"+work+"/metadata", token)
	if code != http.StatusOK {
		t.Fatalf("metadata read: %d %v", code, out)
	}
	cover := out["cover"].(map[string]any)
	if cover["sha256"] != digest || cover["updated_at"] == "" {
		t.Fatalf("metadata cover: %v", cover)
	}
	if code, _ := get(t, ts.URL+"/v1/works/"+work+"/metadata", other); code != http.StatusNotFound {
		t.Fatalf("cross-user metadata read: %d, want 404", code)
	}
	if code, _ := get(t, url, other); code != http.StatusNotFound {
		t.Fatalf("cross-user cover read: %d, want 404", code)
	}

	// A delete records the choice so a client stops asking, and the
	// publication's own cover is the catalog's business again.
	code, out = del(t, url, token)
	if code != http.StatusOK || out["status"] != "deleted" || out["updated_at"] == "" {
		t.Fatalf("delete: %d %v", code, out)
	}
	if code, _ := get(t, url, token); code != http.StatusNotFound {
		t.Fatalf("deleted cover still served: %d", code)
	}
	code, out = get(t, ts.URL+"/v1/works/"+work+"/metadata", token)
	cover = out["cover"].(map[string]any)
	if code != http.StatusOK || cover["sha256"] != nil || cover["updated_at"] == "" {
		t.Fatalf("tombstone: %d %v", code, out)
	}

	// Refusals: nothing sent, bytes that are not an image, an image past
	// the source cap, and a work nobody has.
	if resp, _ := workCoverRequest(t, http.MethodPut, url, token, "", nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty body: %d", resp.StatusCode)
	}
	if resp, _ := workCoverRequest(t, http.MethodPut, url, token, "", []byte("not an image at all")); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed image: %d", resp.StatusCode)
	}
	oversized := make([]byte, (16<<20)+1)
	if resp, _ := workCoverRequest(t, http.MethodPut, url, token, "", oversized); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized image: %d, want 413", resp.StatusCode)
	}
	if resp, _ := workCoverRequest(t, http.MethodPut, ts.URL+"/v1/works/no-such-work/cover", token, "", source); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown work put: %d", resp.StatusCode)
	}
	if code, _ := del(t, ts.URL+"/v1/works/no-such-work/cover", token); code != http.StatusNotFound {
		t.Fatalf("unknown work delete: %d", code)
	}
}

func TestWorkMetadataMergeConflictIsReported(t *testing.T) {
	ts, st := testServer(t)
	token, into := annotationTestUser(t, ts.URL, st, "u1", "alice")
	from := storetest.MkWork(t, st, store.User{ID: "u1"}, "metadata-source", "metadata-source-sha")
	write := func(work, id, writer string, revision int64) {
		t.Helper()
		if code, out := post(t, ts.URL+"/v1/works/"+work+"/metadata", token, map[string]any{
			"write_id": id, "writer_id": writer, "revision": revision,
			"field": "title", "value": "Title",
		}); code != http.StatusOK {
			t.Fatalf("write: %d %v", code, out)
		}
	}
	write(from.ID, "a20", "writer-a", 20)
	write(into, "b20", "writer-b", 20)
	write(into, "a10", "writer-a", 10)
	write(from.ID, "b10", "writer-b", 10)
	code, out := post(t, ts.URL+"/v1/works/merge", token, map[string]any{
		"from_work_id": from.ID, "into_work_id": into,
	})
	if code != http.StatusConflict || out["error"] != "rewrite the metadata override before merging these works" {
		t.Fatalf("merge conflict: %d %v", code, out)
	}
}
