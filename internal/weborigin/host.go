// Package weborigin normalizes hostnames for browser origin comparisons.
package weborigin

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

var lookup = idna.New(idna.MapForLookup(), idna.StrictDomainName(false))

// Host returns an ASCII DNS name or canonical IP address.
func Host(raw string) (string, bool) {
	if ip, err := netip.ParseAddr(raw); err == nil {
		if ip.Zone() != "" {
			return "", false
		}
		return ip.String(), true
	}
	host, err := lookup.ToASCII(raw)
	if err != nil || host == "" {
		return "", false
	}
	for _, c := range host {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_'
		if !ok {
			return "", false
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.String(), true
	}
	// Browsers interpret numeric final labels as IPv4, including shortened
	// and hexadecimal forms that netip deliberately does not accept.
	last := strings.TrimSuffix(host, ".")
	if i := strings.LastIndexByte(last, '.'); i >= 0 {
		last = last[i+1:]
	}
	if last == "" {
		return "", false
	}
	base := 10
	if strings.HasPrefix(strings.ToLower(last), "0x") {
		last, base = last[2:], 16
	}
	numeric := true
	for _, c := range strings.ToLower(last) {
		valid := c >= '0' && c <= '9' || base == 16 && c >= 'a' && c <= 'f'
		if !valid {
			numeric = false
			break
		}
	}
	if numeric {
		return "", false
	}
	return host, true
}

// Same reports whether two bare HTTP origins identify the same origin.
func Same(origin, want string) bool {
	if strings.Contains(origin, "#") || strings.Contains(want, "#") {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil || !bareOrigin(o) {
		return false
	}
	w, err := url.Parse(want)
	if err != nil || !bareOrigin(w) || !strings.EqualFold(o.Scheme, w.Scheme) {
		return false
	}
	oHost, oValid := Host(o.Hostname())
	wHost, wValid := Host(w.Hostname())
	if !oValid || !wValid || !strings.EqualFold(oHost, wHost) {
		return false
	}
	oPort, oOK := originPort(o)
	wPort, wOK := originPort(w)
	return oOK && wOK && oPort == wPort
}

func bareOrigin(u *url.URL) bool {
	if strings.HasPrefix(u.Host, "[") {
		ip, err := netip.ParseAddr(u.Hostname())
		if err != nil || !ip.Is6() {
			return false
		}
	} else if strings.Contains(u.Hostname(), ":") {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil &&
		u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.HasSuffix(u.Host, ":")
}

func originPort(u *url.URL) (int, bool) {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			return 443, true
		}
		return 80, true
	}
	n, err := strconv.Atoi(port)
	return n, err == nil && n > 0 && n <= 65535
}
