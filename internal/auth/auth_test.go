package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
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

// TestProductionPasswordParamsPinned guards the parameters that
// protect stored passwords. Test binaries hash at a reduced cost
// (issue #31) so that suites which need an account per fixture do not
// spend their run inside argon2id; nothing else may lower it, and the
// production baseline stays where OWASP puts it.
func TestProductionPasswordParamsPinned(t *testing.T) {
	want := argon2Params{time: 3, memory: 64 * 1024, threads: 2}
	if productionParams != want {
		t.Fatalf("production argon2id cost changed: got %+v want %+v", productionParams, want)
	}
	if argonKeyLen != 32 || saltLen != 16 {
		t.Fatalf("key/salt length changed: key=%d salt=%d", argonKeyLen, saltLen)
	}
	if got := hashingParams(); got != testParams {
		t.Fatalf("a test binary must hash at the reduced cost, got %+v", got)
	}

	// A hash written at the production cost still round-trips: the
	// parameters travel in the encoding, so the reduced test cost can
	// never invalidate a password a real deployment stored.
	encoded, err := hashPasswordWith(productionParams, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "$m=65536,t=3,p=2$") {
		t.Fatalf("production encoding lost its parameters: %s", encoded)
	}
	ok, err := CheckPassword("correct horse battery staple", encoded)
	if err != nil || !ok {
		t.Fatalf("want match, got ok=%v err=%v", ok, err)
	}

	// The dummy burn must cost what a real check on this process costs,
	// or an unknown username becomes distinguishable by the clock.
	if !strings.Contains(dummyHashForParams(), fmt.Sprintf("$m=%d,t=%d,p=%d$", testParams.memory, testParams.time, testParams.threads)) {
		t.Fatalf("dummy hash does not track the hashing cost: %s", dummyHashForParams())
	}
	if _, err := CheckPassword("dummy", dummyHashForParams()); err != nil {
		t.Fatalf("dummy hash is not verifiable: %v", err)
	}
	if _, err := CheckPassword("dummy", dummyHash); err != nil {
		t.Fatalf("production dummy hash is not verifiable: %v", err)
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
