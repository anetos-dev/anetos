// SPDX-License-Identifier: Apache-2.0

package social

import (
	"time"

	"anetos.dev/anetos/auth"
)

// SetNow sets the clock of s, for tests.
func SetNow[U auth.Authenticatable](s *Social[U], now func() time.Time) { s.now = now }
