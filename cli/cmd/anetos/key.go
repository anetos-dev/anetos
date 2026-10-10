// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"anetos.dev/anetos/config"
	"anetos.dev/anetos/internal/appkey"
)

const keyGenerateUsage = `Usage: anetos key:generate [--show] [--force]

Sets APP_KEY in the project's .env (next to go.mod) to a new random key, when it is
missing or empty (creating .env if there is none). An APP_KEY that is
set is kept, unless --force: then the new key replaces it, and the old
one moves to the front of APP_PREVIOUS_KEYS, so the sessions, cookies
and values encrypted with it still work. --show prints a new key
instead, for a server's environment or a secret store.
`

// keyGenerate runs anetos key:generate.
func keyGenerate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos key:generate", flag.ContinueOnError)
	show := fs.Bool("show", false, "print APP_KEY=… with a new key instead of writing .env")
	force := fs.Bool("force", false, "replace an APP_KEY that is set (the old one moves to APP_PREVIOUS_KEYS)")
	rest, code := parse(fs, args, stderr, keyGenerateUsage)
	if code >= 0 {
		return code
	}
	if len(rest) > 0 {
		fs.Usage()
		return 2
	}
	key := appkey.Generate()
	if *show {
		fmt.Fprintln(stdout, "APP_KEY="+key)
		return 0
	}
	root, err := projectRoot()
	if err != nil {
		root = "." // not in a project: the working directory's .env
	}
	msg, err := setKey(filepath.Join(root, ".env"), key, *force)
	if err != nil {
		fmt.Fprintln(stderr, "anetos key:generate:", err)
		return 1
	}
	fmt.Fprintln(stderr, msg) // stdout stays empty: key:generate >> .env, as before v0.5, adds nothing
	return 0
}

var errKeySet = errors.New("APP_KEY is already set in .env: --force replaces it (the old key moves to APP_PREVIOUS_KEYS), --show prints a new one")

// setKey writes key as APP_KEY into the env file path, and returns what
// it did. A set APP_KEY is kept unless force, which moves it to the
// front of APP_PREVIOUS_KEYS. The last line setting a name is the one
// the app reads, so that is the one changed; values are read as the
// app's dotenv parser reads them.
func setKey(path, key string, force bool) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	eol := "\n"
	if strings.Contains(string(data), "\r\n") {
		eol = "\r\n"
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	}
	keyAt, prevAt := find(lines, "APP_KEY"), find(lines, "APP_PREVIOUS_KEYS")
	old, prev := "", ""
	if keyAt >= 0 {
		if old, err = value(lines[keyAt], "APP_KEY"); err != nil {
			return "", fmt.Errorf("%s:%d: %w", path, keyAt+1, err)
		}
	}
	if prevAt >= 0 {
		if prev, err = value(lines[prevAt], "APP_PREVIOUS_KEYS"); err != nil {
			return "", fmt.Errorf("%s:%d: %w", path, prevAt+1, err)
		}
	}
	msg := "Set APP_KEY in .env."
	switch {
	case old != "" && !force:
		return "", errKeySet
	case old != "":
		if prev != "" {
			prev = old + "," + prev
		} else {
			prev = old
		}
		if prevAt >= 0 {
			lines[prevAt] = exportOf(lines[prevAt]) + "APP_PREVIOUS_KEYS=" + prev
		} else {
			lines = insertAfter(lines, keyAt, exportOf(lines[keyAt])+"APP_PREVIOUS_KEYS="+prev)
		}
		msg = "Replaced APP_KEY in .env; the old key is the first of APP_PREVIOUS_KEYS. Set the same keys wherever the app runs."
	}
	if keyAt >= 0 {
		lines[keyAt] = exportOf(lines[keyAt]) + "APP_KEY=" + key
	} else {
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		lines = append(lines, "APP_KEY="+key, "")
	}
	return msg, os.WriteFile(path, []byte(strings.Join(lines, eol)), 0o600)
}

// find returns the index of the last line setting name, or -1.
func find(lines []string, name string) int {
	at := -1
	for i, l := range lines {
		k, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(l), "export "), "=")
		if ok && strings.TrimSpace(k) == name {
			at = i
		}
	}
	return at
}

// value returns the value line sets for name, as the app reads it.
func value(line, name string) (string, error) {
	m, err := config.ParseDotenv(strings.NewReader(line), nil)
	if err != nil {
		return "", err
	}
	return m[name], nil
}

// exportOf returns line's "export " prefix, if it has one.
func exportOf(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "export ") {
		return "export "
	}
	return ""
}

func insertAfter(lines []string, i int, line string) []string {
	return append(lines[:i+1], append([]string{line}, lines[i+1:]...)...)
}
