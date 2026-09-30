// SPDX-License-Identifier: Apache-2.0

// Package appkey parses and generates APP_KEY values. It is shared by the
// kernel (which validates the configuration) and the encryption package.
package appkey

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Size is the length of a key in bytes (AES-256).
const Size = 32

// Prefix starts every key: "base64:" followed by 32 bytes in base64.
const Prefix = "base64:"

// Parse decodes a key written as "base64:…" (standard or URL alphabet,
// with or without padding).
func Parse(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	b64, ok := strings.CutPrefix(s, Prefix)
	if !ok {
		return nil, errors.New(`a key must start with "base64:" (generate one with encryption.GenerateKey)`)
	}
	b64 = strings.TrimRight(b64, "=")
	var key []byte
	var err error
	if strings.ContainsAny(b64, "-_") {
		key, err = base64.RawURLEncoding.DecodeString(b64)
	} else {
		key, err = base64.RawStdEncoding.DecodeString(b64)
	}
	if err != nil {
		return nil, fmt.Errorf("a key must be base64: %w", err)
	}
	if len(key) != Size {
		return nil, fmt.Errorf("a key must be %d bytes, got %d", Size, len(key))
	}
	return key, nil
}

// Generate returns a new random key in the "base64:…" form.
func Generate() string {
	key := make([]byte, Size)
	_, _ = rand.Read(key) // never fails (crypto/rand panics instead)
	return Prefix + base64.StdEncoding.EncodeToString(key)
}
