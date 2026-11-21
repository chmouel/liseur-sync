package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ChatGPTMetadataURL is the Client ID Metadata Document ChatGPT
// identifies itself with. The admin UI offers it as a one-click entry;
// nothing trusts it until an administrator allows it.
const ChatGPTMetadataURL = "https://chatgpt.com/oauth/client.json"

const (
	metadataMaxBytes = 64 << 10
	metadataTimeout  = 5 * time.Second
	metadataCacheTTL = 15 * time.Minute
	maxRedirectURIs  = 16
)

// ClientMetadata is the part of a Client ID Metadata Document this
// server reads.
type ClientMetadata struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	ClientURI    string   `json:"client_uri"`
	RedirectURIs []string `json:"redirect_uris"`
	AuthMethod   string   `json:"token_endpoint_auth_method"`
	AuthMethods  []string `json:"token_endpoint_auth_methods_supported"`
	ClientSecret string   `json:"client_secret"`
}

// MetadataFetcher retrieves Client ID Metadata Documents. It only ever
// fetches a URL an administrator allowlisted, and even then it refuses
// anything but a direct HTTPS answer from a public address: no
// redirects, no proxy, no private, loopback or link-local destination,
// a small body and a short deadline.
type MetadataFetcher struct {
	// Client is the HTTP client used for fetches. NewMetadataFetcher
	// builds the hardened one; tests may substitute their own.
	Client *http.Client
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cachedMetadata
}

type cachedMetadata struct {
	doc     ClientMetadata
	expires time.Time
}

func NewMetadataFetcher() *MetadataFetcher {
	dialer := &net.Dialer{Timeout: metadataTimeout, Control: refusePrivateDial}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   metadataTimeout,
		ResponseHeaderTimeout: metadataTimeout,
		MaxIdleConns:          4,
		IdleConnTimeout:       time.Minute,
		ForceAttemptHTTP2:     true,
	}
	return &MetadataFetcher{
		Client: &http.Client{
			Transport:     transport,
			Timeout:       metadataTimeout,
			CheckRedirect: refuseRedirect,
		},
		now:   time.Now,
		cache: map[string]cachedMetadata{},
	}
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return errors.New("client metadata must not redirect")
}

// refusePrivateDial runs after DNS resolution, on the address actually
// being dialled, so a name that resolves to an internal address is
// refused even if it resolved elsewhere a moment ago.
func refusePrivateDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if !PublicAddr(ip) {
		return fmt.Errorf("refusing to fetch client metadata from %s", ip)
	}
	return nil
}

var nonPublicPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "2001:db8::/32", "2002::/16", "fc00::/7",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// PublicAddr reports whether ip is a globally routable unicast address.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// ValidateMetadataURL checks the shape a Client ID Metadata Document URL
// must have: HTTPS, a path, and nothing that lets two spellings of one
// client compare unequal.
func ValidateMetadataURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.Fragment != "" || u.RawQuery != "" || u.Path == "" || u.Path == "/" ||
		strings.Contains(u.Path, "/./") || strings.Contains(u.Path, "/../") || u.String() != raw {
		return errors.New("a client metadata URL must be a plain https URL with a path")
	}
	return nil
}

// Fetch returns the validated metadata document at docURL.
func (f *MetadataFetcher) Fetch(ctx context.Context, docURL string) (ClientMetadata, error) {
	now := f.now()
	f.mu.Lock()
	if c, ok := f.cache[docURL]; ok && now.Before(c.expires) {
		f.mu.Unlock()
		return c.doc, nil
	}
	f.mu.Unlock()

	if err := ValidateMetadataURL(docURL); err != nil {
		return ClientMetadata{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return ClientMetadata{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.Client.Do(req)
	if err != nil {
		return ClientMetadata{}, fmt.Errorf("fetching client metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ClientMetadata{}, fmt.Errorf("client metadata answered %s", resp.Status)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil ||
		(mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
		return ClientMetadata{}, errors.New("client metadata is not JSON")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, metadataMaxBytes+1))
	if err != nil {
		return ClientMetadata{}, fmt.Errorf("reading client metadata: %w", err)
	}
	if len(body) > metadataMaxBytes {
		return ClientMetadata{}, errors.New("client metadata is too large")
	}
	var doc ClientMetadata
	if err := json.Unmarshal(body, &doc); err != nil {
		return ClientMetadata{}, fmt.Errorf("parsing client metadata: %w", err)
	}
	if err := validateMetadata(&doc, docURL); err != nil {
		return ClientMetadata{}, err
	}
	f.mu.Lock()
	f.cache[docURL] = cachedMetadata{doc: doc, expires: now.Add(metadataCacheTTL)}
	f.mu.Unlock()
	return doc, nil
}

// Forget drops a cached document, so removing and re-adding a client
// reads it again.
func (f *MetadataFetcher) Forget(docURL string) {
	f.mu.Lock()
	delete(f.cache, docURL)
	f.mu.Unlock()
}

func validateMetadata(doc *ClientMetadata, docURL string) error {
	if doc.ClientID != docURL {
		return errors.New("client metadata names a different client_id")
	}
	allowNone := doc.AuthMethod == "none"
	for _, method := range doc.AuthMethods {
		allowNone = allowNone || method == "none"
	}
	if doc.ClientSecret != "" || !allowNone {
		return errors.New("client metadata must permit the none authentication method without a shared secret")
	}
	if len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > maxRedirectURIs {
		return fmt.Errorf("client metadata must list between 1 and %d redirect_uris", maxRedirectURIs)
	}
	for _, r := range doc.RedirectURIs {
		if err := ValidateRedirectURI(r); err != nil {
			return err
		}
	}
	doc.ClientName = clampName(doc.ClientName)
	return nil
}

func clampName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	return name
}

// ValidateRedirectURI accepts an https URL, or an http URL on a
// loopback address for a native client (RFC 8252 §7.3). Fragments are
// never allowed, and comparison later is by exact string.
func ValidateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" || u.User != nil || u.String() != raw {
		return fmt.Errorf("redirect URI %q is not a plain absolute URL", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if ip, err := netip.ParseAddr(host); err == nil && ip.IsLoopback() {
			return nil
		}
		if host == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("redirect URI %q must be https, or http on a loopback address", raw)
}
