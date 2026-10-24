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

type fakeAudiobookshelf struct {
	calls      int
	disquietID string
	leftHandID string
	essaysID   string
}

func (f *fakeAudiobookshelf) View(context.Context, string) ([]audiobookshelf.ItemView, error) {
	f.calls++
	localProg := 0.54
	activeLocalProg := 0.38
	disquietID := f.disquietID
	if disquietID == "" {
		disquietID = "b-disquiet"
	}
	leftHandID := f.leftHandID
	if leftHandID == "" {
		leftHandID = "b-left-hand"
	}
	essaysID := f.essaysID
	if essaysID == "" {
		essaysID = "b-essays"
	}
	return []audiobookshelf.ItemView{
		{
			Item: audiobookshelf.Item{
				ID:       "audio",
				Title:    "A Book",
				Authors:  []string{"Ursula K. Le Guin"},
				Duration: 41400,
				Chapters: make([]audiobookshelf.Chapter, 18),
			},
		},
		{
			BookTitle:        "Disquiet Gods",
			BookSubtitle:     "The Sun Eater · Book Six",
			BookAuthors:      []string{"Christopher Ruocchio"},
			BookPresent:      f.disquietID != "",
			BookCanRead:      true,
			LocalProgression: &localProg,
			RemoteProgress: &audiobookshelf.Progress{
				CurrentTime: 15000,
				Duration:    80100,
				Progress:    0.187,
			},
			Item: audiobookshelf.Item{
				ID:       "partial",
				Title:    "Les Dieux inquiets",
				Subtitle: "Le Dévoreur de soleil · Tome 6 (Partie 2)",
				Authors:  []string{"Christopher Ruocchio"},
				Duration: 80100,
				Chapters: make([]audiobookshelf.Chapter, 32),
			},
			Link: &store.AudiobookLink{BookID: disquietID, Generation: "g", State: store.AudiobookLinkNeedsMapping},
			Mapping: audiobookshelf.Mapping{CoversLast: true, Segments: []audiobookshelf.Segment{
				{Label: "Chapitre 37", AudioStart: 0, AudioEnd: 600, BookStart: 0.5, BookEnd: 0.6, Approx: true},
				{Label: "Chapitre 38", AudioStart: 600, AudioEnd: 1200, BookStart: 0.6, BookEnd: 0.7},
			}},
		},
		{
			BookTitle:        "The Left Hand of Darkness",
			BookAuthors:      []string{"Ursula K. Le Guin"},
			BookPresent:      f.leftHandID != "",
			BookCanRead:      true,
			LocalProgression: &activeLocalProg,
			RemoteProgress: &audiobookshelf.Progress{
				CurrentTime: 13680,
				Duration:    36000,
				Progress:    0.38,
			},
			Item: audiobookshelf.Item{
				ID:       "following",
				Title:    "The Left Hand of Darkness",
				Authors:  []string{"Ursula K. Le Guin"},
				Duration: 36000,
				Chapters: make([]audiobookshelf.Chapter, 20),
			},
			Link: &store.AudiobookLink{BookID: leftHandID, Generation: "g-active", State: store.AudiobookLinkActive},
			Mapping: audiobookshelf.Mapping{FullNarrative: true, CoversFirst: true, CoversLast: true, Segments: []audiobookshelf.Segment{
				{Label: "Chapter 1", AudioStart: 0, AudioEnd: 1800, BookStart: 0.02, BookEnd: 0.08},
				{Label: "Chapter 20", AudioStart: 34200, AudioEnd: 36000, BookStart: 0.94, BookEnd: 0.99},
			}},
		},
		{
			BookTitle:   "Collected Essays",
			BookAuthors: []string{"Virginia Woolf"},
			BookPresent: f.essaysID != "",
			Item: audiobookshelf.Item{
				ID:       "linear",
				Title:    "A recording without chapter headings",
				Authors:  []string{"Virginia Woolf"},
				Duration: 19800,
			},
			Link:          &store.AudiobookLink{BookID: essaysID, Generation: "g2", State: store.AudiobookLinkNeedsMapping},
			LinearAllowed: true,
		},
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
	if !strings.Contains(body, "Needs confirmation") || !strings.Contains(body, "Following") || !strings.Contains(body, "Unmatched") || !strings.Contains(body, "abs-cstart-11 abs-cend-15") {
		t.Fatalf("missing priority sections or coverage ruler: %s", body)
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
	code, filtered := page(t, ts, cookie, "/ui/audiobookshelf?state=active")
	if code != 200 || !strings.Contains(filtered, "The Left Hand of Darkness") || strings.Contains(filtered, "Les Dieux inquiets") {
		t.Fatalf("active filter: %d %s", code, filtered)
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
	fake := &fakeAudiobookshelf{}
	ts, st := testServerCfg(t, func(c *config.Config) { c.Audiobookshelf.Enabled = true; c.Audiobookshelf.Account = "alice" }, func(s *Server) { s.Audiobookshelf = fake })
	cookie := loginCookie(t, ts)
	fake.disquietID = seedFolderBook(t, st, "f-disquiet", "disquiet.epub", "Disquiet Gods")
	fake.leftHandID = seedFolderBook(t, st, "f-lefthand", "lefthand.epub", "The Left Hand of Darkness")
	cmd := exec.Command("node", filepath.Join("testdata", "uishots.mjs"))
	cmd.Env = append(os.Environ(), "SHOT_CHROME="+chrome, "SHOT_URL="+ts.URL, "SHOT_COOKIE="+cookie.Name+"="+cookie.Value,
		"SHOT_DIR="+out, "SHOT_PATHS=/ui/audiobookshelf", "SHOT_NAMES=audiobookshelf", "SHOT_WIDTHS=1440,420")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("screenshot: %v\n%s", err, data)
	}
}
