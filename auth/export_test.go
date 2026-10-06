// SPDX-License-Identifier: Apache-2.0

package auth

import "time"

// SetNow sets the clock of a, for tests.
func SetNow[U Authenticatable](a *Auth[U], now func() time.Time) { a.now = now }

// TOTP returns the code of a base32 secret at t.
func TOTP(secret string, t time.Time) string {
	c, err := TwoFactorCode(secret, t)
	if err != nil {
		panic(err)
	}
	return c
}

// TOTPDigits is totp with a number of digits, for RFC 6238's vectors.
func TOTPDigits(secret []byte, t time.Time, digits int) string {
	return totp(secret, t.Unix()/totpStep, digits)
}
