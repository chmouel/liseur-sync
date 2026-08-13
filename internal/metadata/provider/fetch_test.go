package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestOnlyPublicAddressesAreDialed is the check that survives DNS.
//
// The allowlist is a check on a name, and a name is not an address: a
// host on the allowlist whose DNS answers 169.254.169.254 turns this
// server into a reader of the operator's cloud credentials, and no
// amount of URL inspection notices, because the URL is correct. So the
// address is checked at the moment of connecting to it.
func TestOnlyPublicAddressesAreDialed(t *testing.T) {
	refused := []string{
		"127.0.0.1:443",              // loopback
		"[::1]:443",                  // loopback, v6
		"10.1.2.3:443",               // private
		"192.168.1.1:443",            // private
		"172.16.0.1:443",             // private
		"169.254.169.254:80",         // the cloud metadata service
		"[fe80::1]:443",              // link-local
		"[fd00::1]:443",              // unique local
		"0.0.0.0:443",                // unspecified
		"100.64.0.1:443",             // carrier-grade NAT
		"[64:ff9b::a00:1]:443",       // NAT64 wrapping 10.0.0.1
		"255.255.255.255:443",        // broadcast
		"224.0.0.1:443",              // multicast
		"192.0.0.1:443",              // IETF protocol assignments
		"198.18.0.1:443",             // benchmarking
		"[::ffff:127.0.0.1]:443",     // loopback behind a v6 wrapper
		"[::ffff:169.254.169.254]:1", // metadata behind a v6 wrapper
	}
	for _, address := range refused {
		if err := allowedAddress(address); !errors.Is(err, ErrAddressNotAllowed) {
			t.Errorf("%s was allowed: %v", address, err)
		}
	}

	for _, address := range []string{"93.184.216.34:443", "[2606:2800:220:1:248:1893:25c8:1946]:443"} {
		if err := allowedAddress(address); err != nil {
			t.Errorf("%s is a public address and was refused: %v", address, err)
		}
	}

	// A name that never resolved is refused rather than passed through,
	// because a dialer that cannot say what it is connecting to is a
	// dialer that has not been checked.
	if err := allowedAddress("openlibrary.org:443"); !errors.Is(err, ErrAddressNotAllowed) {
		t.Error("an unresolved name must not be dialed")
	}
	if err := allowedAddress("nonsense"); !errors.Is(err, ErrAddressNotAllowed) {
		t.Error("an unparseable address must not be dialed")
	}
}

// TestOnlyAllowlistedHostsAreRequested covers the other half: the name.
func TestOnlyAllowlistedHostsAreRequested(t *testing.T) {
	f := newFetcher([]string{"openlibrary.org"}, Limits{})
	for _, raw := range []string{
		"https://evil.example/",         // not on the list
		"http://openlibrary.org/",       // on the list, wrong scheme
		"https://OPENLIBRARY.ORG.evil/", // a suffix, not the host
		"https://sub.openlibrary.org/",  // a subdomain is a different host
		"file:///etc/passwd",            // not even a network scheme
	} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.permit(target); !errors.Is(err, ErrHostNotAllowed) {
			t.Errorf("%s was permitted: %v", raw, err)
		}
	}

	// Case in the host is a DNS detail, not a different service.
	for _, raw := range []string{"https://openlibrary.org/x", "https://OpenLibrary.org/x"} {
		target, _ := url.Parse(raw)
		if err := f.permit(target); err != nil {
			t.Errorf("%s should be permitted: %v", raw, err)
		}
	}
}

// testFetcher points a Fetcher at a local server, which means letting it
// dial loopback. Only the address guard is relaxed; the allowlist, the
// redirect check and the size limit are the real ones.
func testFetcher(t *testing.T, host string, limits Limits) *Fetcher {
	t.Helper()
	previous := dialGuard
	dialGuard = func(string) error { return nil }
	t.Cleanup(func() { dialGuard = previous })

	f := newFetcher([]string{host}, limits)
	f.client.Transport.(*http.Transport).TLSClientConfig =
		&tls.Config{InsecureSkipVerify: true} //nolint:gosec // local test server
	return f
}

// serverURL addresses the local test server as https, which is what the
// Fetcher will agree to request.
func serverURL(t *testing.T, ts *httptest.Server, _, path string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &url.URL{Scheme: "https", Host: parsed.Host, Path: path}
}

