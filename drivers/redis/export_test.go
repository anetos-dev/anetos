// SPDX-License-Identifier: Apache-2.0

package redis

import "time"

// SetIdleConsumer sets how long an empty consumer may be idle before
// another one removes it, and returns a function restoring it.
func SetIdleConsumer(d time.Duration) func() {
	old := idleConsumer
	idleConsumer = d
	return func() { idleConsumer = old }
}
