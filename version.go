// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"regexp"
	"runtime/debug"
	"sync"
)

// develVersion is the version of this source tree: the next release,
// marked as in development.
const develVersion = "v0.5.0-dev"

// Version returns the version of the Anetos module the app is built
// with: from the binary's build information ("v0.2.3"), or, when the
// module is replaced by a directory or built from a commit rather than
// a release (a pseudo-version such as v0.1.1-0.20261001…), the version
// its source is heading for, such as "v0.5.0-dev". Plugins' version requirements are checked against it.
func Version() string { return version() }

// pseudo matches pseudo-versions: v0.0.0-20261001120000-abcdef123456,
// v0.1.1-0.20261001120000-abcdef123456, v0.2.0-rc.1.0.2026….
var pseudo = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}(\+incompatible)?$`)

var version = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return develVersion
	}
	for _, m := range append([]*debug.Module{&bi.Main}, bi.Deps...) {
		if m.Path != "anetos.dev/anetos" {
			continue
		}
		if m.Replace != nil || m.Version == "" || m.Version == "(devel)" || pseudo.MatchString(m.Version) {
			return develVersion
		}
		return m.Version
	}
	return develVersion
})
