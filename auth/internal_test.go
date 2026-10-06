// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"testing"

	"anetos.dev/anetos/session"
)

func TestIntended(t *testing.T) {
	for stored, want := range map[string]string{
		"/dashboard?tab=2":     "/dashboard?tab=2",
		"//evil.example/x":     "/home",
		"/\\evil.example/x":    "/home",
		"https://evil.example": "/home",
	} {
		s := session.New()
		s.Put(keyIntended, stored)
		ctx := session.NewContext(context.Background(), s)
		if got := Intended(ctx, "/home"); got != want {
			t.Errorf("Intended with %q = %q, want %q", stored, got, want)
		}
		if s.Has(keyIntended) {
			t.Error("Intended didn't remove the page")
		}
	}
	if got := Intended(context.Background(), "/home"); got != "/home" {
		t.Errorf("without a session: %q", got)
	}
}

func TestTwoFactorURIAndRecoveryCodes(t *testing.T) {
	got := twoFactorSetup([]byte("12345678901234567890"), "My App: Co", "a+b@x.com").URI
	if want := "otpauth://totp/My%20App%20Co:a%2Bb%40x.com?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=My%20App%20Co"; got != want {
		t.Errorf("URI %s, want %s", got, want)
	}
	if got := twoFactorSetup([]byte("x"), "", "ada").URI; got != "otpauth://totp/ada?secret=PA" {
		t.Errorf("without an issuer: %s", got)
	}
	if recoveryHash("abcdo-lxyz2") != recoveryHash(" ABCD0 1XYZ2 ") {
		t.Error("0 and 1 for o and l")
	}
}
