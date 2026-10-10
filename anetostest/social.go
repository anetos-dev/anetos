// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/internal/socialstub"
)

// FakeSocial makes social login (package auth/social) log in through a
// stand-in provider the test controls, for every provider the app has:
// [App.SocialLogin] logs in with an account of your choosing. Every
// provider counts as configured (social.Configured), with test
// credentials when its SOCIAL_<NAME>_* settings are missing. Nothing
// reaches Google or GitHub.
//
//	app := anetostest.New(t, setup, anetostest.FakeSocial())
//	app.SocialLogin("/auth/google/redirect", anetostest.SocialAccount{
//		ID: "g-1", Email: "ada@example.com", EmailVerified: true, Name: "Ada",
//	}).AssertRedirect("/dashboard")
func FakeSocial() Option { return func(o *options) { o.fakeSocial = true } }

// SocialAccount is the account a test logs in with at the stand-in
// provider ([App.SocialLogin]): the profile the app's resolver gets.
type SocialAccount struct {
	// ID is the account's identifier at the provider (the profile's
	// Subject). Required.
	ID string
	// Email is the account's address.
	Email string
	// EmailVerified says the provider verified Email.
	EmailVerified bool
	// Name is the account's display name.
	Name string
	// AvatarURL is the account's picture.
	AvatarURL string
}

// idp is the stand-in OpenID Connect provider of [FakeSocial].
type idp struct {
	srv   *httptest.Server
	mu    sync.Mutex
	codes map[string]idpCode // authorization code → its login
}

type idpCode struct {
	claims    map[string]any
	challenge string // PKCE S256 challenge
}

// startIDP runs the stand-in provider and has the app's social login use
// it.
func (a *App) startIDP() {
	p := &idp{codes: map[string]idpCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "anetostest: log in with app.SocialLogin", http.StatusBadRequest)
	})
	mux.HandleFunc("POST /token", p.token)
	p.srv = httptest.NewTLSServer(mux)
	a.t.Cleanup(p.srv.Close)
	a.idp = p
	anetos.Provide(a.App, &socialstub.Stub{Issuer: p.srv.URL, Client: p.srv.Client()})
}

// token is the token endpoint: it trades a code for an ID token (not
// signed: package social trusts what comes from the token endpoint over
// TLS).
func (p *idp) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	c, ok := p.codes[code]
	delete(p.codes, code) // one use
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	w.Header().Set("Content-Type", "application/json")
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	payload, _ := json.Marshal(c.claims)
	idToken := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "."
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-access-token", "token_type": "Bearer",
		"expires_in": 3600, "id_token": idToken + "c2ln"})
}

// SocialLogin logs in with acct through social login, as a browser
// would: it follows redirect (the app's route that sends users to the
// provider, "/auth/google/redirect", with ?remember=1 if you like), plays
// the provider's login page for acct, and returns the app's answer to
// the callback: a redirect to the intended page or AUTH_HOME_URL, or back
// to the login page with a "social" error. It needs [FakeSocial].
func (a *App) SocialLogin(redirect string, acct SocialAccount) *Response {
	a.t.Helper()
	if a.idp == nil {
		a.t.Fatalf("anetostest: SocialLogin needs the FakeSocial option")
		return nil
	}
	if acct.ID == "" {
		a.t.Fatalf("anetostest: SocialLogin: the account needs an ID")
		return nil
	}
	res := a.Get(redirect)
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, a.idp.srv.URL+"/authorize?") {
		a.t.Fatalf("anetostest: SocialLogin: GET %s didn't redirect to the provider (status %d, Location %q)", redirect, res.StatusCode, loc)
		return nil
	}
	u, _ := url.Parse(loc)
	q := u.Query()
	callback, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || callback.Path == "" {
		a.t.Fatalf("anetostest: SocialLogin: bad redirect_uri %q", q.Get("redirect_uri"))
		return nil
	}
	now := a.Now()
	claims := map[string]any{"iss": a.idp.srv.URL, "aud": q.Get("client_id"), "sub": acct.ID,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": q.Get("nonce"),
		"email_verified": acct.EmailVerified}
	for k, v := range map[string]string{"email": acct.Email, "name": acct.Name, "picture": acct.AvatarURL} {
		if v != "" {
			claims[k] = v
		}
	}
	code := "code-" + rand.Text()
	a.idp.mu.Lock()
	a.idp.codes[code] = idpCode{claims: claims, challenge: q.Get("code_challenge")}
	a.idp.mu.Unlock()
	return a.Get(callback.EscapedPath() + "?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode())
}

// SocialSignIn is [App.SocialLogin].
//
// Deprecated: Use SocialLogin; SocialSignIn is removed in v0.6.
//
//go:fix inline
func (a *App) SocialSignIn(redirect string, acct SocialAccount) *Response {
	return a.SocialLogin(redirect, acct)
}
