package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
)

func xffRequest(remoteAddr string, xff ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/login", nil)
	r.RemoteAddr = remoteAddr
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func proxiedCfg(cidrs ...string) config.Config {
	cfg := config.Default()
	cfg.TrustedProxies = cidrs
	return cfg
}

// Behind a reverse proxy every connection arrives from the proxy's
// address. If the limiter keyed on that, all clients would share one
// bucket and a stranger probing the login could exhaust the real
// user's budget: the forwarded client must be the key.
func TestClientIPSeparatesClientsBehindATrustedProxy(t *testing.T) {
	cfg := proxiedCfg("172.29.0.0/16")
	a := ClientIP(xffRequest("172.29.0.1:4242", "203.0.113.7"), cfg)
	b := ClientIP(xffRequest("172.29.0.1:4242", "198.51.100.9"), cfg)
	if a != "203.0.113.7" || b != "198.51.100.9" {
		t.Fatalf("forwarded clients not separated: %q vs %q", a, b)
	}
	if a == b {
		t.Fatal("two clients behind one proxy share a rate-limit key")
	}
}

// A direct client controls its own X-Forwarded-For entirely. Honouring
// it would hand every attacker unlimited fresh buckets, so the header
// from an untrusted peer must be ignored — same trust rule as
// X-Forwarded-Proto in IsSecure.
func TestClientIPIgnoresSpoofedForwardedFor(t *testing.T) {
	cfg := proxiedCfg("172.29.0.0/16")
	got := ClientIP(xffRequest("203.0.113.7:9999", "10.0.0.1"), cfg)
	if got != "203.0.113.7" {
		t.Fatalf("spoofed XFF from an untrusted peer was honoured: %q", got)
	}
	// No trusted proxies configured at all: same answer.
	got = ClientIP(xffRequest("203.0.113.7:9999", "10.0.0.1"), config.Default())
	if got != "203.0.113.7" {
		t.Fatalf("XFF honoured with no trusted proxies: %q", got)
	}
}

// The proxy appends the peer it saw, so with chained trusted proxies
// the rightmost untrusted hop is the client; hops the client made up
// to its left change nothing.
func TestClientIPWalksPastTrustedHops(t *testing.T) {
	cfg := proxiedCfg("172.29.0.0/16", "10.0.0.0/8")
	got := ClientIP(
		xffRequest("172.29.0.1:4242", "6.6.6.6, 203.0.113.7, 10.1.2.3"), cfg)
	if got != "203.0.113.7" {
		t.Fatalf("first untrusted hop from the right should win, got %q", got)
	}
	// Split across repeated headers, same list.
	got = ClientIP(
		xffRequest("172.29.0.1:4242", "6.6.6.6", "203.0.113.7", "10.1.2.3"), cfg)
	if got != "203.0.113.7" {
		t.Fatalf("repeated XFF headers not joined, got %q", got)
	}
}

// Garbage in the header means an attacker shaped it; fall back to the
// proxy address rather than trusting whatever else it says. An empty
// or all-trusted list likewise keys on the proxy.
func TestClientIPFallsBackToPeerOnBadOrEmptyHeader(t *testing.T) {
	cfg := proxiedCfg("172.29.0.0/16")
	for name, r := range map[string]*http.Request{
		"absent":      xffRequest("172.29.0.1:4242"),
		"unparseable": xffRequest("172.29.0.1:4242", "not-an-ip"),
		"all-trusted": xffRequest("172.29.0.1:4242", "172.29.0.9"),
	} {
		if got := ClientIP(r, cfg); got != "172.29.0.1" {
			t.Errorf("%s: want proxy address fallback, got %q", name, got)
		}
	}
}

// End-to-end through the middleware: exhausting one forwarded client's
// budget must not lock out another behind the same proxy.
func TestRateLimitIPKeysOnForwardedClient(t *testing.T) {
	cfg := proxiedCfg("172.29.0.0/16")
	rl := NewRateLimiter(1, time.Minute)
	h := RateLimitIP(rl, cfg, http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	do := func(xff string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, xffRequest("172.29.0.1:4242", xff))
		return w.Code
	}
	if do("203.0.113.7") != http.StatusOK {
		t.Fatal("first request should pass")
	}
	if do("203.0.113.7") != http.StatusTooManyRequests {
		t.Fatal("second request from the same client should be limited")
	}
	if do("198.51.100.9") != http.StatusOK {
		t.Fatal("a different client behind the same proxy was locked out")
	}
}