// TestTheAllowlistIsRecheckedOnEveryRedirect is the constraint ADR-0004
// wrote down before any of this existed: a service that answers 302 to
// http://169.254.169.254/ must not be followed. Checking only the URL
// this server composed means the allowlist stops at the first response,
// which is the same as not having one.
func TestTheAllowlistIsRecheckedOnEveryRedirect(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/offsite":
				http.Redirect(w, r, "https://metadata.example/secrets", http.StatusFound)
			case "/metadata-service":
				// The attack the ADR named: a 302 into link-local space.
				http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/",
					http.StatusFound)
			case "/loop":
				http.Redirect(w, r, "/loop", http.StatusFound)
			default:
				w.Write([]byte(`{"ok":true}`))
			}
		}))
	defer ts.Close()

	host := mustHost(t, ts.URL)
	f := testFetcher(t, host, Limits{})

	for _, path := range []string{"/offsite", "/metadata-service"} {
		_, err := f.Get(t.Context(), serverURL(t, ts, host, path))
		if err == nil {
			t.Fatalf("%s: a redirect off the allowlist was followed", path)
		}
		if !strings.Contains(err.Error(), ErrHostNotAllowed.Error()) {
			t.Errorf("%s: refused for the wrong reason: %v", path, err)
		}
	}

	// A service that redirects to itself forever must stop, or one
	// lookup holds a connection and a goroutine until the timeout.
	if _, err := f.Get(t.Context(), serverURL(t, ts, host, "/loop")); err == nil {
		t.Error("an endless redirect was followed")
	}

	// The permitted case still works, or the tests above prove nothing.
	body, err := f.Get(t.Context(), serverURL(t, ts, host, "/fine"))
	if err != nil {
		t.Fatalf("an allowed request failed: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body: %s", body)
	}
}

// TestAResponseLargerThanTheLimitIsRefusedNotTruncated: half a JSON
// document is not half a book, it is a parse error or, worse, a book
// with fields silently missing.
func TestAResponseLargerThanTheLimitIsRefusedNotTruncated(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(strings.Repeat("x", 4096)))
		}))
	defer ts.Close()
	host := mustHost(t, ts.URL)

	f := testFetcher(t, host, Limits{MaxBytes: 100})
	if _, err := f.Get(t.Context(), serverURL(t, ts, host, "/")); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an oversized response was accepted: %v", err)
	}

	// Exactly at the limit is fine: the bound is a maximum, not a
	// number to stay under.
	f = testFetcher(t, host, Limits{MaxBytes: 4096})
	body, err := f.Get(t.Context(), serverURL(t, ts, host, "/"))
	if err != nil {
		t.Fatalf("a response exactly at the limit was refused: %v", err)
	}
	if len(body) != 4096 {
		t.Errorf("body length %d", len(body))
	}
}

// TestASlowServiceDoesNotHoldTheRequest: an external service is a
// convenience, and a convenience must not be able to hold a handler
// open.
func TestASlowServiceDoesNotHoldTheRequest(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
	defer func() { close(release); ts.Close() }()
	host := mustHost(t, ts.URL)

	f := testFetcher(t, host, Limits{Timeout: 150 * time.Millisecond})
	start := time.Now()
	if _, err := f.Get(t.Context(), serverURL(t, ts, host, "/")); err == nil {
		t.Fatal("a hanging service was waited on forever")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the timeout did not apply: waited %s", elapsed)
	}
}

// TestNotFoundIsAnAnswerNotAFailure: a service that has never heard of a
// book has answered the question.
func TestNotFoundIsAnAnswerNotAFailure(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no", http.StatusNotFound)
		}))
	defer ts.Close()
	host := mustHost(t, ts.URL)

	f := testFetcher(t, host, Limits{})
	body, err := f.Get(t.Context(), serverURL(t, ts, host, "/"))
	if err != nil {
		t.Errorf("404 was reported as a failure: %v", err)
	}
	if body != nil {
		t.Errorf("404 returned a body: %s", body)
	}
}

// TestLookupIsRefusedUntilAnOperatorEnablesIt. The default posture of a
// self-hosted server is that it talks to nobody, and the difference
// between "off" and "found nothing" has to be legible or an operator
// debugs the wrong thing.
func TestLookupIsRefusedUntilAnOperatorEnablesIt(t *testing.T) {
	registry, err := New(nil, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if registry.Enabled() {
		t.Error("a registry with no providers reports itself enabled")
	}
	if _, err := registry.Lookup(context.Background(), Query{Title: "x"}); !errors.Is(err, ErrDisabled) {
		t.Errorf("a disabled registry looked something up: %v", err)
	}
	var nilRegistry *Registry
	if _, err := nilRegistry.Lookup(context.Background(), Query{Title: "x"}); !errors.Is(err, ErrDisabled) {
		t.Errorf("a nil registry must be disabled, not a panic: %v", err)
	}

	// A misspelled provider is refused rather than dropped: silence
	// would look exactly like a service being down.
	if _, err := New([]string{"openlibary"}, Limits{}); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("an unknown provider was accepted: %v", err)
	}

	registry, err = New([]string{"openlibrary", "googlebooks", "openlibrary"}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Names(); len(got) != 2 {
		t.Errorf("a repeated provider was configured twice: %v", got)
	}
	// The allowlist is built from the providers, never from configuration.
	if !registry.fetcher.allowed["openlibrary.org"] || !registry.fetcher.allowed["www.googleapis.com"] {
		t.Errorf("allowlist: %v", registry.fetcher.allowed)
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return parsed.Host
	}
	return host
}
