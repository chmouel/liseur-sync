package auth

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
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
	h1 := HashSecret(a)
	h2 := HashSecret(a)
	if h1 != h2 {
		t.Fatal("hash not deterministic")
	}
}

func TestScopeAllowed(t *testing.T) {
	if !scopeAllowed(store.ScopeSet{store.ScopeAdmin}, []store.Scope{store.ScopeSync}) {
		t.Fatal("admin should imply sync")
	}
	if scopeAllowed(store.ScopeSet{store.ScopeSync}, []store.Scope{store.ScopeAdmin}) {
		t.Fatal("sync must not imply admin")
	}
	if scopeAllowed(store.ScopeSet{store.ScopeSync}, []store.Scope{store.ScopeReadInsights}) {
		t.Fatal("sync must not read insights")
	}
	if !scopeAllowed(store.ScopeSet{store.ScopeReadInsights}, []store.Scope{store.ScopeReadInsights}) {
		t.Fatal("exact scope should pass")
	}
	if !scopeAllowed(store.ScopeSet{store.ScopeAdmin}, []store.Scope{store.ScopeLibraryRead}) {
		t.Fatal("admin should imply library-read")
	}
	if scopeAllowed(store.ScopeSet{store.ScopeLibraryRead}, []store.Scope{store.ScopeAdmin}) {
		t.Fatal("library-read must not imply admin")
	}
	multi := store.ScopeSet{store.ScopeSync, store.ScopeLibraryRead}
	if !scopeAllowed(multi, []store.Scope{store.ScopeSync}) ||
		!scopeAllowed(multi, []store.Scope{store.ScopeLibraryRead}) {
		t.Fatal("multi-scope token should grant every explicit scope")
	}
	// The catalog resolve route names two scopes, and it means both.
	both := []store.Scope{store.ScopeLibraryRead, store.ScopeSync}
	if !scopeAllowed(multi, both) {
		t.Fatal("token holding both scopes should pass a two-scope route")
	}
	if scopeAllowed(store.ScopeSet{store.ScopeLibraryRead}, both) {
		t.Fatal("catalog-only token must not resolve a book to a work")
	}
	if scopeAllowed(store.ScopeSet{store.ScopeSync}, both) {
		t.Fatal("sync-only token must not read the catalog")
	}
	if !scopeAllowed(store.ScopeSet{store.ScopeAdmin}, both) {
		t.Fatal("admin should imply every scope in a set")
	}
	if !scopeAllowed(multi, nil) {
		t.Fatal("a route naming no scope requires only authentication")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(2, 60_000_000_000)
	if !rl.Allow("ip1") {
		t.Fatal("first should pass")
	}
	if !rl.Allow("ip1") {
		t.Fatal("second should pass")
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
