// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/anetostest"
)

// sso is a fake OpenID Connect provider that signs in whoever it's told.
type sso struct {
	*httptest.Server
	mu    sync.Mutex
	codes map[string]map[string]any // code → ID token claims
}

func newSSO(t *testing.T) *sso {
	p := &sso{codes: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token"})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		claims := p.codes[r.PostForm.Get("code")]
		delete(p.codes, r.PostForm.Get("code"))
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if claims == nil || claims["challenge"] != base64.RawURLEncoding.EncodeToString(sum[:]) {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		delete(claims, "challenge")
		payload, _ := json.Marshal(claims)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at", "token_type": "Bearer",
			"id_token": "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".c2ln"})
	})
	p.Server = httptest.NewTLSServer(mux)
	t.Cleanup(p.Close)
	socialProviders = []social.Provider{social.OIDC("sso", p.URL)}
	socialOptions = []social.Option{social.WithHTTPClient(p.Client())}
	t.Cleanup(func() { socialProviders, socialOptions = []social.Provider{social.Google(), social.GitHub()}, nil })
	return p
}

// signIn follows "Sign in with sso" as the account, playing the
// provider's page, and returns the app's answer to the callback.
func (p *sso) signIn(t *testing.T, app *anetostest.App, subject, email string, verified bool) *anetostest.Response {
	t.Helper()
	res := app.Get("/auth/sso/redirect").AssertStatus(http.StatusSeeOther)
	u, _ := url.Parse(res.Header.Get("Location"))
	q := u.Query()
	p.mu.Lock()
	p.codes["c-"+subject] = map[string]any{"iss": p.URL, "aud": "client", "sub": subject, "exp": time.Now().Add(time.Hour).Unix(),
		"nonce": q.Get("nonce"), "email": email, "email_verified": verified, "name": "SSO user", "challenge": q.Get("code_challenge")}
	p.mu.Unlock()
	return app.Get("/auth/sso/callback?" + url.Values{"code": {"c-" + subject}, "state": {q.Get("state")}}.Encode())
}

func socialApp(t *testing.T) (*sso, *anetostest.App) {
	p := newSSO(t)
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"APP_URL": "https://app.test",
		"SOCIAL_SSO_CLIENT_ID": "client", "SOCIAL_SSO_CLIENT_SECRET": "secret"}))
	return p, app
}

// region: test-social
func TestSocialSignIn(t *testing.T) {
	p, app := socialApp(t)
	app.Get("/login").AssertSee(`href="/auth/sso/redirect"`)

	// A new account: a user is created, with the address verified.
	p.signIn(t, app, "s-1", "grace@example.com", true).AssertRedirect("/") // AUTH_HOME_URL
	app.Get("/dashboard").AssertSee("Hello, SSO user").AssertDontSee("Please verify")
	app.PostForm("/logout", nil)

	// The same account again: the linked user.
	p.signIn(t, app, "s-1", "grace@new.example", true).AssertRedirect("/")
	n, err := db.RawFirst[int64](app.Context(), "SELECT COUNT(*) FROM users")
	if err != nil || n != 1 {
		t.Errorf("users: %d, %v", n, err)
	}
}

// endregion

func TestSocialLinking(t *testing.T) {
	p, app := socialApp(t)
	verified := createUser(t, app, "Ada", "ada@example.com", false)
	now := time.Now().UTC()
	if _, err := db.Query[User](app.Context()).Where(colID.Eq(verified.ID)).Update(colVerified.Set(&now)); err != nil {
		t.Fatal(err)
	}
	createUser(t, app, "Mallory", "victim@example.com", false) // registered first, never verified

	// A verified address finds the existing, verified user.
	p.signIn(t, app, "s-ada", "ada@example.com", true).AssertRedirect("/")
	app.Get("/dashboard").AssertSee("Hello, Ada")
	app.PostForm("/logout", nil)

	// An unverified address at the provider finds no one.
	p.signIn(t, app, "s-x", "ada@example.com", false).AssertRedirect("/login").
		Follow().AssertSee("has no verified email address")

	// Nor does an address no one verified here (a pre-registered account).
	p.signIn(t, app, "s-victim", "victim@example.com", true).AssertRedirect("/login").
		Follow().AssertSee("An account with this email address exists")
}
