// Package auth implements account authentication (argon2id), per-device
// API tokens, short-lived auth credentials, and the fail-closed
// middleware chain.
package auth

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP-recommended baseline).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

// argon2Params is the cost a new hash is written with. The values a
// stored hash was written with travel inside its encoding, so this
// choice only ever affects hashes this process creates: a password
// hashed by a real deployment still verifies at the cost that
// deployment used.
type argon2Params struct {
	time    uint32
	memory  uint32
	threads uint8
}

var productionParams = argon2Params{time: argonTime, memory: argonMemory, threads: argonThreads}

// testParams is the cost used when the binary was built by `go test`.
// Argon2id is deliberately slow, and almost every test in this
// repository needs an account, so at the production cost the suites
// spend most of their wall clock inside the one function they are not
// trying to measure (issue #31). The reduced cost exercises the same
// code path — same algorithm, same encoding, same verify — at a price
// that does not dominate a run. It is unreachable outside a test
// binary, and productionParams is pinned by a test.
var testParams = argon2Params{time: 1, memory: 8 * 1024, threads: 1}

var hashingParams = sync.OnceValue(func() argon2Params {
	if testing.Testing() {
		return testParams
	}
	return productionParams
})

// HashPassword hashes a password with argon2id, returning the standard
// encoded form: $argon2id$v=19$m=...,t=...,p=...$salt$hash
func HashPassword(password string) (string, error) {
	return hashPasswordWith(hashingParams(), password)
}

func hashPasswordWith(p argon2Params, password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword verifies a password against an encoded argon2id hash.
// Constant-time on the final compare. A malformed stored hash returns
// an error, never a match.
func CheckPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("auth: malformed argon2id hash")
	}
	var mem, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return false, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, time, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is a valid argon2id encoding used to equalize timing when
// the username does not exist. Password "dummy".
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$M2DdlP2yhB+CZCm2lp3DKbT8NYDMv0hWQRdnJP0bLcU"

// dummyHashForParams is the encoding CheckDummyPassword actually
// verifies against. It must carry the same cost as a real hash this
// process would write, otherwise the burn is no longer equal to the
// work it stands in for. In production that is dummyHash verbatim;
// under `go test`, where new hashes are cheap, the same salt and digest
// are re-encoded at the reduced cost. The compare fails either way —
// what matters is that both paths run one argon2id derivation.
var dummyHashForParams = sync.OnceValue(func() string {
	p := hashingParams()
	if p == productionParams {
		return dummyHash
	}
	rest := strings.SplitN(dummyHash, "$", 5)[4]
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s", argon2.Version, p.memory, p.time, p.threads, rest)
})

// CheckDummyPassword burns the same work a real password check would,
// so an unknown username and a wrong password take comparable time.
// Argon2id at 64 MiB is slow enough that skipping it is a plainly
// measurable signal, which turns any login form into a user
// enumeration oracle. Every path that verifies a password must call
// this when the user is not found.
func CheckDummyPassword(password string) {
	_, _ = CheckPassword(password, dummyHashForParams())
}

// NewSecret generates a random 256-bit secret, hex-encoded (64 chars).
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashSecret returns the SHA-256 hex of a secret. This is what is
// stored for tokens, auth sessions, pairing codes, invites, and device
// credentials — never the secret itself.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// KosyncUserKey derives the credential a kosync client actually sends
// from the pairing code a human types. Every kosync client hashes the
// password locally and transmits only the digest: KOReader's plugin
// does `local userkey = md5(password)` before calling users/create, and
// Readest's KOSyncClient does the same. That digest is then presented
// verbatim as x-auth-key on every later request.
//
// So the plaintext pairing code never reaches this server, and the
// value a redemption can be compared against is the hash of this
// derived key, not of the code itself. The protocol's use of MD5 is not
// ours to change (design §7.3); containment is that the code is 128
// bits of entropy, single-use, short-lived, and binds one revocable
// device slot rather than the account (design §8.3).
func KosyncUserKey(pairingCode string) string {
	sum := md5.Sum([]byte(pairingCode))
	return hex.EncodeToString(sum[:])
}

// KosyncPairingHash is what a pairing code is stored under: the hash of
// the MD5-derived key the client will send, so users/create can match
// what it actually receives.
func KosyncPairingHash(pairingCode string) string {
	return HashSecret(KosyncUserKey(pairingCode))
}
