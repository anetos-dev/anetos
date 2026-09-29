// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ByteSize is a number of bytes that can be written with a unit in
// configuration: "512", "64KB", "10MB", "1GB". Units are powers of 1024 and
// are case-insensitive; "KiB", "MiB" and "GiB" are accepted too.
type ByteSize int64

// Common sizes.
const (
	KB ByteSize = 1 << 10
	MB ByteSize = 1 << 20
	GB ByteSize = 1 << 30
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (b *ByteSize) UnmarshalText(text []byte) error {
	s := strings.ToUpper(strings.TrimSpace(string(text)))
	mult := ByteSize(1)
	for _, u := range []struct {
		suffix string
		size   ByteSize
	}{{"GIB", GB}, {"MIB", MB}, {"KIB", KB}, {"GB", GB}, {"MB", MB}, {"KB", KB}, {"B", 1}} {
		if rest, ok := strings.CutSuffix(s, u.suffix); ok {
			s, mult = strings.TrimSpace(rest), u.size
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > int64(^uint64(0)>>1)/int64(mult) {
		return fmt.Errorf("invalid size %q (use values like 512KB, 10MB, 1GB)", string(text))
	}
	*b = ByteSize(n) * mult
	return nil
}

// String formats b with the largest exact unit, e.g. "10MB".
func (b ByteSize) String() string {
	switch {
	case b != 0 && b%GB == 0:
		return strconv.FormatInt(int64(b/GB), 10) + "GB"
	case b != 0 && b%MB == 0:
		return strconv.FormatInt(int64(b/MB), 10) + "MB"
	case b != 0 && b%KB == 0:
		return strconv.FormatInt(int64(b/KB), 10) + "KB"
	}
	return strconv.FormatInt(int64(b), 10) + "B"
}
