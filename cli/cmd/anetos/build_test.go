// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func TestValidVersion(t *testing.T) {
	for _, v := range []string{"", "v1.2.0", "v0.0.0-20261006100000-1a2b3c4d5e6f+dirty", "main", "release/2026_10~rc1"} {
		if !validVersion(v) {
			t.Errorf("validVersion(%q) = false", v)
		}
	}
	for _, v := range []string{"v1 2", "v1\n-X=a.b=c", "v1'", `v1"`, "v1;rm", "v1\t", "vé"} {
		if validVersion(v) {
			t.Errorf("validVersion(%q) = true", v)
		}
	}
}

func TestMergeLDFlags(t *testing.T) {
	for _, tc := range []struct {
		flags, rest []string
		want        string
	}{
		{nil, nil, "-s -w"},
		{[]string{"-tags=prod"}, []string{"-tags=prod"}, "-s -w"},
		{[]string{"-ldflags=-X main.a=b", "-v"}, []string{"-v"}, "-s -w -X main.a=b"},
		{[]string{"--ldflags", "-X main.a=b"}, nil, "-s -w -X main.a=b"},
		{[]string{"-ldflags="}, nil, "-s -w"},
	} {
		got, rest := mergeLDFlags("-s -w", tc.flags)
		if got != tc.want || !slices.Equal(rest, tc.rest) {
			t.Errorf("mergeLDFlags(%q) = %q, %q; want %q, %q", tc.flags, got, rest, tc.want, tc.rest)
		}
	}
}

func TestBuildFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--version=v1 2"}, "only letters, digits"},
		{[]string{"--target=linux"}, "want os/arch"},
		{[]string{"--target=linux/arm64/v8"}, "want os/arch"},
		{[]string{"extra"}, `unexpected argument "extra"`},
	} {
		var out, errOut bytes.Buffer
		if code := build(context.Background(), tc.args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%q: %d %q", tc.args, code, errOut.String())
		}
	}
}
