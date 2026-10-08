// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
)

// Token is an API token of a user, in the api_tokens table. Clients send
// it as "Authorization: Bearer <id>|<secret>"; only a hash of the secret
// is stored.
type Token struct {
	db.Model
	// UserID is the owner's AuthID.
	UserID string `db:"user_id" json:"user_id"`
	// Name says what the token is for ("CLI on my laptop").
	Name string `db:"name" json:"name"`
	// Hash is the SHA-256 of the secret, in hex.
	Hash string `db:"token_hash" json:"-"`
	// Abilities are what the token may do; "*" is everything.
	Abilities []string `db:"abilities,json" json:"abilities"`
	// LastUsedAt is when the token was last used (to the minute).
	LastUsedAt *time.Time `db:"last_used_at" json:"last_used_at"`
	// ExpiresAt is when the token stops working; nil for never.
	ExpiresAt *time.Time `db:"expires_at" json:"expires_at"`
}

// TableName implements db.Tabler.
func (Token) TableName() string { return "api_tokens" }

// Can reports whether the token has the ability ("*" has them all).
func (t *Token) Can(ability string) bool {
	return slices.Contains(t.Abilities, "*") || slices.Contains(t.Abilities, ability)
}

// Migrations returns the migration creating the api_tokens table, for
// migrate.ForApp.
func Migrations() *migrate.Set {
	s := migrate.NewSet("auth")
	s.AddFunc("2026_10_01_000200_create_api_tokens_table",
		func(s *migrate.Schema) error {
			return s.Create("api_tokens", func(t *migrate.Table) {
				t.ID()
				t.String("user_id", 255)
				t.String("name", 255)
				t.String("token_hash", 64).Unique()
				t.JSON("abilities")
				t.Timestamp("last_used_at").Nullable()
				t.Timestamp("expires_at").Nullable()
				t.Timestamps()
				t.Index("user_id")
			})
		},
		func(s *migrate.Schema) error { return s.Drop("api_tokens") })
	return s
}

var (
	colID       = db.Col[int64]("id")
	colUserID   = db.Col[string]("user_id")
	colLastUsed = db.Col[*time.Time]("last_used_at")
)

var errTokenActing = &statusError{http.StatusForbidden, "auth: API tokens can't be created while acting as another user"}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// CreateToken issues an API token for u with abilities ("*" for all),
// expiring after ttl (0: never). It returns the token to give the client,
// shown once (only its hash is stored), and the stored Token. Not while
// acting as another user ([Auth.Impersonate]): a token would outlive the
// impersonation and its checks (403).
//
//	plain, tok, err := a.CreateToken(c, u, "deploy script", []string{"deploy"}, 90*24*time.Hour)
func (a *Auth[U]) CreateToken(ctx context.Context, u U, name string, abilities []string, ttl time.Duration) (string, *Token, error) {
	if s := session.From(ctx); s != nil && s.String(keyImpersonator) != "" {
		return "", nil, errTokenActing
	}
	secret := randomToken()
	t := &Token{UserID: u.AuthID(), Name: name, Hash: hashSecret(secret), Abilities: slices.Clone(abilities)}
	if t.Abilities == nil {
		t.Abilities = []string{}
	}
	if ttl > 0 {
		exp := a.now().Add(ttl).UTC()
		t.ExpiresAt = &exp
	}
	if err := db.Create(ctx, t); err != nil {
		return "", nil, err
	}
	return strconv.FormatInt(t.ID, 10) + "|" + secret, t, nil
}

// Tokens returns u's API tokens, newest first.
func (a *Auth[U]) Tokens(ctx context.Context, u U) ([]Token, error) {
	return db.Query[Token](ctx).Where(colUserID.Eq(u.AuthID())).OrderBy(colID.Desc()).Get()
}

// RevokeToken deletes u's API token id. Revoking a token that isn't
// there, or isn't u's, does nothing.
func (a *Auth[U]) RevokeToken(ctx context.Context, u U, id int64) error {
	_, err := db.Query[Token](ctx).Where(colID.Eq(id), colUserID.Eq(u.AuthID())).Delete()
	return err
}

func parseID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0
}

