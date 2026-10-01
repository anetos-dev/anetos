// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package storage

import "io/fs"

// fileID returns 0: the file's number isn't in its FileInfo here.
func fileID(fs.FileInfo) uint64 { return 0 }
