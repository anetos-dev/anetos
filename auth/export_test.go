// SPDX-License-Identifier: Apache-2.0

package auth

import "time"

// SetNow sets the clock of a, for tests.
func SetNow[U Authenticatable](a *Auth[U], now func() time.Time) { a.now = now }
