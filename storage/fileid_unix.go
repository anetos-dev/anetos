// SPDX-License-Identifier: Apache-2.0

//go:build unix

package storage

import (
	"io/fs"
	"syscall"
)

// fileID returns the file's inode number.
func fileID(st fs.FileInfo) uint64 {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(s.Ino) //nolint:unconvert // Ino's type differs by platform
	}
	return 0
}
