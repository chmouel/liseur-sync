package auth

import (
	"container/list"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
)

// IsSecure reports whether the request counts as HTTPS. Direct TLS is
// secure. Behind a reverse proxy, X-Forwarded-Proto is honoured only
// when the immediate peer is in the configured trusted_proxies CIDRs —
// arbitrary forwarded headers from untrusted peers are never trusted.
func IsSecure(r *http.Request, cfg config.Config) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if trustedProxy(ip, cfg) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

// trustedProxy reports whether ip falls inside one of the configured
// trusted_proxies CIDRs.
func trustedProxy(ip net.IP, cfg config.Config) bool {
	for _, cidr := range cfg.TrustedProxies {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP returns the address per-IP rate limits key on. Direct
// connections key on the peer itself. When the immediate peer is a
// trusted proxy, X-Forwarded-For is walked right to left, skipping
// further trusted-proxy hops, and the first untrusted address is the
// client — otherwise every visitor behind the proxy would share the
// proxy's one bucket, and a stranger probing the login could exhaust
// the real user's budget. The same trust rule as X-Forwarded-Proto in
// IsSecure applies: a forwarded header from an untrusted peer is never
// honoured, so a direct client cannot spoof its way into fresh buckets.
// Any unparseable hop falls back to the peer address rather than
// trusting the rest of a header an attacker may have shaped.
func ClientIP(r *http.Request, cfg config.Config) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || !trustedProxy(peer, cfg) {
		return host
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hops = append(hops, h)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(hops[i])
		if ip == nil {
			return host
		}
		if !trustedProxy(ip, cfg) {
			return ip.String()
		}
	}
	return host
}

// RequireSecureTransport rejects credential-bearing requests over plain
// HTTP unless insecure_http is configured. Wraps login, bearer, and
// adapter credential routes.
func RequireSecureTransport(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.InsecureHTTP && !IsSecure(r, cfg) {
			http.Error(w, `{"error":"https required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimiter is a per-key fixed-window limiter. In-memory and
// per-process by design (v1 is single-replica).
type RateLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	buckets   map[string]*bucket
	nextSweep time.Time
	recent    list.List
}

// Evict the least recently used key at capacity. A full table must not
// prevent previously unseen clients from attempting authentication.
const maxRateLimitBuckets = 10_000

type bucket struct {
	count   int
	resetAt time.Time
	entry   *list.Element
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, buckets: map[string]*bucket{}}
}

// Allow reports whether key may proceed, consuming one unit.
func (rl *RateLimiter) Allow(key string) bool {
	return rl.allowAt(key, time.Now())
}

func (rl *RateLimiter) allowAt(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if !now.Before(rl.nextSweep) {
		for k, b := range rl.buckets {
			if !now.Before(b.resetAt) {
				delete(rl.buckets, k)
				rl.recent.Remove(b.entry)
			}
		}
		rl.nextSweep = now.Add(rl.window)
	}
	b, ok := rl.buckets[key]
	if !ok {
		if len(rl.buckets) >= maxRateLimitBuckets {
			oldest := rl.recent.Back()
			delete(rl.buckets, oldest.Value.(string))
			rl.recent.Remove(oldest)
		}
		b = &bucket{entry: rl.recent.PushFront(key)}
		rl.buckets[key] = b
	} else {
		rl.recent.MoveToFront(b.entry)
	}
	if !ok || !now.Before(b.resetAt) {
		b.count, b.resetAt = 1, now.Add(rl.window)
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

// All anonymous password routes share this process-wide budget. Two
// concurrent Argon2 operations use 128 MiB at the production parameters.
var passwordRequests = make(chan struct{}, 2)

// PasswordBusyRetryAfter is the retry delay in seconds for a full password budget.
const PasswordBusyRetryAfter = "1"

// BeginPasswordRequest reserves capacity after the handler has parsed its
// body, so a slow sender cannot hold a password slot. Nil means busy;
// callers render the appropriate API or HTML response. Defer a non-nil
// release, and reserve before consuming an invite or creating an account.
func BeginPasswordRequest() (release func()) {
	select {
	case passwordRequests <- struct{}{}:
		return func() { <-passwordRequests }
	default:
		return nil
	}
}

// ClientRateKey groups IPv6 clients by /64 so rotating interface addresses
// does not provide fresh budgets or churn the table. Logs still use ClientIP.
func ClientRateKey(r *http.Request, cfg config.Config) string {
	key := ClientIP(r, cfg)
	ip, err := netip.ParseAddr(key)
	if err != nil {
		return key
	}
	ip = ip.Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String()
	}
	return ip.String()
}

// RateLimitIP throttles requests per client IP, as ClientIP resolves
// it — behind a trusted proxy that is the forwarded client, not the
// proxy.
func RateLimitIP(rl *RateLimiter, cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(ClientRateKey(r, cfg)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
