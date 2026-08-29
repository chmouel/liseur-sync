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

// HashPassword hashes a password with argon2id, returning the standard
// encoded form: $argon2id$v=19$m=...,t=...,p=...$salt$hash
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
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
