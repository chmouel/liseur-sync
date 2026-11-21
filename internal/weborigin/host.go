// Package weborigin normalizes hostnames for browser origin comparisons.
package weborigin

import (
	"net/netip"
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
