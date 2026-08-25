package webui

import (
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestWorkPageAnnotationsPanel covers ADR-0028's work-page panel: the
// reader's own annotations render — as text, never as markup — and
// the panel is simply absent when there are none.
func TestWorkPageAnnotationsPanel(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.CreateWork(ctx,
		store.Work{ID: "wa", UserID: "u1", Title: "Annotated Book", CreatedAt: time.Now()},
		nil, []store.Identifier{{Kind: "sha256", Value: "aaaa"}}); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)

	// No annotations: no panel.
	code, body := page(t, ts, cookie, "/ui/works/wa")
	if code != 200 {
		t.Fatalf("work page: %d", code)
	}
	if strings.Contains(body, `id="annotations"`) {
		t.Fatal("annotations panel rendered with nothing to show")
	}

	prog := 0.25
	if _, err := st.PushAnnotations(ctx, "u1", "dev1", []store.AnnotationWrite{{
		ID: "a1", WorkID: "wa", Kind: store.AnnotationHighlight,
		LocatorJSON: []byte(`{"href":"c1"}`), Progression: &prog,
		Excerpt: "the sea was <calm>", Color: "green",
		Body:     "a note with <script>alert(1)</script> in it",
		ClientTS: time.Now(),
	}}, 100); err != nil {
		t.Fatal(err)
	}

	_, body = page(t, ts, cookie, "/ui/works/wa")
	if !strings.Contains(body, `id="annotations"`) {
		t.Fatal("annotations panel missing")
	}
	// Text, never markup: the store holds the raw string, the page
	// shows it escaped.
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("annotation body reached the page as markup")
	}
	if !strings.Contains(body, "the sea was &lt;calm&gt;") {
		t.Fatal("annotation excerpt missing or unescaped")
	}
	if !strings.Contains(body, "ann-color-green") {
		t.Fatal("palette token did not map to its fixed class")
	}

	// Another reader's page never shows it: the work itself is
	// per-user, so for bob it does not exist at all — the same 404
	// TestCrossUserIsolation pins. This test only adds the panel's
	// data to that story.
}
