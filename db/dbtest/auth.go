// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/encryption"
)

func init() {
	extra = append(extra, test{"AuthTokens", testAuthTokens})
}

type stAuthUser struct{ id string }

func (u stAuthUser) AuthID() string     { return u.id }
func (stAuthUser) AuthPassword() string { return "" }

// testAuthTokens issues and checks API tokens in the table
// auth.Migrations creates.
func testAuthTokens(t *testing.T, ctx context.Context) {
	r, err := migrate.NewRunner(d(ctx), []*migrate.Set{auth.Migrations()}, migrate.WithTable("st_auth_migrations"))
	check(t, err)
	_, err = r.Up(ctx)
	check(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS api_tokens")
		_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS st_auth_migrations")
	})
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.NewEncrypter(k)
	cfg, err := auth.LoadConfig(nil)
	check(t, err)
	a, err := auth.NewWithConfig(cfg, auth.Users[stAuthUser]{
		ByID: func(_ context.Context, id string) (stAuthUser, error) {
			if id == "gone" {
				return stAuthUser{}, auth.ErrNoUser
			}
			return stAuthUser{id}, nil
		},
		ByLogin: func(context.Context, string) (stAuthUser, error) { return stAuthUser{}, auth.ErrNoUser },
	}, enc)
	check(t, err)

	ada := stAuthUser{"ada"}
	plain, tok, err := a.CreateToken(ctx, ada, "cli", []string{"posts:read"}, 0)
	check(t, err)
	expired, _, err := a.CreateToken(ctx, ada, "old", []string{"*"}, time.Millisecond)
	check(t, err)
	orphan, _, err := a.CreateToken(ctx, stAuthUser{"gone"}, "orphan", nil, 0)
	check(t, err)
	time.Sleep(5 * time.Millisecond)

	var seen string
	var can, canWrite bool
	h := a.TokenMiddleware(a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.User[stAuthUser](r.Context())
		seen = u.id
		can, canWrite = auth.TokenCan(r.Context(), "posts:read"), auth.TokenCan(r.Context(), "posts:write")
	})))
	call := func(header string) int {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api", nil)
		req.Header.Set("Accept", "application/json")
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if code := call("Bearer " + plain); code != http.StatusOK || seen != "ada" || !can || canWrite {
		t.Errorf("valid token: %d user %q can %v/%v", code, seen, can, canWrite)
	}
	got, err := db.Find[auth.Token](ctx, tok.ID)
	check(t, err)
	if got.LastUsedAt == nil || got.Hash == plain || len(got.Abilities) != 1 {
		t.Errorf("stored token: %+v", got)
	}
	for name, header := range map[string]string{
		"wrong secret": "Bearer " + plain + "x",
		"expired":      "Bearer " + expired,
		"no user":      "Bearer " + orphan,
		"not a token":  "Bearer abc",
		"basic":        "Basic YTpi",
		"none":         "",
	} {
		if code := call(header); code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, code)
		}
	}

	list, err := a.Tokens(ctx, ada)
	check(t, err)
	if len(list) != 2 || list[0].Name != "old" {
		t.Errorf("tokens: %+v", list)
	}
	check(t, a.RevokeToken(ctx, stAuthUser{"bob"}, tok.ID)) // not bob's
	if code := call("Bearer " + plain); code != http.StatusOK {
		t.Errorf("revoked by another user: %d", code)
	}
	check(t, a.RevokeToken(ctx, ada, tok.ID))
	if code := call("Bearer " + plain); code != http.StatusUnauthorized {
		t.Errorf("revoked token: %d", code)
	}
	check(t, a.RevokeAllTokens(ctx, ada))
	if list, _ := a.Tokens(ctx, ada); len(list) != 0 {
		t.Errorf("tokens after RevokeAllTokens: %d", len(list))
	}
}
