package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

func newAdminStore(t *testing.T) store.Store {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return st
}

func addUser(t *testing.T, st store.Store, name string) store.User {
	t.Helper()
	u := store.User{
		ID:         name + "-id",
		Name:       name,
		Argon2Hash: "x",
		Timezone:   "UTC",
		CreatedAt:  time.Now().UTC(),
	}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

// capture runs an admin command with stdout redirected, because these
// commands report the ids an operator needs for the next command and a
// silent success would be useless.
func capture(t *testing.T, st store.Store, args ...string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	runErr := Run(st, args)
	os.Stdout = saved
	w.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := r.Read(buf)
		sb.Write(buf[:n])
		if readErr != nil {
			break
		}
	}
	r.Close()
	return sb.String(), runErr
}

// folderIDFrom pulls the id out of an add-folder line, which is the only
// way an operator learns the id remove-folder wants.
func folderIDFrom(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "(id ")
	if !ok {
		t.Fatalf("no folder id in %q", out)
	}
	id, _, ok := strings.Cut(rest, ")")
	if !ok {
		t.Fatalf("unterminated folder id in %q", out)
	}
	return id
}

// TestAddListRemoveFolder walks the whole CLI story: point at a
// directory, see it listed, stop watching it. There is nothing else to
// configure, and that is the point of the redesign.
func TestAddListRemoveFolder(t *testing.T) {
	st := newAdminStore(t)
	root := t.TempDir()

	added, err := capture(t, st, "add-folder", "Books", root)
	if err != nil {
		t.Fatal(err)
	}
	id := folderIDFrom(t, added)
	if !strings.Contains(added, root) || !strings.Contains(added, "never writes") {
		t.Fatalf("add-folder said %q", added)
	}

	listed, err := capture(t, st, "list-folders")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, id) || !strings.Contains(listed, root) ||
		!strings.Contains(listed, string(store.FolderPlain)) {
		t.Fatalf("list-folders said %q", listed)
	}

	removed, err := capture(t, st, "remove-folder", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(removed, "nothing under that directory was changed") {
		t.Fatalf("remove-folder said %q", removed)
	}
	after, err := st.ListFolders(t.Context(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("%d folders survived removal", len(after))
	}
	// The directory itself is somebody else's. Removing the folder must
	// not have taken it with them.
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("remove-folder touched the disk: %v", err)
	}
}

// TestFolderKindIsDetectedNotAsked: a tree holding metadata.db is a
// Calibre folder, and nobody is asked to say so. Getting this wrong
// would key the folder by path instead of by calibre_id, and every
// title edit in Calibre would then look like a delete plus an add.
func TestFolderKindIsDetectedNotAsked(t *testing.T) {
	st := newAdminStore(t)
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "metadata.db"), []byte("x"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	folder, err := NewFolder(t.Context(), st, "Calibre", root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if folder.Kind != store.FolderCalibre {
		t.Fatalf("kind = %q, want calibre", folder.Kind)
	}
}

// TestFolderRootMustExistAndBeADirectory: a typo in a path is the
// likeliest thing to go wrong here and the cheapest thing to catch, so
// it is caught while the operator is still looking at their terminal
// rather than in a log half an hour later.
func TestFolderRootMustExistAndBeADirectory(t *testing.T) {
	st := newAdminStore(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, root, want string }{
		{"missing", filepath.Join(dir, "nope"), "no such file"},
		{"a file", file, "not a directory"},
		{"blank", "  ", "must not be blank"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewFolder(t.Context(), st, "Books", tc.root, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestFolderRootAllowlistBindsTheBrowserNotTheShell: the allowlist
// exists so an administrator's session cannot name any path on the
// machine. It is a subtree test, not an equality test, or pointing at
// one shelf below the allowed root would be refused.
func TestFolderRootAllowlistBindsTheBrowserNotTheShell(t *testing.T) {
	st := newAdminStore(t)
	allowed := t.TempDir()
	inside := filepath.Join(allowed, "shelf")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	if _, err := NewFolder(
		t.Context(), st, "Inside", inside, []string{allowed},
	); err != nil {
		t.Fatalf("a subdirectory of an allowed root was refused: %v", err)
	}
	_, err := NewFolder(t.Context(), st, "Outside", outside, []string{allowed})
	if err == nil || !strings.Contains(err.Error(), "not below any of the roots") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	// An empty allowlist is the default and means "anywhere the server
	// can read", which is what the subcommand has always permitted.
	if _, err := NewFolder(t.Context(), st, "Anywhere", outside, nil); err != nil {
		t.Fatalf("empty allowlist refused a readable root: %v", err)
	}
}

// TestFolderNameIsBoundedAndNotBlank keeps a name that has to fit a
// table cell from being either absent or unbounded.
func TestFolderNameIsBoundedAndNotBlank(t *testing.T) {
	st := newAdminStore(t)
	root := t.TempDir()
	if _, err := NewFolder(t.Context(), st, "   ", root, nil); err != ErrFolderNameEmpty {
		t.Fatalf("err = %v, want %v", err, ErrFolderNameEmpty)
	}
	long := strings.Repeat("x", MaxFolderNameLength+1)
	if _, err := NewFolder(t.Context(), st, long, root, nil); err != ErrFolderNameTooLong {
		t.Fatalf("err = %v, want %v", err, ErrFolderNameTooLong)
	}
}

// TestFolderRootIsStoredAbsolute: a relative path would resolve against
// whatever directory the server was started from, which is not the
// directory the administrator was standing in when they typed it.
func TestFolderRootIsStoredAbsolute(t *testing.T) {
	st := newAdminStore(t)
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("books", 0o700); err != nil {
		t.Fatal(err)
	}
	folder, err := NewFolder(t.Context(), st, "Books", "books", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(folder.RootPath) {
		t.Fatalf("root_path = %q, want an absolute path", folder.RootPath)
	}
}

// TestFolderUploadsIsOffUntilAskedFor: ADR-0023 narrows ADR-0017's rule
// 3 rather than repealing it, so a folder takes uploads only where
// somebody said so. The panel has a toggle; a server administered over
// SSH needs this.
func TestFolderUploadsIsOffUntilAskedFor(t *testing.T) {
	st := newAdminStore(t)
	root := t.TempDir()

	added, err := capture(t, st, "add-folder", "Books", root)
	if err != nil {
		t.Fatal(err)
	}
	id := folderIDFrom(t, added)

	folder, err := st.FolderByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if folder.AcceptsUploads {
		t.Fatal("a new folder accepted uploads without being asked")
	}

	if _, err := capture(t, st, "folder-uploads", id, "on"); err != nil {
		t.Fatal(err)
	}
	if folder, err = st.FolderByID(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if !folder.AcceptsUploads {
		t.Fatal("folder-uploads on did not take")
	}

	if _, err := capture(t, st, "folder-uploads", id, "off"); err != nil {
		t.Fatal(err)
	}
	if folder, err = st.FolderByID(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if folder.AcceptsUploads {
		t.Fatal("folder-uploads off did not take")
	}

	// A typo must not read as "off": turning a permission off by
	// accident is survivable, leaving one on because the word was not
	// understood is not.
	if _, err := capture(t, st, "folder-uploads", id, "maybe"); err == nil {
		t.Fatal("folder-uploads accepted a word it does not know")
	}
}
