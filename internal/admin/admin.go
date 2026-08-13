// Package admin implements the `liseur-sync admin` subcommands. It is
// backend-neutral (uses the configured store) and takes secrets via
// TTY/stdin, never argv.
package admin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/workident"
)

// Run dispatches an admin subcommand. args excludes "admin" itself.
// contentRoot is the configured content directory, needed by the
// commands that inspect stored bytes rather than database rows.
func Run(st store.Store, contentRoot string, args []string) error {
	if len(args) == 0 {
		return errors.New(Usage)
	}
	ctx := context.Background()
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(Usage)
		return nil
	case "create-user":
		return createUser(ctx, st, args[1:])
	case "mint-token":
		return mintToken(ctx, st, args[1:])
	case "list-tokens":
		return listTokens(ctx, st, args[1:])
	case "revoke-token":
		return revokeToken(ctx, st, args[1:])
	case "pairing-code":
		return pairingCode(ctx, st, args[1:])
	case "koplugin-device":
		return kopluginDevice(ctx, st, args[1:])
	case "create-library":
		return createLibrary(ctx, st, args[1:])
	case "list-libraries":
		return listLibraries(ctx, st, args[1:])
	case "grant-library":
		return grantLibrary(ctx, st, args[1:])
	case "revoke-library":
		return revokeLibrary(ctx, st, args[1:])
	case "backfill-works":
		return backfillWorks(ctx, st, args[1:])
	case "verify-backup":
		return verifyBackup(ctx, st, contentRoot, args[1:])
	default:
		return fmt.Errorf("unknown admin subcommand %q\n%s", args[0], Usage)
	}
}

// Usage lists every admin subcommand. It is exported so that the command
// line has one list rather than a copy that drifts: an operator who is
// told a command does not exist has no way to discover that it does.
const Usage = `usage: liseur-sync admin [-config <file>] <subcommand>

  create-user <name>            create a user (password from TTY/stdin)
  mint-token <user> <name>      create a device token
                                flags: -scope <scope>[,<scope>...]
  list-tokens <user>            list tokens for a user
  revoke-token <user> <tokenID> revoke a token
  pairing-code <user>           generate a kosync pairing code (15 min TTL)
  koplugin-device <user> <name> create a statistics-plugin capability URL

  create-library <owner> <name> create a managed library
  list-libraries <user>         list libraries the user can read
  grant-library <actor> <library-id> <user> read|manage
                                grant access; actor must own or manage it
  revoke-library <actor> <library-id> <user>
                                remove a grant

  backfill-works <user>         map every catalog book the user can
                                read to a sync work, so statistics do
                                not wait for each book to be opened

  verify-backup                 check that the database and content
                                directory named by -config are a
                                restorable pair; exits non-zero if not
`

func createUser(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: create-user <name>")
	}
	name := args[0]
	pw, err := readPassword("password for " + name + ": ")
	if err != nil {
		return err
	}
	pw2, err := readPassword("repeat password: ")
	if err != nil {
		return err
	}
	if pw != pw2 {
		return errors.New("passwords do not match")
	}
	if len(pw) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	id, err := auth.NewSecret()
	if err != nil {
		return err
	}
	u := store.User{
		ID:              id[:16],
		Name:            name,
		Argon2Hash:      hash,
		Timezone:        "UTC",
		KosyncEnabled:   true,
		KopluginEnabled: true,
		CreatedAt:       time.Now(),
	}
	if err := st.CreateUser(ctx, u); err != nil {
		return err
	}
	fmt.Printf("created user %q (id %s)\n", name, u.ID)
	return nil
}

