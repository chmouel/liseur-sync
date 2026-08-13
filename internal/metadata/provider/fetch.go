// Package provider looks a book up in an external metadata service and
// returns what it found as candidates for a person to accept or ignore.
//
// Everything here is off unless an operator turns it on, and nothing in
// the ingest path may call it (ADR-0004): a scan that phoned home would
// make a library's contents visible to a third party as a side effect of
// having files on disk, which is not a trade a self-hoster agreed to.
// Lookup happens because somebody asked about one book.
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Errors a caller can act on. Everything else is reported as a fetch
// failure with the provider named, because an operator debugging a
// lookup needs to know which service was unreachable.
var (
	// ErrHostNotAllowed is returned when a request, or any redirect it
	// followed, aimed somewhere outside the allowlist.
	ErrHostNotAllowed = errors.New("provider: host is not on the allowlist")
	// ErrAddressNotAllowed is returned when a permitted name resolved to
	// an address that is not a public one.
	ErrAddressNotAllowed = errors.New("provider: address is not a public address")
	// ErrTooLarge is returned when a response exceeded the byte budget.
	// It is deliberately an error rather than a truncation: half a JSON
	// document parses into a book with half its metadata.
	ErrTooLarge = errors.New("provider: response is larger than the limit")
)

// Limits bound one lookup. Zero values are replaced with the defaults,
// so a partially filled Limits is usable rather than a fetcher that
// waits forever.
type Limits struct {
	// Timeout bounds one whole lookup including redirects.
	Timeout time.Duration
	// MaxBytes bounds one response body.
	MaxBytes int64
	// MaxRedirects bounds the redirect chain.
	MaxRedirects int
}

// Default limits: an external service is a convenience, and a lookup
// that is slow enough to notice has already failed at being one.
const (
	DefaultTimeout      = 8 * time.Second
	DefaultMaxBytes     = 4 << 20 // 4 MiB
	DefaultMaxRedirects = 3
)

func (l Limits) withDefaults() Limits {
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxRedirects <= 0 {
		l.MaxRedirects = DefaultMaxRedirects
	}
	return l
}

// Fetcher performs allowlisted HTTPS GETs. It is the only way a
// provider is given to reach the network, so every constraint below
// applies to every provider by construction rather than by each one
// remembering.
//
// Two independent checks, because each one alone has a hole:
//
//   - the allowlist is re-checked on every redirect hop, since a 302 is
//     a request to a URL this server never chose, and checking only the
//     first one means the allowlist ends at the first response;
//   - the dialer refuses any address that is not public, checked against
//     the address actually being connected to rather than the name. A
//     name on the allowlist that resolves to 169.254.169.254 is the
//     whole of the classic cloud-metadata attack, and no amount of URL
//     inspection catches it because the URL is fine.
//
// TLS is left at the Go default, which verifies against the system trust
// roots. That is load-bearing enough to have its own test: an image
// built without CA certificates fails every lookup, and it fails at the
// only moment anybody would notice, which is in production.
type Fetcher struct {
	client *http.Client
	limits Limits
	// allowed is the set of hosts, lowercased, that may be contacted.
	// It is built from the providers this build ships, never from
	// anything a request carries.
	allowed map[string]bool
	// rewrite redirects a permitted URL to a local server. It is set
	// only by this package's tests, and only after permit has run, so
	// what is exercised is the real provider composing a real URL — the
	// alternative being providers that take a base URL from outside,
	// which is the property the allowlist depends on not existing.
	rewrite func(*url.URL) *url.URL
}

func newFetcher(hosts []string, limits Limits) *Fetcher {
	limits = limits.withDefaults()
	allowed := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		allowed[strings.ToLower(host)] = true
	}
	f := &Fetcher{limits: limits, allowed: allowed}

	dialer := &net.Dialer{Timeout: limits.Timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// No proxy. HTTP_PROXY would send every lookup to a host of the
		// environment's choosing, which is the allowlist's whole job,
		// and a proxy is almost always on a private address the dialer
		// below is right to refuse. An operator who must control egress
		// has a firewall and a two-host allowlist to do it with.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if err := dialGuard(address); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, address)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   limits.Timeout,
		ExpectContinueTimeout: time.Second,
	}
	f.client = &http.Client{
		Transport: transport,
		Timeout:   limits.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= limits.MaxRedirects {
				return fmt.Errorf("provider: more than %d redirects", limits.MaxRedirects)
			}
			return f.permit(req.URL)
		},
	}
	return f
}

