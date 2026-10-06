// SPDX-License-Identifier: Apache-2.0

package encryption_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := encryption.ParseKey(encryption.GenerateKey())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	enc, err := encryption.New(mustKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"", "hello", strings.Repeat("x", 5000)} {
		ct := enc.Encrypt([]byte(msg), "ctx")
		got, err := enc.Decrypt(ct, "ctx")
		if err != nil || string(got) != msg {
			t.Errorf("round trip %d bytes: %q %v", len(msg), got, err)
		}
	}
	a, b := enc.Encrypt([]byte("same"), "ctx"), enc.Encrypt([]byte("same"), "ctx")
	if bytes.Equal(a, b) {
		t.Error("two encryptions of one message are equal")
	}
	s := enc.EncryptString("hi", "c")
	if strings.ContainsAny(s, "+/=") {
		t.Errorf("not URL-safe: %s", s)
	}
	if got, err := enc.DecryptString(s, "c"); err != nil || got != "hi" {
		t.Errorf("DecryptString = %q, %v", got, err)
	}
}

func TestTampering(t *testing.T) {
	enc, _ := encryption.New(mustKey(t))
	other, _ := encryption.New(mustKey(t))
	ct := enc.Encrypt([]byte("secret"), "session")
	cases := map[string]func() ([]byte, error){
		"other context": func() ([]byte, error) { return enc.Decrypt(ct, "cookie") },
		"other key":     func() ([]byte, error) { return other.Decrypt(ct, "session") },
		"truncated":     func() ([]byte, error) { return enc.Decrypt(ct[:10], "session") },
		"empty":         func() ([]byte, error) { return enc.Decrypt(nil, "session") },
		"flipped bit": func() ([]byte, error) {
			bad := bytes.Clone(ct)
			bad[len(bad)-1] ^= 1
			return enc.Decrypt(bad, "session")
		},
		"bad version": func() ([]byte, error) {
			bad := bytes.Clone(ct)
			bad[0] = 9
			return enc.Decrypt(bad, "session")
		},
	}
	for name, fn := range cases {
		if _, err := fn(); !errors.Is(err, encryption.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := enc.DecryptString("!!!", "session"); !errors.Is(err, encryption.ErrInvalid) {
		t.Errorf("bad base64: %v", err)
	}
}

func TestRotation(t *testing.T) {
	oldKey, newKey := mustKey(t), mustKey(t)
	old, _ := encryption.New(oldKey)
	ct := old.Encrypt([]byte("v"), "c")
	rotated, err := encryption.New(newKey, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.Decrypt(ct, "c"); err != nil || string(got) != "v" {
		t.Errorf("old message after rotation: %q %v", got, err)
	}
	if _, err := old.Decrypt(rotated.Encrypt([]byte("v"), "c"), "c"); err == nil {
		t.Error("new messages must use the new key")
	}
	if _, err := encryption.New([]byte("short")); err == nil {
		t.Error("short key accepted")
	}
}

func TestForApp(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encryption.ForApp(app); err == nil || !strings.Contains(err.Error(), "APP_KEY=base64:") {
		t.Errorf("missing key: %v", err)
	}
	key := encryption.GenerateKey()
	app, err = anetos.New(anetos.WithSource(config.Map{"APP_KEY": key, "APP_PREVIOUS_KEYS": encryption.GenerateKey()}))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := encryption.ForApp(app)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := encryption.ParseKey(key)
	same, _ := encryption.New(k)
	if got, err := same.DecryptString(enc.EncryptString("x", "c"), "c"); err != nil || got != "x" {
		t.Errorf("ForApp doesn't use APP_KEY: %v", err)
	}
}

func BenchmarkEncrypt(b *testing.B) {
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.New(k)
	msg := bytes.Repeat([]byte("x"), 512)
	for b.Loop() {
		enc.Decrypt(enc.Encrypt(msg, "c"), "c") //nolint:errcheck // benchmark
	}
}

// Keys derived for messages are cached once a message opens, bounded,
// and never for forged ones; cached or not, opening checks everything.
func TestDerivedKeyCache(t *testing.T) {
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	e, _ := encryption.New(k)
	msg := e.EncryptString("hello", "ctx")
	for range 3 {
		if got, err := e.DecryptString(msg, "ctx"); err != nil || got != "hello" {
			t.Fatalf("DecryptString = %q, %v", got, err)
		}
	}
	if _, err := e.DecryptString(msg, "other"); err == nil {
		t.Error("a cached key opened a message for another context")
	}
	b, _ := base64.RawURLEncoding.DecodeString(msg)
	b[len(b)-1] ^= 1
	if _, err := e.DecryptString(base64.RawURLEncoding.EncodeToString(b), "ctx"); err == nil {
		t.Error("a tampered message opened")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 600 { // more than the cache holds
				m := e.EncryptString("x", "ctx")
				if got, err := e.DecryptString(m, "ctx"); err != nil || got != "x" {
					t.Errorf("DecryptString = %q, %v", got, err)
					return
				}
			}
		})
	}
	wg.Wait()
	if got, err := e.DecryptString(msg, "ctx"); err != nil || got != "hello" {
		t.Errorf("after the cache was emptied: %q, %v", got, err)
	}
}
