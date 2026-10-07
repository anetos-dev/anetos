// SPDX-License-Identifier: Apache-2.0

// Package netaddr has small helpers for host names and addresses.
package netaddr

import (
	"net/netip"
	"strings"
)

// Local reports whether host names this machine: localhost (or a name
// under .localhost) or a loopback address, with or without brackets.
func Local(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && ip.IsLoopback()
}

// Example reports whether host is one of the domains reserved for
// documentation (RFC 2606: example.com, example.net, example.org, and
// the .example and .test top-level domains) or a name under them: a
// setting still holding a sample's value.
func Example(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range []string{"example.com", "example.net", "example.org"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return strings.HasSuffix(host, ".example") || strings.HasSuffix(host, ".test")
}
