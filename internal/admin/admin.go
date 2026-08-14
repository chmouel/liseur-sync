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


	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/workident"
)

// Run dispatches an admin subcommand. args excludes "admin" itself.
// contentRoot is the configured content directory, needed by the
// commands that inspect stored bytes rather than database rows.
func Run(st store.Store, contentRoot string, args []string) error {
	if len(args) == 0 {
		return UsageError{ExitCode: 1}
	}
	ctx := context.Background()
	switch args[0] {
	case "help", "-h", "--help":
		return UsageError{ExitCode: 0}
	case "create-user":
		return createUser(ctx, st, args[1:])
	case "reset-password":
		return resetPassword(ctx, st, args[1:])
	case "revoke-credentials":
		return revokeCredentials(ctx, st, args[1:])
	case "mint-token":
		return mintToken(ctx, st, args[1:])
	case "grant-admin":
		return setAdminCmd(ctx, st, args[1:], true)
	case "revoke-admin":
		return setAdminCmd(ctx, st, args[1:], false)
	case "disable-user":
		return setDisabledCmd(ctx, st, args[1:], true)
	case "enable-user":
		return setDisabledCmd(ctx, st, args[1:], false)
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
	case "watch-library":
		return watchLibrary(ctx, st, args[1:])
	case "list-review":
		return listReview(ctx, st, args[1:])
	case "clear-review":
		return clearReview(ctx, st, args[1:])
	case "list-libraries":
		return listLibraries(ctx, st, args[1:])
	case "grant-library":
		return grantLibrary(ctx, st, args[1:])
	case "library-layout":
		return libraryLayout(ctx, st, args[1:])
	case "revoke-library":
		return revokeLibrary(ctx, st, args[1:])
	case "backfill-works":
		return backfillWorks(ctx, st, args[1:])
	case "verify-backup":
		return verifyBackup(ctx, st, contentRoot, args[1:])
	default:
		return UsageError{
			Message:  fmt.Sprintf("unknown admin subcommand %q", args[0]),
			ExitCode: 1,
		}
	}
}

// UsageError marks an admin usage path so the command line can print it
// directly instead of logging it as an operational failure.
type UsageError struct {
	Message  string
	ExitCode int
}

func (e UsageError) Error() string {
	if e.Message == "" {
		return Usage
	}
	return e.Message + "\n" + Usage
}

