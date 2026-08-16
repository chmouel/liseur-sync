//go:build linux

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// makeEPUBWithContributorsAndSeries builds a real EPUB whose metadata
// carries two contributors in different roles and a series membership
// with a fractional position, using the same EPUB3 markup the epub
// package's own tests parse (belongs-to-collection / marc:relators). It
// exists because catalogBookJSON's contributor and series fields are
// otherwise never exercised by anything other than bytes.
func makeEPUBWithContributorsAndSeries(t *testing.T, title string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string, method uint16) {
		t.Helper()
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OPS/book.opf"
 media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate)
	add("OPS/book.opf", `<package xmlns="http://www.idpf.org/2007/opf">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
 <dc:title>`+title+`</dc:title>
 <dc:creator id="creator">Ada Author</dc:creator>
 <meta refines="#creator" property="role" scheme="marc:relators">aut</meta>
 <dc:contributor id="translator">Tara Translator</dc:contributor>
 <meta refines="#translator" property="role" scheme="marc:relators">trl</meta>
 <meta id="series" property="belongs-to-collection">Payload Cycle</meta>
 <meta refines="#series" property="collection-type">series</meta>
 <meta refines="#series" property="group-position">2.5</meta>
</metadata>
<manifest><item href="nav.xhtml" media-type="application/xhtml+xml"
 properties="nav"/></manifest></package>`, zip.Deflate)
	add("OPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml">
<body><nav/></body></html>`, zip.Deflate)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bookRow finds the JSON object for bookID in a "books" array response.
func bookRow(t *testing.T, raw []byte, bookID string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	rows, _ := body["books"].([]any)
	for _, row := range rows {
		m, _ := row.(map[string]any)
		if m["book_id"] == bookID {
			return m
		}
	}
	t.Fatalf("book %s not present in %s", bookID, raw)
	return nil
}

// normalizeBookRow strips the fields every route computes but that are
// allowed to legitimately differ in float encoding between two
// json.Unmarshal calls of the same server payload — there are none in
// practice, but sorting the slices makes the comparison independent of
// any incidental ordering difference the two routes might have.
func normalizeBookRow(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	if cs, ok := m["contributors"].([]any); ok {
		sort.Slice(cs, func(i, j int) bool {
			a, _ := json.Marshal(cs[i])
			b, _ := json.Marshal(cs[j])
			return string(a) < string(b)
		})
	}
	if ss, ok := m["series"].([]any); ok {
		sort.Slice(ss, func(i, j int) bool {
			a, _ := json.Marshal(ss[i])
			b, _ := json.Marshal(ss[j])
			return string(a) < string(b)
		})
	}
	return m
}

// TestCatalogBookShapeIsIdenticalOnEveryRoute pins ADR-0015's promise: a
// listing row, a search hit, an entity-scoped listing and the detail view
// all render catalogBookJSON, so a client parses exactly one shape. If a
// route stops calling the shared renderer this test is the one that
// notices.
func TestCatalogBookShapeIsIdenticalOnEveryRoute(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.writeBook(t, "shaped.epub", makeEPUBWithContributorsAndSeries(t, "Shaped Book"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	_, listRaw := f.get(t, "/v1/folders/"+f.folder.ID+"/books", tok)
	fromList := normalizeBookRow(t, bookRow(t, listRaw, bookID))

	_, searchRaw := f.get(t, "/v1/folders/"+f.folder.ID+"/search?q=Shaped", tok)
	fromSearch := normalizeBookRow(t, bookRow(t, searchRaw, bookID))

	// The entity route is keyed by the store-minted entity id, not its
	// name, so the series id has to come from the entities list first.
	_, entitiesRaw := f.get(t, "/v1/entities/series", tok)
	var entitiesBody struct {
		Entities []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(entitiesRaw, &entitiesBody); err != nil {
		t.Fatal(err)
	}
	var seriesID string
	for _, e := range entitiesBody.Entities {
		if e.Name == "Payload Cycle" {
			seriesID = e.ID
		}
	}
	if seriesID == "" {
		t.Fatalf("Payload Cycle series not found: %+v", entitiesBody.Entities)
	}
	_, seriesRaw := f.get(t,
		"/v1/entities/series/"+seriesID+"/books", tok)
	fromSeries := normalizeBookRow(t, bookRow(t, seriesRaw, bookID))

	respDetail, detailRaw := f.get(t, "/v1/books/"+bookID, tok)
	if respDetail.StatusCode != http.StatusOK {
		t.Fatalf("detail: %d", respDetail.StatusCode)
	}
	var fromDetail map[string]any
	if err := json.Unmarshal(detailRaw, &fromDetail); err != nil {
		t.Fatal(err)
	}
	fromDetail = normalizeBookRow(t, fromDetail)

	for name, got := range map[string]map[string]any{
		"search": fromSearch, "series entity": fromSeries, "detail": fromDetail,
	} {
		if !reflect.DeepEqual(fromList, got) {
			t.Fatalf("%s payload diverges from the listing:\nlist: %#v\n%s: %#v",
				name, fromList, name, got)
		}
	}

	// The metadata actually made it through the pipeline, not just that
	// the four routes agree with each other about nothing.
	contribs, _ := fromList["contributors"].([]any)
	if len(contribs) != 2 {
		t.Fatalf("contributors = %#v, want 2", contribs)
	}
	series, _ := fromList["series"].([]any)
	if len(series) != 1 || series[0].(map[string]any)["position"] != 2.5 {
		t.Fatalf("series = %#v", series)
	}
}

// TestCatalogPayloadKeepsEmptyRelationsPresent guards the comment in
// catalogBookJSON: a book with no contributors and no series must still
// carry those keys as empty arrays. A client distinguishes "no data yet"
// from "field omitted", and only the former is true of a freshly
// reconciled book with no dc:creator or series.
func TestCatalogPayloadKeepsEmptyRelationsPresent(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "bare", []byte("no metadata at all"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	_, raw := f.get(t, "/v1/books/"+bookID, tok)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	contribs, ok := body["contributors"]
	if !ok {
		t.Fatal("contributors field is absent, want an empty array")
	}
	if arr, ok := contribs.([]any); !ok || arr == nil {
		t.Fatalf("contributors = %#v, want []", contribs)
	}
	series, ok := body["series"]
	if !ok {
		t.Fatal("series field is absent, want an empty array")
	}
	if arr, ok := series.([]any); !ok || arr == nil {
		t.Fatalf("series = %#v, want []", series)
	}
}

// countingStore wraps a store.Store to count relation-batch reads, so a
// test can hold the API to the promise in catalogBooksJSON's comment:
// one relations query per page, never one per row.
type countingStore struct {
	store.Store
	relationCalls int
}

func (c *countingStore) CatalogBookRelationsForBooks(
	ctx context.Context, userID string, bookIDs []string,
) (store.CatalogBookRelations, error) {
	c.relationCalls++
	return c.Store.CatalogBookRelationsForBooks(ctx, userID, bookIDs)
}

// TestCatalogListingReadsRelationsOnceRegardlessOfPageSize is the load-
// bearing reason catalogBooksJSON batches: a page of N books must cost
// one relations query, not N. A regression here turns a book list into
// an O(n) query storm.
func TestCatalogListingReadsRelationsOnceRegardlessOfPageSize(t *testing.T) {
	f := newFolderFixture(t)
	for i := 0; i < 5; i++ {
		f.publish(t, "counted-"+string(rune('a'+i)), []byte("book body"))
	}
	counting := &countingStore{Store: f.srv.St}
	f.srv.St = counting
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, raw := f.get(t, "/v1/folders/"+f.folder.ID+"/books?limit=50", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d %s", resp.StatusCode, raw)
	}
	var body struct {
		Books []map[string]any `json:"books"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Books) != 5 {
		t.Fatalf("books = %d, want 5", len(body.Books))
	}
	if counting.relationCalls != 1 {
		t.Fatalf("relation batch calls = %d, want 1", counting.relationCalls)
	}
}