// permit reports whether one URL may be requested.
func (f *Fetcher) permit(target *url.URL) error {
	if target.Scheme != "https" {
		return fmt.Errorf("%w: %s is not https", ErrHostNotAllowed, target.Scheme)
	}
	if !f.allowed[strings.ToLower(target.Hostname())] {
		return fmt.Errorf("%w: %s", ErrHostNotAllowed, target.Hostname())
	}
	return nil
}

// dialGuard is the address check, indirected only so that the package's
// own tests can stand up a server on loopback and exercise the redirect
// and size limits against something real. It is unexported and never
// reassigned outside a test, so no deployment can turn it off.
var dialGuard = allowedAddress

// allowedAddress refuses to connect to anything that is not a public
// address. This is where DNS rebinding is stopped: the name was checked
// before resolution, and this is checked after it.
func allowedAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrAddressNotAllowed, address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: %s did not resolve to an address", ErrAddressNotAllowed, host)
	}
	if !publicIP(ip) {
		return fmt.Errorf("%w: %s", ErrAddressNotAllowed, ip)
	}
	return nil
}

// publicIP reports whether ip is routable on the public internet.
//
// The list is written as a refusal rather than an allowance because the
// harm is one-sided: refusing a public address that looks private breaks
// a lookup, while allowing a private one turns this server into a probe
// for whatever else is on the operator's network.
func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 100 && v4[1]&0xc0 == 64: // 100.64/10 carrier-grade NAT
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0/24 IETF protocol
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 2: // TEST-NET-1
			return false
		case v4[0] == 198 && v4[1]&0xfe == 18: // 198.18/15 benchmarking
			return false
		case v4[0] == 198 && v4[1] == 51 && v4[2] == 100: // TEST-NET-2
			return false
		case v4[0] == 203 && v4[1] == 0 && v4[2] == 113: // TEST-NET-3
			return false
		case v4[0] >= 240: // 240/4 reserved, includes broadcast
			return false
		}
		return true
	}
	// IsPrivate already covers unique-local fc00::/7. NAT64 does not
	// look private and wraps whatever v4 address was embedded in it.
	if strings.HasPrefix(ip.String(), "64:ff9b:") {
		return false
	}
	return true
}

// Get fetches one URL and returns its body, bounded. A 404 is reported
// as no body and no error: a service that has never heard of a book has
// answered the question.
//
// It is a GET of a URL this package composed, never one a request
// supplied: a caller passes a query, and the provider builds the URL
// around it. That is what makes the allowlist meaningful — there is no
// path by which a user names a host.
func (f *Fetcher) Get(ctx context.Context, target *url.URL) ([]byte, error) {
	if err := f.permit(target); err != nil {
		return nil, err
	}
	if f.rewrite != nil {
		target = f.rewrite(target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// Identifying the client is a courtesy to services that are free and
	// run on donations, and it is what lets them ask us to stop rather
	// than block us.
	req.Header.Set("User-Agent", UserAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // a book the service does not know is not an error
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider: %s answered %d", target.Host, resp.StatusCode)
	}

	// One byte past the limit, so a body that is exactly at it is
	// accepted and one that is over it is refused rather than truncated
	// into a document that parses as a book with pieces missing.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.limits.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > f.limits.MaxBytes {
		return nil, fmt.Errorf("%w: %s sent more than %d bytes",
			ErrTooLarge, target.Host, f.limits.MaxBytes)
	}
	return body, nil
}

// UserAgent identifies this server to the services it queries.
const UserAgent = "liseur-sync (+https://github.com/chmouel/liseur-sync)"
