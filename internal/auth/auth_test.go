package auth

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := CheckPassword("correct horse battery staple", h)
	if err != nil || !ok {
		t.Fatalf("want match, got ok=%v err=%v", ok, err)
	}
	ok, err = CheckPassword("wrong", h)
	if err != nil || ok {
		t.Fatalf("want no match, got ok=%v err=%v", ok, err)
	}
	if _, err := CheckPassword("x", "not-a-hash"); err == nil {
		t.Fatal("want error on malformed hash")
	}
}

func TestNewSecretUnique(t *testing.T) {
	a, _ := NewSecret()
	b, _ := NewSecret()
	if a == b || len(a) != 64 {
		t.Fatalf("bad secrets: %q %q", a, b)
	}
	// HashSecret is deterministic.
	if HashSecret(a) != HashSecret(a) {
		t.Fatal("hash not deterministic")
	}
}

func TestScopeAllowed(t *testing.T) {
	if !scopeAllowed("admin", "sync") {
		t.Fatal("admin should imply sync")
	}
	if scopeAllowed("sync", "admin") {
		t.Fatal("sync must not imply admin")
	}
	if scopeAllowed("sync", "read-insights") {
		t.Fatal("sync must not read insights")
	}
	if !scopeAllowed("read-insights", "read-insights") {
		t.Fatal("exact scope should pass")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(2, 60_000_000_000)
	if !rl.Allow("ip1") || !rl.Allow("ip1") {
		t.Fatal("first two should pass")
	}
	if rl.Allow("ip1") {
		t.Fatal("third should be limited")
	}
	if !rl.Allow("ip2") {
		t.Fatal("other key unaffected")
	}
}

func TestSecretEntropy(t *testing.T) {
	// 256-bit secrets must never collide in practice; smoke-check 1k.
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		s, err := NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatal("collision")
		}
		seen[s] = true
	}
	_ = rand.Reader
	_ = hex.EncodeToString
}