func mintToken(ctx context.Context, st store.Store, args []string) error {
	var requested []store.Scope
	explicitScopes := false
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-scope" {
			if i+1 >= len(args) {
				return errors.New("-scope requires a value")
			}
			if !explicitScopes {
				requested = nil
				explicitScopes = true
			}
			for _, value := range strings.Split(args[i+1], ",") {
				requested = append(requested, store.Scope(strings.TrimSpace(value)))
			}
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if len(rest) != 2 {
		return errors.New("usage: mint-token [-scope <scope>[,<scope>...]] <user> <token-name>")
	}
	if !explicitScopes {
		requested = []store.Scope{store.ScopeSync}
	}
	scopes, err := store.NormalizeScopes(requested)
	if err != nil {
		return err
	}
	u, err := st.UserByName(ctx, rest[0])
	if err != nil {
		return err
	}
	svc := auth.NewService(st)
	secret, tok, err := svc.MintToken(ctx, u.ID, rest[1], scopes, nil)
	if err != nil {
		return err
	}
	fmt.Printf("token id:     %s\ndevice id:    %s\nscopes:       %s\nsecret (shown once): %s\n",
		tok.ID, tok.DeviceID, tok.Scopes, secret)
	return nil
}

func listTokens(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: list-tokens <user>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	toks, err := st.ListTokens(ctx, u.ID)
	if err != nil {
		return err
	}
	for _, t := range toks {
		state := "active"
		if t.RevokedAt != nil {
			state = "revoked " + t.RevokedAt.Format(time.RFC3339)
		}
		fmt.Printf("%s  %-20s scopes=%-30s device=%s  %s\n", t.ID, t.Name, t.Scopes, t.DeviceID, state)
	}
	return nil
}

func revokeToken(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: revoke-token <user> <token-id>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	if err := st.RevokeToken(ctx, u.ID, args[1]); err != nil {
		return err
	}
	fmt.Println("revoked")
	return nil
}

// pairingCode generates a one-time kosync pairing code: 128-bit
// entropy, hashed at rest, 15-minute TTL, atomically single-use.
func pairingCode(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: pairing-code <user>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	code, err := auth.NewSecret()
	if err != nil {
		return err
	}
	code = code[:32] // 128 bits is plenty for a 15-minute code
	id, err := auth.NewSecret()
	if err != nil {
		return err
	}
	expires := time.Now().Add(15 * time.Minute)
	if err := st.CreatePairingCode(ctx, store.PairingCode{
		ID: id, UserID: u.ID, CodeSHA256: auth.HashSecret(code), ExpiresAt: expires,
	}); err != nil {
		return err
	}
	fmt.Printf("pairing code (valid until %s, single use):\n  %s\n",
		expires.Format(time.RFC3339), code)
	fmt.Println("In KOReader kosync settings: username = device name, password = this code,")
	fmt.Println("custom server = https://<host>/adapter/kosync")
	return nil
}

// kopluginDevice mints a capability-URL credential for the statistics
// plugin. The capability is shown once.
func kopluginDevice(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: koplugin-device <user> <device-name>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	capability, err := auth.NewSecret()
	if err != nil {
		return err
	}
	id, err := auth.NewSecret()
	if err != nil {
		return err
	}
	if err := st.CreateKopluginDevice(ctx, store.KopluginDevice{
		ID: id, UserID: u.ID, TokenSHA256: auth.HashSecret(capability),
		Label: args[1], DeviceID: "koplugin:" + args[1], CreatedAt: time.Now(),
	}); err != nil {
		return err
	}
	fmt.Printf("capability (shown once): %s\n", capability)
	fmt.Printf("Configure the KOReader statistics plugin server as:\n  https://<host>/adapter/koplugin/%s\n", capability)
	return nil
}

var stdinReader = bufio.NewReader(os.Stdin)

// readPassword reads a secret from the TTY if one is available AND
// stdin is not piped; when stdin is piped (tests, scripts) it reads
// from a shared stdin reader so multiple reads in one process work.
// Never from argv.
func readPassword(prompt string) (string, error) {
	// Piped stdin wins: non-interactive use.
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
		line, err := stdinReader.ReadString('\n')
		if err != nil {
			return "", err
		}
		fmt.Fprintf(os.Stderr, "%s(read from stdin)\n", prompt)
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, prompt)
	if tty, err := os.Open("/dev/tty"); err == nil {
		defer tty.Close()
		pw, err := readNoEcho(tty)
		fmt.Fprintln(os.Stderr)
		return pw, err
	}
	return "", errors.New("no TTY and no piped stdin for password input")
}

