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
