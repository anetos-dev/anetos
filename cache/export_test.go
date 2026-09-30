// SPDX-License-Identifier: Apache-2.0

package cache

import "log/slog"

// SetLogger sets the logger of c, for tests.
func SetLogger(c *Cache, l *slog.Logger) { c.log = l }
