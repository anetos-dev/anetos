// SPDX-License-Identifier: Apache-2.0

package secscan

import (
	"bufio"
	"bytes"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// keyFormat is a kind of key that has a recognizable form.
type keyFormat struct {
	name string
	re   *regexp.Regexp
}

// keyFormats are the keys recognized by their form alone: almost no
// false alarms.
var keyFormats = []keyFormat{
	{"an AWS access key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"a Stripe secret key", regexp.MustCompile(`\b[sr]k_live_[0-9a-zA-Z]{20,}\b`)},
	{"a GitHub token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})\b`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abposr]-[0-9A-Za-z-]{10,}\b`)},
	{"a Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"an Anthropic API key", regexp.MustCompile(`\bsk-ant-[0-9A-Za-z_-]{20,}`)},
	{"an OpenAI API key", regexp.MustCompile(`\bsk-(proj-)?[A-Za-z0-9_-]{20,}T3BlbkFJ[A-Za-z0-9_-]{20,}`)},
	{"a SendGrid API key", regexp.MustCompile(`\bSG\.[0-9A-Za-z_-]{22}\.[0-9A-Za-z_-]{43}\b`)},
	{"a private key", regexp.MustCompile(`-----BEGIN ((RSA|EC|DSA|OPENSSH|ENCRYPTED) )?PRIVATE KEY-----`)},
}

// assigned is a value given to a name, in Go, YAML, JSON, TOML or an
// env file: name = "value", name: value, "name": "value", NAME=value;
// quoted is a value written as a string literal.
var (
	assigned = regexp.MustCompile(`["']?([A-Za-z_][A-Za-z0-9_.-]*)["']?\s*(?::=|=|:)\s*(["'\x60]?)([^"'\x60\s,;}]{16,})`)
	quoted   = regexp.MustCompile(`["']?([A-Za-z_][A-Za-z0-9_.-]*)["']?\s*(?::=|=|:)\s*(["'\x60])([^"'\x60\s]{16,})["'\x60]`)
)

// codeFiles are the files whose secrets are string literals.
var codeFiles = map[string]bool{".go": true, ".templ": true, ".js": true, ".ts": true}

// secretKeyName reports whether a name is given secrets: password,
// apiKey, client_secret, signingKey (not passwordHash, tokens, tokenizer).
func secretKeyName(name string) bool {
	w := words(name)
	if len(w) == 0 || notSecret[w[len(w)-1]] {
		return false
	}
	for i, x := range w {
		switch x {
		case "secret", "token", "password", "passwd", "apikey", "passphrase", "credential", "credentials":
			return true
		case "key":
			if i > 0 && keyKinds[w[i-1]] {
				return true
			}
		}
	}
	return false
}

var (
	keyKinds  = map[string]bool{"api": true, "private": true, "access": true, "auth": true, "signing": true, "encryption": true, "secret": true, "master": true, "app": true}
	notSecret = map[string]bool{"hash": true, "hashed": true, "digest": true, "count": true, "url": true, "uri": true, "name": true, "type": true,
		"length": true, "len": true, "size": true, "file": true, "path": true, "endpoint": true, "header": true, "field": true, "env": true,
		"var": true, "prefix": true, "suffix": true, "ttl": true, "expiry": true, "expires": true, "lifetime": true, "timeout": true,
		"cookie": true, "param": true, "label": true, "id": true, "policy": true, "min": true, "max": true}
)

// notAValue matches values that are code or references, not secrets:
// calls, cfg.Field, $ENV and bcrypt hashes, UUIDs.
var notAValue = regexp.MustCompile(`[()]|^[A-Za-z_]\w*(\.[A-Za-z_]\w*)+$|^\$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// placeholder words mean a value is an example, not a secret.
var placeholder = regexp.MustCompile(`(?i)(example|change|your|xxxx|placeholder|dummy|sample|fake|redacted|replace|todo|<|\$\{|\{\{|%s|os\.Getenv|config\.|env\(|\.\.\.)`)

// secretFiles are the files searched, by extension (or name).
var secretFiles = map[string]bool{".go": true, ".templ": true, ".yaml": true, ".yml": true, ".json": true, ".toml": true,
	".ini": true, ".conf": true, ".properties": true, ".sh": true, ".example": true, ".sample": true, ".tf": true, ".js": true, ".ts": true}

// skipDirs are folders not searched: dependencies, build output, data.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "storage": true, "testdata": true,
	"tmp": true, "dist": true, "build": true, ".idea": true, ".vscode": true, ".cache": true}

// secrets searches the project's files for secrets written in them.
// Files of settings (.env, production.env) are doctor's (not in git),
// tests and generated code aren't searched.
func secrets(dir string) ([]Finding, error) {
	var out []Finding
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return skip(d) // unreadable: skip it
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (skipDirs[name] || strings.HasPrefix(name, ".") && name != ".github") {
				return filepath.SkipDir
			}
			return nil
		}
		if !searched(name) || !d.Type().IsRegular() {
			return nil
		}
		b, ok := readText(path)
		if !ok || generated(b) {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		out = append(out, secretsIn(filepath.ToSlash(rel), b)...)
		return nil
	})
	return out, err
}

// skip skips an entry WalkDir couldn't read: its folder, or nothing.
func skip(d fs.DirEntry) error {
	if d != nil && d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// readText reads a text file of at most 512 KB; ok is false for a
// bigger, binary or unreadable one.
func readText(path string) (b []byte, ok bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 {
		return nil, false
	}
	b, err = os.ReadFile(path)
	if err != nil || bytes.IndexByte(b, 0) >= 0 {
		return nil, false
	}
	return b, true
}

// searched reports whether a file is searched for secrets.
func searched(name string) bool {
	switch {
	case strings.HasSuffix(name, "_test.go"), strings.HasSuffix(name, "_templ.go"), strings.HasSuffix(name, "_gen.go"),
		strings.Contains(name, ".min."), name == "package-lock.json", name == "go.sum", name == "openapi.json":
		return false
	case name == ".env", strings.HasPrefix(name, ".env.") && !strings.HasSuffix(name, ".example") && !strings.HasSuffix(name, ".sample") && name != ".env.testing",
		strings.HasSuffix(name, ".env"):
		return false // settings, which doctor's git check covers
	case name == "Dockerfile", strings.HasPrefix(name, ".env."):
		return true
	}
	return secretFiles[filepath.Ext(name)]
}

// secretsIn finds the secrets in a file's text.
func secretsIn(rel string, b []byte) []Finding {
	var out []Finding
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		matched := false
		for _, k := range keyFormats {
			if m := k.re.FindString(line); m != "" && !strings.Contains(strings.ToLower(m), "example") {
				out = append(out, Finding{Rule: "source.secret", Severity: "problem", Where: rel, File: rel, Line: n,
					Message: "the file has " + k.name + " (" + mask(m) + "): anyone who reads the code has it; revoke it, and read it from a setting (.env, the server's environment)"})
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		re := assigned
		if codeFiles[filepath.Ext(rel)] {
			re = quoted
		}
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			name, value := m[1], m[3]
			if !secretKeyName(name) || placeholder.MatchString(value) || notAValue.MatchString(value) || entropy(value) < 3.5 || !mixed(value) {
				continue
			}
			out = append(out, Finding{Rule: "source.secret", Severity: "warning", Where: rel, File: rel, Line: n,
				Message: name + " is given a value that looks like a secret (" + mask(value) + "): anyone who reads the code has it; read it from a setting (.env, the server's environment), and change it if it's real"})
			break
		}
	}
	return out
}

// mask shows the start of a secret only: up to 4 characters, a fifth of
// it at most.
func mask(s string) string {
	return s[:min(4, len(s)/5)] + "…"
}

// generated reports whether a file says it's generated (Code generated
// … DO NOT EDIT.).
func generated(b []byte) bool {
	head := b[:min(len(b), 2048)]
	return bytes.Contains(head, []byte("Code generated")) && bytes.Contains(head, []byte("DO NOT EDIT"))
}

// entropy is s's Shannon entropy, in bits per character.
func entropy(s string) float64 {
	counts := map[rune]float64{}
	for _, r := range s {
		counts[r]++
	}
	var h float64
	n := float64(len([]rune(s)))
	for _, c := range counts {
		p := c / n
		h -= p * math.Log2(p)
	}
	return h
}

// mixed reports whether s has letters and digits, as keys do (and
// words and identifiers usually don't).
func mixed(s string) bool {
	var letter, digit bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			letter = true
		}
	}
	return letter && digit
}