// createLibrary makes a managed library. Managed is the only kind the
// MVP can fill: watched libraries need the folder scanner, which does
// not exist yet, so a watched library would be permanently empty.
func createLibrary(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: create-library <owner> <name>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	name := strings.TrimSpace(args[1])
	if name == "" {
		return errors.New("library name must not be blank")
	}
	lib := store.Library{
		ID:          uuid.New().String(),
		OwnerUserID: u.ID,
		// The owner pays for what the library holds, including bytes
		// uploaded by others they grant access to (ADR-0002).
		QuotaUserID: u.ID,
		Kind:        store.LibraryManaged,
		Name:        name,
		CreatedAt:   time.Now().UTC(),
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		return err
	}
	fmt.Printf("created library %q (id %s) owned by %s\n", lib.Name, lib.ID, u.Name)
	return nil
}

func listLibraries(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: list-libraries <user>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	libs, err := st.ListLibraries(ctx, u.ID, store.LibraryRoleRead)
	if err != nil {
		return err
	}
	for _, l := range libs {
		owner := "shared"
		if l.Library.OwnerUserID == u.ID {
			owner = "owner"
		}
		fmt.Printf("%s  %-10s %-6s %-7s %s\n",
			l.Library.ID, l.Library.Kind, l.Role, owner, l.Library.Name)
	}
	return nil
}

// backfillWorks exists because the book-to-work mapping is created
// lazily, on first resolve. A reader who uploads a library and then looks
// at their statistics sees an empty catalog until they have opened every
// book one by one; this maps the lot in a single pass.
func backfillWorks(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: backfill-works <user>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	report, err := workident.Backfill(ctx, st, u.ID, newWorkID, time.Now)
	// The report is printed even on failure: a run that stops halfway has
	// still committed everything it counted, and the operator needs to
	// know what was done before deciding whether to re-run it.
	fmt.Printf("books=%d created=%d linked=%d needs-confirmation=%d conflicted=%d skipped=%d\n",
		report.Books, report.Created, report.Linked,
		report.Fuzzy, report.Conflicted, report.Skipped)
	if err != nil {
		return err
	}
	if report.Fuzzy > 0 {
		fmt.Printf("%d book(s) matched an existing work on title and author alone "+
			"and were left unmapped; a reader can confirm them from a client.\n",
			report.Fuzzy)
	}
	if report.Conflicted > 0 {
		fmt.Printf("%d book(s) carry identifiers naming more than one work "+
			"and were left unmapped.\n", report.Conflicted)
	}
	return nil
}

func newWorkID() (string, error) {
	id, err := auth.NewSecret()
	if err != nil {
		return "", err
	}
	return id[:16], nil
}

// grantLibrary goes through the same ACL-checked store call the API
// uses, which is why it asks for an actor rather than acting as root:
// the operator gets one authorization path to reason about instead of
// a second one that could drift from it.
func grantLibrary(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 4 {
		return errors.New(
			"usage: grant-library <actor> <library-id> <user> read|manage")
	}
	actor, err := st.UserByName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("actor %q: %w", args[0], err)
	}
	target, err := st.UserByName(ctx, args[2])
	if err != nil {
		return fmt.Errorf("user %q: %w", args[2], err)
	}
	role := store.LibraryRole(args[3])
	if !role.Valid() {
		return fmt.Errorf("role must be read or manage, got %q", args[3])
	}
	err = st.GrantLibraryAccess(ctx, actor.ID, args[1], target.ID, role, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf(
			"no such library, or %s cannot manage it, or %s already owns it",
			actor.Name, target.Name)
	}
	if err != nil {
		return err
	}
	fmt.Printf("granted %s %s on library %s\n", target.Name, role, args[1])
	return nil
}

func revokeLibrary(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 3 {
		return errors.New("usage: revoke-library <actor> <library-id> <user>")
	}
	actor, err := st.UserByName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("actor %q: %w", args[0], err)
	}
	target, err := st.UserByName(ctx, args[2])
	if err != nil {
		return fmt.Errorf("user %q: %w", args[2], err)
	}
	err = st.RevokeLibraryAccess(ctx, actor.ID, args[1], target.ID)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no such grant, or %s cannot manage that library", actor.Name)
	}
	if err != nil {
		return err
	}
	fmt.Printf("revoked %s's access to library %s\n", target.Name, args[1])
	return nil
}
