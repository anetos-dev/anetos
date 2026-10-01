// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAppendEnv(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".env.example")
	env := "# greeter\nGREETER_GREETING=Hello\nGREETER_TOKEN= # required\n# other\nOTHER_X=1\n"
	if added, err := appendEnv(file, env); err != nil || added != nil {
		t.Errorf("a missing file: %v, %v", added, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("appendEnv created the file")
	}
	if err := os.WriteFile(file, []byte("APP_NAME=x\n# GREETER_GREETING=Hi\nexport OTHER_X=2"), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := appendEnv(file, env)
	if err != nil || !slices.Equal(added, []string{"GREETER_TOKEN"}) {
		t.Errorf("added %v, %v", added, err)
	}
	want := "APP_NAME=x\n# GREETER_GREETING=Hi\nexport OTHER_X=2\n\n# greeter plugin\nGREETER_TOKEN= # required\n"
	if b, _ := os.ReadFile(file); string(b) != want {
		t.Errorf("file:\n%s", b)
	}
	if added, err := appendEnv(file, env); err != nil || added != nil {
		t.Errorf("again: %v, %v", added, err)
	}
}

func TestBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore, err := backup(root, "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a")); string(b) != "a" {
		t.Errorf("a = %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "b")); !os.IsNotExist(err) {
		t.Error("b wasn't removed")
	}
}

// A hung app doesn't hang anetos add.
func TestRunAppTimeout(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep command")
	}
	old := appTimeout
	appTimeout = 100 * time.Millisecond
	t.Cleanup(func() { appTimeout = old })
	start := time.Now()
	err = runApp(context.Background(), t.TempDir(), sleep, io.Discard, io.Discard, "30")
	if err == nil || !strings.Contains(err.Error(), "no answer") || time.Since(start) > 10*time.Second {
		t.Errorf("runApp = %v after %v", err, time.Since(start))
	}
}
