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
