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
	if len(cfg.TrustedProxies) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range cfg.TrustedProxies {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if n.Contains(ip) {
			return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
		}
	}
	return false
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

// RateLimitIP throttles requests per remote IP.
func RateLimitIP(rl *RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !rl.Allow(host) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
