// SPDX-License-Identifier: Apache-2.0

package anetos

import "testing"

func TestPseudoVersions(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.0.0-20261001120000-abcdef123456":                true,
		"v0.1.1-0.20261001120000-abcdef123456":              true,
		"v0.2.0-rc.1.0.20261001120000-abcdef123456":         true,
		"v2.0.1-0.20261001120000-abcdef123456+incompatible": true,
		"v0.2.0":      false,
		"v0.2.0-rc.1": false,
		"v0.2.0-dev":  false,
	} {
		if got := pseudo.MatchString(v); got != want {
			t.Errorf("pseudo(%s) = %v", v, got)
		}
	}
	if v := Version(); v != develVersion { // tests build the module itself
		t.Errorf("Version = %s, want %s", v, develVersion)
	}
}
