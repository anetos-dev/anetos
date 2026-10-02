// SPDX-License-Identifier: Apache-2.0

package ai

import "time"

// SetKeepAlive sets how often SSE sends a comment, for a test.
func SetKeepAlive(d time.Duration) (restore func()) {
	old := keepAlive
	keepAlive = d
	return func() { keepAlive = old }
}
