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
)

// Run dispatches an admin subcommand. args excludes "admin" itself.
func Run(st store.Store, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	ctx := context.Background()
	switch args[0] {
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
	default:
		return fmt.Errorf("unknown admin subcommand %q\n%s", args[0], usage)
	}
}

const usage = `usage: liseur-sync admin <subcommand>

  create-user <name>            create a user (password from TTY/stdin)
  mint-token <user> <name>      create a device token
                                flags: -scope sync|read-insights|admin
  list-tokens <user>            list tokens for a user
  revoke-token <user> <tokenID> revoke a token
  pairing-code <user>           generate a kosync pairing code (15 min TTL)
  koplugin-device <user> <name> create a statistics-plugin capability URL`

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
	scope := store.ScopeSync
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-scope" && i+1 < len(args) {
			scope = store.Scope(args[i+1])
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if len(rest) != 2 {
		return errors.New("usage: mint-token [-scope sync|read-insights|admin] <user> <token-name>")
	}
	switch scope {
	case store.ScopeSync, store.ScopeReadInsights, store.ScopeAdmin:
	default:
		return fmt.Errorf("invalid scope %q", scope)
	}
	u, err := st.UserByName(ctx, rest[0])
	if err != nil {
		return err
	}
	svc := auth.NewService(st)
	secret, tok, err := svc.MintToken(ctx, u.ID, rest[1], scope, nil)
	if err != nil {
		return err
	}
	fmt.Printf("token id:     %s\ndevice id:    %s\nscope:        %s\nsecret (shown once): %s\n",
		tok.ID, tok.DeviceID, tok.Scope, secret)
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
		fmt.Printf("%s  %-20s scope=%-13s device=%s  %s\n", t.ID, t.Name, t.Scope, t.DeviceID, state)
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
