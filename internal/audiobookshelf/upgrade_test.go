package audiobookshelf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// legacyAudiobookshelfFile is a deployment configured before the admin
// page existed: an operator put stray slashes on the address and startup
// accepted it.
const legacyAudiobookshelfFile = `insecure_http = true

[audiobookshelf]
enabled = true
base_url = "https://abs.example.com///"
account = "reader"
api_key = "key"
poll_interval = "30s"
timeout = "5s"
session_overlap = "10m"
active_days = 30

[speech_to_text]
url = "https://stt.example/v1"
api_key = "stt-key"
model = "whisper-small"

[content]
cache_dir = "covers"
`

// legacyPeerIdentity is the hash a stored deployment was keyed by: the
// address startup kept, one trailing slash removed, exactly. It is
// written out here rather than called through PeerIdentity so a change
// to how many slashes that function drops fails this file.
func legacyPeerIdentity(startupBaseURL, userID string) string {
	sum := sha256.Sum256([]byte("audiobookshelf\n" + strings.TrimSuffix(startupBaseURL, "/") + "\n" + userID))
	return hex.EncodeToString(sum[:8])
}

// TestALegacyAddressKeepsItsStoredHistory: an address with more than one
// trailing slash was already accepted, and the links, rejections and
// imported sessions of that deployment are keyed by the identity startup
// derived from it. Reloading the same file must keep all three, and must
// not import the same remote sessions a second time.
func TestALegacyAddressKeepsItsStoredHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(legacyAudiobookshelfFile), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Audiobookshelf.BaseURL; got != "https://abs.example.com//" {
		t.Fatalf("startup normalised the address to %q", got)
	}
	// The file said "https://abs.example.com///"; startup kept two
	// slashes of it, and the peer identity was that with one more gone.
	legacyPeer := legacyPeerIdentity("https://abs.example.com//", "remote-user")
	if got := PeerIdentity(loaded.Audiobookshelf.BaseURL, "remote-user"); got != legacyPeer {
		t.Fatalf("the peer identity is now %q, the stored rows are under %q", got, legacyPeer)
	}

	f := newFixture(t)
	audio := evenAudio("Chapter 1", "Chapter 2", "Chapter 3")
	f.book(t, "f-main", "book", "The Book", "An Author")
	f.book(t, "f-main", "other", "Another Book", "Another Author")
	var mu sync.Mutex
	var sessions []Session
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/me":
			fmt.Fprint(w, `{"id":"remote-user"}`)
		case "/api/libraries":
			fmt.Fprint(w, `{"libraries":[{"id":"l","mediaType":"book"}]}`)
		case "/api/libraries/l/items":
			fmt.Fprint(w, `{"total":2,"results":[
				{"id":"item","mediaType":"book","media":{"duration":300,"metadata":{"title":"The Book","authorName":"An Author"}}},
				{"id":"item2","mediaType":"book","media":{"duration":300,"metadata":{"title":"Another Book","authorName":"Another Author"}}}]}`)
		case "/api/items/item", "/api/items/item2":
			writeItem(t, w, strings.TrimPrefix(r.URL.Path, "/api/items/"), audio)
		case "/api/me/item/listening-sessions/item":
			writeSessions(t, w, sessions)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	service := NewService(f.st, loaded.Audiobookshelf, loaded.SpeechToText)
	service.Client = client
	service.loadBook = func(context.Context, store.CatalogBook) ([]BookChapter, []Resource, error) {
		chapters, resources := evenBook("Chapter 1", "Chapter 2", "Chapter 3")
		return chapters, resources, nil
	}
	ctx := t.Context()

	book, err := f.st.CatalogBookByID(ctx, f.user.ID, f.ids["book"])
	if err != nil {
		t.Fatal(err)
	}
	item, err := client.Item(ctx, "item")
	if err != nil {
		t.Fatal(err)
	}
	seed, err := service.newLink(ctx, f.user.ID, legacyPeer, book, item, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if seed.State != store.AudiobookLinkActive {
		t.Fatalf("the seed link is not active: %+v", seed)
	}
	if err := f.st.CreateAudiobookLink(ctx, f.user.ID, seed); err != nil {
		t.Fatal(err)
	}
	stored, err := f.st.AudiobookLinks(ctx, f.user.ID, legacyPeer)
	if err != nil || len(stored) != 1 {
		t.Fatalf("seed link: %+v %v", stored, err)
	}
	seedLink := stored[0]

	// One listening session, imported by the version that wrote this
	// database, under the legacy key and device prefix.
	var mapping Mapping
	if err := json.Unmarshal(seedLink.Mapping, &mapping); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	remote := Session{ID: "session-1", LibraryItemID: "item", TimeListening: 60,
		StartTime: 30, CurrentTime: 90,
		StartedAt: now.Add(-time.Minute).UnixMilli(), UpdatedAt: now.UnixMilli()}
	var seeded Report
	if err := service.importSession(ctx, f.user.ID, seedLink, mapping, remote, &seeded); err != nil {
		t.Fatal(err)
	}
	if seeded.Imported != 1 {
		t.Fatalf("the seed session was not imported: %+v", seeded)
	}
	sessions = []Session{remote}

	// A rejection of the second item: the reader said no to that pair.
	book2, err := f.st.CatalogBookByID(ctx, f.user.ID, f.ids["other"])
	if err != nil {
		t.Fatal(err)
	}
	item2, err := client.Item(ctx, "item2")
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := service.newLink(ctx, f.user.ID, legacyPeer, book2, item2, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.CreateAudiobookLink(ctx, f.user.ID, rejected); err != nil {
		t.Fatal(err)
	}
	stored, err = f.st.AudiobookLinks(ctx, f.user.ID, legacyPeer)
	if err != nil {
		t.Fatal(err)
	}
	seedGeneration := seedLink.Generation
	var rejectGeneration string
	for _, l := range stored {
		if l.ItemID == "item2" {
			rejectGeneration = l.Generation
		}
	}
	if rejectGeneration == "" {
		t.Fatalf("seed links: %+v", stored)
	}
	if err := f.st.DeleteAudiobookLink(ctx, f.user.ID, legacyPeer, "item2", rejectGeneration, true); err != nil {
		t.Fatal(err)
	}

	// The reload, as a start after the upgrade would run it.
	report, err := service.Pass(ctx)
	if err != nil || report.FirstError != nil {
		t.Fatalf("pass: %+v %v", report, err)
	}
	if report.Imported != 0 {
		t.Fatalf("the upgrade imported the same listening history again: %+v", report)
	}
	links, err := f.st.AudiobookLinks(ctx, f.user.ID, legacyPeer)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].ItemID != "item" || links[0].Generation != seedGeneration {
		t.Fatalf("the stored link did not survive the upgrade: %+v", links)
	}
	rejections, err := f.st.AudiobookRejections(ctx, f.user.ID, legacyPeer)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejections) != 1 || rejections[0].ItemID != "item2" {
		t.Fatalf("the rejection did not survive the upgrade: %+v", rejections)
	}
	key, sessionID := sessionRevision(legacyPeer, remote)
	imported, err := f.st.AudiobookSessionImported(ctx, f.user.ID, key, sessionID)
	if err != nil || !imported {
		t.Fatalf("the imported session is no longer recorded under the legacy peer: %v", err)
	}
}

// writeItem answers one library item with the chapter list its mapping
// is computed from.
func writeItem(t *testing.T, w http.ResponseWriter, id string, audio []Chapter) {
	t.Helper()
	titles := map[string]string{"item": "The Book", "item2": "Another Book"}
	authors := map[string]string{"item": "An Author", "item2": "Another Author"}
	if err := json.NewEncoder(w).Encode(map[string]any{
		"id": id, "mediaType": "book",
		"media": map[string]any{
			"duration": float64(len(audio) * 100), "chapters": audio,
			"metadata": map[string]any{"title": titles[id], "authorName": authors[id]},
		},
	}); err != nil {
		t.Error(err)
	}
}

func writeSessions(t *testing.T, w http.ResponseWriter, sessions []Session) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(SessionPage{Total: len(sessions), NumPages: 1, Sessions: sessions}); err != nil {
		t.Error(err)
	}
}