// Usage lists every admin subcommand. It is exported so that the command
// line has one list rather than a copy that drifts: an operator who is
// told a command does not exist has no way to discover that it does.
const Usage = `usage: liseur-sync admin [-config <file>] <subcommand>

  create-user <name>            create a user (password from TTY/stdin)
  reset-password <user>         set a new password (from TTY/stdin) and
                                revoke the account's web and login
                                sessions; devices keep working
  revoke-credentials <user>     revoke every credential the account
                                holds: tokens, sessions, kosync slots,
                                koplugin devices and unused pairing codes
  grant-admin <user>            make a user an administrator
  revoke-admin <user>           take administrator rights away; refuses
                                to remove the last enabled admin
  disable-user <user>           stop an account: every credential it
                                holds is refused and its sessions are
                                revoked; nothing is deleted
  enable-user <user>            start it again; tokens and devices
                                resume working, sessions do not
  mint-token <user> <name>      create a device token
                                flags: -scope <scope>[,<scope>...]
  list-tokens <user>            list tokens for a user
  revoke-token <user> <tokenID> revoke a token
  pairing-code <user>           generate a kosync pairing code (15 min TTL)
  koplugin-device <user> <name> create a statistics-plugin capability URL

  create-library <owner> <name> create a managed library
  watch-library <owner> <name> <root>
                                create a watched library over an existing
                                read-only directory; the server never
                                writes below <root>
  list-libraries <user>         list libraries the user can read
  grant-library <actor> <library-id> <user> read|manage
                                grant access; actor must own or manage it
  revoke-library <actor> <library-id> <user>
                                remove a grant
  library-layout <actor> <library-id> [<layouts>]
                                show or set how a library's filenames are
                                read; <layouts> is a comma-separated list,
                                "default", or "none"
  list-review <actor> <library-id>
                                list watched books whose source file
                                changed, which the server will not
                                reinterpret on its own
  clear-review <actor> <library-id> <book-id>
                                accept the copy being served and take one
                                book back out of review

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
	pw, err := readPasswordTwice("password for " + name + ": ")
	if err != nil {
		return err
	}
	u, err := CreateUser(ctx, st, name, pw)
	if err != nil {
		return err
	}
	fmt.Printf("created user %q (id %s)\n", u.Name, u.ID)
	// A brand-new instance has nobody who can reach the admin panel,
	// and the operator who just made the only account is the one person
	// who wants to know that. (The web UI's first-run page grants the
	// flag by itself; this path is the shell one, which does not.)
	if counts, err := st.AdminCounts(ctx); err == nil && counts.AdminUsers == 0 {
		fmt.Printf("this instance has no administrator yet; run:\n"+
			"  liseur-sync admin grant-admin %s\n", u.Name)
	}
	return nil
}

// readPasswordTwice prompts and confirms. Both the CLI's create-user
// and its reset-password use it, so a typo costs one retry rather than
// an account nobody can sign in to.
func readPasswordTwice(prompt string) (string, error) {
	pw, err := readPassword(prompt)
	if err != nil {
		return "", err
	}
	pw2, err := readPassword("repeat password: ")
	if err != nil {
		return "", err
	}
	if err := ValidatePassword(pw, pw2); err != nil {
		return "", err
	}
	return pw, nil
}

func resetPassword(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: reset-password <user>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	pw, err := readPasswordTwice("new password for " + u.Name + ": ")
	if err != nil {
		return err
	}
	// No session is spared: an operator resetting somebody else's
	// password is not signed in as them.
	if err := SetPassword(ctx, st, u.ID, pw, ""); err != nil {
		return err
	}
	fmt.Printf("password changed for %q (id %s); web and login sessions revoked\n",
		u.Name, u.ID)
	return nil
}

func revokeCredentials(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: revoke-credentials <user>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	if err := RevokeAllCredentials(ctx, st, u.ID); err != nil {
		return err
	}
	fmt.Printf("revoked every credential for %q (id %s): tokens, sessions, "+
		"kosync slots, koplugin devices and unused pairing codes\n", u.Name, u.ID)
	return nil
}

// SetAdmin grants or removes administrator rights on an account. It is
// exported because the CLI and (from ADR-0013 phase 3) the web panel
// must be one implementation: the guard against removing the last
// enabled administrator lives in the store, and the message an operator
// reads about it lives here.
func SetAdmin(ctx context.Context, st store.Store, name string, admin bool) (store.User, error) {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return u, err
	}
	if err := st.SetUserAdmin(ctx, u.ID, admin); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			return u, fmt.Errorf(
				"%q is the last enabled administrator: grant admin to somebody else first", name)
		}
		return u, err
	}
	u.IsAdmin = admin
	return u, nil
}

func setAdminCmd(ctx context.Context, st store.Store, args []string, admin bool) error {
	verb := "grant-admin"
	if !admin {
		verb = "revoke-admin"
	}
	if len(args) != 1 {
		return errors.New("usage: " + verb + " <user>")
	}
	u, err := SetAdmin(ctx, st, args[0], admin)
	if err != nil {
		return err
	}
	if admin {
		fmt.Printf("%q (id %s) is now an administrator\n", u.Name, u.ID)
		return nil
	}
	fmt.Printf("%q (id %s) is no longer an administrator; admin-scoped tokens revoked\n",
		u.Name, u.ID)
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
		if errors.Is(err, store.ErrAdminGrantRequiresAdmin) {
			return fmt.Errorf(
				"the admin scope belongs to an admin account: run `liseur-sync admin grant-admin %s` first",
				u.Name)
		}
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

// createLibrary makes a managed library, whose content the server owns.
// A library over a directory the server must not write to is a different
// command, because the two differ in what an administrator is promising
// rather than in a flag.
func createLibrary(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: create-library <owner> <name>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	lib, err := NewManagedLibrary(ctx, st, u.ID, args[1])
	if err != nil {
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

// SetDisabled stops or restarts an account. Like SetAdmin it exists so
// that the CLI and the panel are one implementation, and so that the
// last-enabled-administrator refusal reads the same on both.
func SetDisabled(ctx context.Context, st store.Store, name string, disabled bool) (store.User, error) {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return u, err
	}
	if err := st.SetUserDisabled(ctx, u.ID, disabled, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			return u, fmt.Errorf(
				"%q is the last enabled administrator: make somebody else an admin first", name)
		}
		return u, err
	}
	return u, nil
}

func setDisabledCmd(ctx context.Context, st store.Store, args []string, disabled bool) error {
	verb := "disable-user"
	if !disabled {
		verb = "enable-user"
	}
	if len(args) != 1 {
		return errors.New("usage: " + verb + " <user>")
	}
	u, err := SetDisabled(ctx, st, args[0], disabled)
	if err != nil {
		return err
	}
	if disabled {
		fmt.Printf("%q (id %s) is disabled: its credentials are refused and its "+
			"sessions were revoked. Nothing was deleted.\n", u.Name, u.ID)
		return nil
	}
	fmt.Printf("%q (id %s) is enabled again. Tokens and devices work; "+
		"web sessions do not, so they sign in again.\n", u.Name, u.ID)
	return nil
}
