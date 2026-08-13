package admin

import (
	"errors"
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

func libraryIDFrom(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "(id ")
	if !ok {
		t.Fatalf("no library id in %q", out)
	}
	id, _, ok := strings.Cut(rest, ")")
	if !ok {
		t.Fatalf("unterminated library id in %q", out)
	}
	return id
}

func TestCreateLibraryMakesAnUploadableLibrary(t *testing.T) {
	st := newAdminStore(t)
	owner := addUser(t, st, "ada")

	out, err := capture(t, st, "create-library", "ada", "Ada's books")
	if err != nil {
		t.Fatal(err)
	}
	id := libraryIDFrom(t, out)

	got, err := st.LibraryByID(t.Context(), owner.ID, id, store.LibraryRoleManage)
	if err != nil {
		t.Fatalf("owner cannot manage the library they were given: %v", err)
	}
	if got.Library.Kind != store.LibraryManaged {
		t.Fatalf("kind = %q, want managed: a watched library cannot be uploaded to",
			got.Library.Kind)
	}
	if got.Library.QuotaUserID != owner.ID {
		t.Fatalf("quota_user_id = %q, want the owner", got.Library.QuotaUserID)
	}
	if got.Library.RootPath != nil {
		t.Fatalf("managed library has a root path: %v", *got.Library.RootPath)
	}
	if got.Library.Name != "Ada's books" {
		t.Fatalf("name = %q", got.Library.Name)
	}
}

func TestCreateLibraryRejectsBadInput(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no such user", []string{"create-library", "nobody", "books"}},
		{"blank name", []string{"create-library", "ada", "   "}},
		{"missing name", []string{"create-library", "ada"}},
		{"extra argument", []string{"create-library", "ada", "books", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := capture(t, st, tc.args...); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestGrantLibraryLetsASecondUserUpload(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	guest := addUser(t, st, "bob")

	out, err := capture(t, st, "create-library", "ada", "shared")
	if err != nil {
		t.Fatal(err)
	}
	id := libraryIDFrom(t, out)

	if _, err := st.LibraryByID(t.Context(), guest.ID, id, store.LibraryRoleRead); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("guest saw the library before any grant: %v", err)
	}
	if _, err := capture(t, st, "grant-library", "ada", id, "bob", "manage"); err != nil {
		t.Fatal(err)
	}
	got, err := st.LibraryByID(t.Context(), guest.ID, id, store.LibraryRoleManage)
	if err != nil {
		t.Fatalf("granted user cannot manage: %v", err)
	}
	if got.Role != store.LibraryRoleManage {
		t.Fatalf("role = %q", got.Role)
	}
	if _, err := capture(t, st, "revoke-library", "ada", id, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LibraryByID(t.Context(), guest.ID, id, store.LibraryRoleRead); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("access survived revocation: %v", err)
	}
}

// TestGrantLibraryRequiresTheActorsAuthority is the reason grant-library
// takes an actor: the CLI must not become a way around the ACL that the
// API enforces.
func TestGrantLibraryRequiresTheActorsAuthority(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	addUser(t, st, "bob")
	addUser(t, st, "eve")

	out, err := capture(t, st, "create-library", "ada", "private")
	if err != nil {
		t.Fatal(err)
	}
	id := libraryIDFrom(t, out)

	// Eve neither owns nor manages it, so she cannot let herself in.
	if _, err := capture(t, st, "grant-library", "eve", id, "eve", "manage"); err == nil {
		t.Fatal("a stranger granted herself access")
	}
	if _, err := st.LibraryByID(t.Context(), "eve-id", id, store.LibraryRoleRead); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("eve has access: %v", err)
	}
	// Read access is not manage access.
	if _, err := capture(t, st, "grant-library", "ada", id, "bob", "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := capture(t, st, "grant-library", "bob", id, "eve", "read"); err == nil {
		t.Fatal("a reader granted access to someone else")
	}
	if _, err := capture(t, st, "revoke-library", "bob", id, "bob"); err == nil {
		t.Fatal("a reader revoked a grant")
	}

	// Give Bob manage, then check a stranger cannot revoke it. The actor
	// must be the one authorized, not merely *someone* authorized: if the
	// target's own authority were consulted instead, Eve could strip any
	// manager she named.
	if _, err := capture(t, st, "grant-library", "ada", id, "bob", "manage"); err != nil {
		t.Fatal(err)
	}
	if _, err := capture(t, st, "revoke-library", "eve", id, "bob"); err == nil {
		t.Fatal("a stranger revoked a manager's access")
	}
	if got, err := st.LibraryByID(t.Context(), "bob-id", id, store.LibraryRoleManage); err != nil {
		t.Fatalf("bob lost his access to a stranger: %v", err)
	} else if got.Role != store.LibraryRoleManage {
		t.Fatalf("role downgraded to %q", got.Role)
	}
}

func TestGrantLibraryRejectsBadInput(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	addUser(t, st, "bob")
	out, err := capture(t, st, "create-library", "ada", "books")
	if err != nil {
		t.Fatal(err)
	}
	id := libraryIDFrom(t, out)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown role", []string{"grant-library", "ada", id, "bob", "admin"}, "read or manage"},
		{"empty role", []string{"grant-library", "ada", id, "bob", ""}, "read or manage"},
		{"unknown actor", []string{"grant-library", "nobody", id, "bob", "read"}, ""},
		{"unknown target", []string{"grant-library", "ada", id, "nobody", "read"}, ""},
		{"unknown library", []string{"grant-library", "ada", "not-a-library", "bob", "read"}, ""},
		{"owner cannot be granted to", []string{"grant-library", "ada", id, "ada", "read"}, ""},
		{"too few arguments", []string{"grant-library", "ada", id, "bob"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := capture(t, st, tc.args...)
			if err == nil {
				t.Fatal("accepted")
			}
			// A bad role is caught at the CLI edge, so the operator is
			// told what to type instead of seeing a store error.
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not say %q", err, tc.want)
			}
		})
	}
}

func TestListLibrariesShowsOwnedAndSharedSeparately(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	addUser(t, st, "bob")

	mine, err := capture(t, st, "create-library", "ada", "ada-owned")
	if err != nil {
		t.Fatal(err)
	}
	mineID := libraryIDFrom(t, mine)
	theirs, err := capture(t, st, "create-library", "bob", "bob-owned")
	if err != nil {
		t.Fatal(err)
	}
	theirsID := libraryIDFrom(t, theirs)
	if _, err := capture(t, st, "grant-library", "bob", theirsID, "ada", "read"); err != nil {
		t.Fatal(err)
	}

	out, err := capture(t, st, "list-libraries", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, mineID) || !strings.Contains(out, "owner") {
		t.Fatalf("owned library missing from listing:\n%s", out)
	}
	if !strings.Contains(out, theirsID) || !strings.Contains(out, "shared") {
		t.Fatalf("shared library missing from listing:\n%s", out)
	}

	// Bob was never granted anything of Ada's.
	bobOut, err := capture(t, st, "list-libraries", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bobOut, mineID) {
		t.Fatalf("listing leaked another user's library:\n%s", bobOut)
	}
}

func TestUnknownSubcommandIsAnError(t *testing.T) {
	st := newAdminStore(t)
	if _, err := capture(t, st, "grant-libraries"); err == nil {
		t.Fatal("accepted a subcommand that does not exist")
	}
}
