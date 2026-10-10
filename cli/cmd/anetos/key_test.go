// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetKey(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	read := func() string {
		b, err := os.ReadFile(env)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// No .env: it is created.
	if _, err := setKey(env, "base64:one", false); err != nil || read() != "APP_KEY=base64:one\n" {
		t.Fatalf("no .env: %v %q", err, read())
	}
	// Set: kept, unless forced.
	if _, err := setKey(env, "base64:two", false); !errors.Is(err, errKeySet) || read() != "APP_KEY=base64:one\n" {
		t.Errorf("set: %v %q", err, read())
	}
	if _, err := setKey(env, "base64:two", true); err != nil || read() != "APP_KEY=base64:two\nAPP_PREVIOUS_KEYS=base64:one\n" {
		t.Errorf("forced: %v %q", err, read())
	}
	if _, err := setKey(env, "base64:three", true); err != nil || read() != "APP_KEY=base64:three\nAPP_PREVIOUS_KEYS=base64:two,base64:one\n" {
		t.Errorf("forced again: %v %q", err, read())
	}
	// Empty or missing, among other lines: replaced or added.
	if err := os.WriteFile(env, []byte("APP_NAME=Blog\nAPP_KEY=\"\"\nDB_DRIVER=sqlite\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setKey(env, "base64:four", false); err != nil || read() != "APP_NAME=Blog\nAPP_KEY=base64:four\nDB_DRIVER=sqlite\n" {
		t.Errorf("empty: %v %q", err, read())
	}
	if err := os.WriteFile(env, []byte("APP_NAME=Blog # the name\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setKey(env, "base64:five", false); err != nil || read() != "APP_NAME=Blog # the name\nAPP_KEY=base64:five\n" {
		t.Errorf("missing: %v %q", err, read())
	}
	// The last line is the one the app reads; quotes, comments and export
	// are read as the app reads them, and CRLF line endings kept.
	if err := os.WriteFile(env, []byte("APP_KEY=\r\nexport APP_KEY=\"base64:x\" # note\r\nAPP_PREVIOUS_KEYS=base64:y\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setKey(env, "base64:z", true); err != nil || read() != "APP_KEY=\r\nexport APP_KEY=base64:z\r\nAPP_PREVIOUS_KEYS=base64:x,base64:y\r\n" {
		t.Errorf("last line: %v %q", err, read())
	}
	if err := os.WriteFile(env, []byte("APP_KEY= # set me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setKey(env, "base64:seven", false); err != nil || read() != "APP_KEY=base64:seven\n" {
		t.Errorf("empty with a comment: %v %q", err, read())
	}
	// A commented-out key isn't one.
	if err := os.WriteFile(env, []byte("# APP_KEY=base64:old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setKey(env, "base64:six", false); err != nil || !strings.HasSuffix(read(), "APP_KEY=base64:six\n") {
		t.Errorf("commented: %v %q", err, read())
	}
}

func TestKeyGenerate(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, out, errOut := runCmd(t, "key:generate", "--show"); code != 0 || !strings.HasPrefix(out, "APP_KEY=base64:") || errOut != "" {
		t.Errorf("--show = %d %q %q", code, out, errOut)
	}
	if _, err := os.Stat(".env"); err == nil {
		t.Error("--show wrote .env")
	}
	if code, out, errOut := runCmd(t, "key:generate"); code != 0 || out != "" || !strings.Contains(errOut, "Set APP_KEY in .env.") {
		t.Errorf("key:generate = %d %q %q", code, out, errOut)
	}
	if code, _, errOut := runCmd(t, "key:generate"); code != 1 || !strings.Contains(errOut, "--force replaces it") {
		t.Errorf("key:generate again = %d %q", code, errOut)
	}
	if code, _, _ := runCmd(t, "key:generate", "extra"); code != 2 {
		t.Errorf("key:generate extra = %d", code)
	}
}
