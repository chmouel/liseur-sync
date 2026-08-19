package auth

import (
	"net"
	"net/http"
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
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*bucket
}

type bucket struct {
	count   int
	resetAt time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, buckets: map[string]*bucket{}}
}

// Allow reports whether key may proceed, consuming one unit.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok || now.After(b.resetAt) {
		rl.buckets[key] = &bucket{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

// RateLimitIP throttles requests per client IP, as ClientIP resolves
// it — behind a trusted proxy that is the forwarded client, not the
// proxy.
func RateLimitIP(rl *RateLimiter, cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(ClientIP(r, cfg)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
