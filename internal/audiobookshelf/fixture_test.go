package audiobookshelf

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

type fixture struct {
	st       store.Store
	user     store.User
	folder   store.Folder
	now      time.Time
	observed map[string][]store.ObservedBook // by folder
	seq      int
	ids      map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	f := &fixture{st: st, now: now, ids: map[string]string{}, observed: map[string][]store.ObservedBook{}}
	f.user = f.addUser(t, "reader")
	f.folder = f.addFolder(t, "f-main", f.user.ID)
	return f
}

func (f *fixture) addUser(t *testing.T, name string) store.User {
	t.Helper()
	u := store.User{ID: "u-" + name, Name: name, Argon2Hash: "x", Timezone: "UTC", CreatedAt: f.now}
	if err := f.st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) addFolder(t *testing.T, id string, grantees ...string) store.Folder {
	t.Helper()
	folder := store.Folder{ID: id, Name: id, RootPath: "/srv/" + id, Kind: store.FolderPlain, CreatedAt: f.now, UpdatedAt: f.now}
	if err := f.st.CreateFolder(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	for _, g := range grantees {
		if err := f.st.AssignUserFolder(context.Background(), g, id); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

// book records one book in folderID and returns its catalog id. Every
// pass re-observes the folder's earlier books, as a watcher would.
func (f *fixture) book(t *testing.T, folderID, name, title string, authors ...string) string {
	t.Helper()
	f.seq++
	obs := store.ObservedBook{
		RelativePath: name + ".epub", SizeBytes: int64(1000 + f.seq), MTime: f.now,
		ContentSHA256:    fmt.Sprintf("%064x", f.seq),
		OriginalFilename: name + ".epub", MediaType: "application/epub+zip", Title: title,
	}
	for i, a := range authors {
		obs.Contributors = append(obs.Contributors, store.ObservedContributor{Name: a, Role: "author", Position: i})
	}
	f.observed[folderID] = append(f.observed[folderID], obs)
	if _, err := f.st.ReconcileFolder(context.Background(), folderID, f.observed[folderID], true, f.now); err != nil {
		t.Fatal(err)
	}
	known, err := f.st.BooksInFolder(context.Background(), folderID)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range known {
		if k.RelativePath == obs.RelativePath {
			f.ids[name] = k.ID
			return k.ID
		}
	}
	t.Fatalf("book %q was not recorded", name)
	return ""
}
