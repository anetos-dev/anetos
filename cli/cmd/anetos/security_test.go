// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSecurityCheckPaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wd, _ := os.Getwd()
	got := absPaths([]string{"--out=r.sarif", "--ignore", "ci/ignore.yaml", "--format=sarif", "--out=-", "-out", filepath.Join(wd, "x")})
	want := []string{"--out=" + filepath.Join(wd, "r.sarif"), "--ignore", filepath.Join(wd, "ci", "ignore.yaml"), "--format=sarif", "--out=-", "-out", filepath.Join(wd, "x")}
	if !slices.Equal(got, want) {
		t.Errorf("absPaths:\n got %q\nwant %q", got, want)
	}

	// Without a .env, it says how to write one before building anything.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_KEY", "")
	var out, errOut strings.Builder
	if code := securityCheck(t.Context(), nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "cp .env.example .env") {
		t.Errorf("without a .env: %d %s%s", code, out.String(), errOut.String())
	}
}