// RevokeAllTokens deletes every API token of u: after a password reset,
// say, so that whoever had the account loses its API access too.
func (a *Auth[U]) RevokeAllTokens(ctx context.Context, u U) error {
	_, err := db.Query[Token](ctx).Where(colUserID.Eq(u.AuthID())).Delete()
	return err
}

// RevokeOtherTokens deletes every API token of u but keep (the
// request's, after a password change through the API, say: the API's
// "sign out other devices"). A keep of 0 keeps none.
func (a *Auth[U]) RevokeOtherTokens(ctx context.Context, u U, keep int64) error {
	_, err := db.Query[Token](ctx).Where(colUserID.Eq(u.AuthID()), colID.Ne(keep)).Delete()
	return err
}

// lookup finds the Token of a plain "<id>|<secret>" token.
func lookup(ctx context.Context, plain string) (*Token, error) {
	idText, secret, ok := strings.Cut(plain, "|")
	id, valid := parseID(idText)
	if !ok || !valid || secret == "" {
		return nil, nil
	}
	t, err := db.Find[Token](ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(hashSecret(secret))) != 1 {
		return nil, nil
	}
	if t.ExpiresAt != nil && !anetos.Now(ctx).Before(*t.ExpiresAt) {
		return nil, nil
	}
	return &t, nil
}

// TokenMiddleware signs in the user of the request's API token, sent as
// "Authorization: Bearer <token>". A request without one (or with another
// Authorization scheme) goes through as a guest: put [Auth.Require] after
// it to refuse those. One with an invalid, expired or revoked token gets
// 401. Use it on API routes, in a group of their own: without the session
// middleware and web.CSRF, which would refuse API clients' posts. API
// descriptions (package web/openapi) list that 401.
func (a *Auth[U]) TokenMiddleware(next http.Handler) http.Handler {
	return web.Documented(a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := stateFrom(r.Context())
		st.mu.Lock()
		st.bearer = true // Require's 401s name the scheme
		st.mu.Unlock()
		scheme, plain, found := strings.Cut(r.Header.Get("Authorization"), " ")
		if !found || !strings.EqualFold(scheme, "Bearer") {
			next.ServeHTTP(w, r) // no token (other schemes are someone else's)
			return
		}
		ctx := r.Context()
		t, err := lookup(ctx, strings.TrimSpace(plain))
		if err != nil {
			web.WriteError(w, r, err)
			return
		}
		if t == nil {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			web.WriteError(w, r, ErrUnauthenticated)
			return
		}
		u, err := a.users.ByID(ctx, t.UserID)
		if notFound(err) || err == nil && a.disabled(u) {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			web.WriteError(w, r, ErrUnauthenticated)
			return
		}
		if err != nil {
			web.WriteError(w, r, err)
			return
		}
		if now := a.now().UTC().Truncate(time.Microsecond); t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= time.Minute {
			// At most one write a minute per token.
			if _, err := db.Query[Token](ctx).Where(colID.Eq(t.ID)).
				Update(colLastUsed.Set(&now)); err != nil {
				a.log.Warn("auth: recording a token's use failed", "error", err)
			}
			t.LastUsedAt = &now
		}
		stateFrom(ctx).set(u, t)
		next.ServeHTTP(w, r)
	})), tokenDoc)
}

// tokenDoc is what TokenMiddleware tells API descriptions
// (web.Documented): a token sent may be refused.
var tokenDoc = web.MiddlewareDoc{Responses: map[int]string{
	http.StatusUnauthorized: "The API token is invalid, expired or revoked.",
}}

// CurrentToken returns the API token the request authenticated with, and
// whether it did.
func CurrentToken(ctx context.Context) (*Token, bool) {
	st := stateFrom(ctx)
	if st == nil {
		return nil, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.token, st.token != nil
}

// TokenCan reports whether the request may do ability: a request signed
// in with an API token needs the ability on the token; one signed in
// with a session (the app's own pages and front end) may do anything its
// user may. A guest may do nothing.
func TokenCan(ctx context.Context, ability string) bool {
	if t, ok := CurrentToken(ctx); ok && !t.Can(ability) {
		return false
	}
	return Check(ctx)
}
