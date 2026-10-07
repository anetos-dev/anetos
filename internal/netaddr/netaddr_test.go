// SPDX-License-Identifier: Apache-2.0

package netaddr

import "testing"

func TestLocal(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST.": true, "app.localhost": true, "127.0.0.1": true, "127.1.2.3": true,
		"::1": true, "[::1]": true, "": false, "example.com": false, "10.0.0.5": false, "localhost.example.com": false,
		"0.0.0.0": false,
	} {
		if got := Local(host); got != want {
			t.Errorf("Local(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestExample(t *testing.T) {
	for host, want := range map[string]bool{
		"example.com": true, "smtp.example.com": true, "EXAMPLE.ORG.": true, "shop.example": true, "app.test": true,
		"example.co": false, "myexample.com": false, "anetos.dev": false, "": false,
	} {
		if got := Example(host); got != want {
			t.Errorf("Example(%q) = %v", host, got)
		}
	}
}
