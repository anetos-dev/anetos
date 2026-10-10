// SPDX-License-Identifier: Apache-2.0

package storage

import "time"

// SetNow replaces d's clock.
func SetNow(d *Disk, now func() time.Time) { d.now = now }

// DiskSource is diskSourceOf, for the tests.
var DiskSource = diskSourceOf
