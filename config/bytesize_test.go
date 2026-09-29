// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestByteSize(t *testing.T) {
	tests := map[string]ByteSize{
		"512": 512, "512B": 512, "64kb": 64 * KB, "10MB": 10 * MB, "10 mb": 10 * MB,
		"1GB": GB, "2MiB": 2 * MB, "3KiB": 3 * KB, "0": 0,
	}
	for in, want := range tests {
		var b ByteSize
		if err := b.UnmarshalText([]byte(in)); err != nil || b != want {
			t.Errorf("%q = %d, %v; want %d", in, b, err, want)
		}
	}
	for _, bad := range []string{"", "MB", "-1KB", "1.5MB", "10TB", "99999999999GB"} {
		var b ByteSize
		if err := b.UnmarshalText([]byte(bad)); err == nil {
			t.Errorf("%q accepted as %d", bad, b)
		}
	}
	for b, want := range map[ByteSize]string{0: "0B", 100: "100B", 2 * KB: "2KB", 10 * MB: "10MB", 3 * GB: "3GB", MB + 1: "1048577B"} {
		if b.String() != want {
			t.Errorf("String(%d) = %s, want %s", int64(b), b.String(), want)
		}
	}

	var cfg struct {
		Limit ByteSize `env:"LIMIT" default:"1MB"`
	}
	if err := Bind(Map{}, &cfg); err != nil || cfg.Limit != MB {
		t.Errorf("default = %v, %v", cfg.Limit, err)
	}
	if err := Bind(Map{"LIMIT": "lots"}, &cfg); err == nil {
		t.Error("invalid size accepted")
	}
}
