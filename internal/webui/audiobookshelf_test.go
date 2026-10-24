package webui

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/audiobookshelf"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

type fakeAudiobookshelf struct{ calls int }

func (f *fakeAudiobookshelf) View(context.Context, string) ([]audiobookshelf.ItemView, error) {
	f.calls++
	return []audiobookshelf.ItemView{
		{Item: audiobookshelf.Item{ID: "audio", Title: "A Book"}},
		{Item: audiobookshelf.Item{ID: "partial", Title: "Les Dieux inquiets"},
			Link: &store.AudiobookLink{Generation: "g", State: store.AudiobookLinkNeedsMapping},
			Mapping: audiobookshelf.Mapping{CoversLast: true, Segments: []audiobookshelf.Segment{
				{Label: "Chapitre 37", AudioStart: 0, AudioEnd: 600, BookStart: 0.5, BookEnd: 0.6, Approx: true},
				{Label: "Chapitre 38", AudioStart: 600, AudioEnd: 1200, BookStart: 0.6, BookEnd: 0.7},
			}}},
		{Item: audiobookshelf.Item{ID: "linear", Title: "A recording without chapter headings"},
			Link: &store.AudiobookLink{Generation: "g2", State: store.AudiobookLinkNeedsMapping}, LinearAllowed: true},
	}, nil
}
func (f *fakeAudiobookshelf) Link(context.Context, string, string, string) error {
	f.calls++
	return nil
}
func (f *fakeAudiobookshelf) Mutate(context.Context, string, string, string, string) error {
	f.calls++
	return store.ErrConflict
}

func TestAudiobookshelfPageAndMutationsRequireAccountAndCSRF(t *testing.T) {
	fake := &fakeAudiobookshelf{}
	ts, st := testServerCfg(t, func(c *config.Config) { c.Audiobookshelf.Enabled = true; c.Audiobookshelf.Account = "alice" }, func(s *Server) { s.Audiobookshelf = fake })
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/audiobookshelf")
	if code != 200 || !strings.Contains(body, "A Book") || !strings.Contains(body, `href="audiobookshelf" aria-current="page"`) {
		t.Fatalf("page: %d %s", code, body)
	}
	csrf := extractCSRF(t, body)
	code, _ = postForm(t, ts, cookie, "/ui/audiobookshelf", url.Values{"action": {"link"}, "item_id": {"audio"}, "book_id": {"book"}})
	if code != 403 || fake.calls != 1 {
		t.Fatalf("missing csrf: %d calls=%d", code, fake.calls)
	}
	code, _ = postForm(t, ts, cookie, "/ui/audiobookshelf", url.Values{"csrf": {csrf}, "action": {"link"}, "item_id": {"audio"}, "book_id": {"book"}})
	if code != 303 || fake.calls != 2 {
		t.Fatalf("link: %d calls=%d", code, fake.calls)
	}
	code, _ = postForm(t, ts, cookie, "/ui/audiobookshelf", url.Values{"csrf": {csrf}, "action": {"unlink"}, "item_id": {"audio"}, "generation": {"old"}})
	if code != 409 {
		t.Fatalf("stale form: %d", code)
	}
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	// Administrator rights do not grant access to another account's integration
	ts2, st2 := testServerCfg(t, func(c *config.Config) { c.Audiobookshelf.Enabled = true; c.Audiobookshelf.Account = "somebody-else" }, func(s *Server) { s.Audiobookshelf = fake })
	if err := st2.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	other := loginCookie(t, ts2)
	before := fake.calls
	code, body = page(t, ts2, other, "/ui/audiobookshelf")
	if code != 404 || strings.Contains(body, "A Book") || fake.calls != before {
		t.Fatalf("other account: %d calls=%d", code, fake.calls)
	}
	resp, _ := get(t, ts, nil, "/ui/audiobookshelf")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
}

func TestAudiobookshelfScreenshot(t *testing.T) {
	out := os.Getenv("LISEUR_ABS_SCREENSHOT")
	if out == "" {
		t.Skip("set LISEUR_ABS_SCREENSHOT for visual verification")
	}
	chrome := os.Getenv("LISEUR_CHROME")
	if chrome == "" {
		t.Fatal("LISEUR_CHROME is required")
	}
	ts, _ := testServerCfg(t, func(c *config.Config) { c.Audiobookshelf.Enabled = true; c.Audiobookshelf.Account = "alice" }, func(s *Server) { s.Audiobookshelf = &fakeAudiobookshelf{} })
	cookie := loginCookie(t, ts)
	cmd := exec.Command("node", filepath.Join("testdata", "uishots.mjs"))
	cmd.Env = append(os.Environ(), "SHOT_CHROME="+chrome, "SHOT_URL="+ts.URL, "SHOT_COOKIE="+cookie.Name+"="+cookie.Value,
		"SHOT_DIR="+out, "SHOT_PATHS=/ui/audiobookshelf", "SHOT_NAMES=audiobookshelf", "SHOT_WIDTHS=1440,420")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("screenshot: %v\n%s", err, data)
	}
}
