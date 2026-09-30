// SPDX-License-Identifier: Apache-2.0

package social_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
	"golang.org/x/oauth2"
)

type user struct {
	ID, Email, Remember string
}

func (u *user) AuthID() string     { return u.ID }
func (*user) AuthPassword() string { return "" }

// users is an in-memory user table, filled by the resolver.
type users struct {
	mu      sync.Mutex
	byID    map[string]*user
	seen    []social.Profile
	nilUser bool
}

func (s *users) auth() auth.Users[*user] {
	return auth.Users[*user]{
		ByID: func(_ context.Context, id string) (*user, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if u, ok := s.byID[id]; ok {
				c := *u
				return &c, nil
			}
			return nil, auth.ErrNoUser
		},
		ByLogin:       func(context.Context, string) (*user, error) { return nil, auth.ErrNoUser },
		RememberToken: func(u *user) string { return u.Remember },
		SetRememberToken: func(_ context.Context, u *user, tok string) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.byID[u.ID].Remember = tok
			return nil
		},
	}
}

func (s *users) resolve(_ context.Context, p social.Profile) (*user, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, p)
	if s.nilUser {
		return nil, nil
	}
	if !p.EmailVerified {
		return nil, &social.ErrNoAccount{Message: "Your account has no verified email address."}
	}
	id := p.Provider + ":" + p.Subject
	if _, ok := s.byID[id]; !ok {
		s.byID[id] = &user{ID: id, Email: p.Email}
	}
	return s.byID[id], nil
}

// provider is a fake OpenID Connect provider.
type provider struct {
	*httptest.Server
	mu         sync.Mutex
	challenges map[string]string // code → PKCE challenge
	nonces     map[string]string // code → nonce
	claims     map[string]any    // overrides for the next ID token
	email      bool              // email_verified
	discovery  map[string]string // overrides for the discovery document
}

func newProvider(t *testing.T) *provider {
	p := &provider{challenges: map[string]string{}, nonces: map[string]string{}, email: true}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		doc := map[string]string{"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token"}
		p.mu.Lock()
		maps.Copy(doc, p.discovery)
		p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, secret, _ := r.BasicAuth()
		code, verifier := r.PostForm.Get("code"), r.PostForm.Get("code_verifier")
		sum := sha256.Sum256([]byte(verifier))
		p.mu.Lock()
		defer p.mu.Unlock()
		if id != "client-1" || secret != "secret-1" || p.challenges[code] == "" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenges[code] {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		claims := map[string]any{"iss": p.URL, "sub": "u-42", "aud": "client-1", "exp": time.Now().Add(time.Hour).Unix(),
			"iat": time.Now().Unix(), "nonce": p.nonces[code], "email": "ada@example.com", "email_verified": p.email, "name": "Ada"}
		maps.Copy(claims, p.claims)
		delete(p.challenges, code) // one use
		payload, _ := json.Marshal(claims)
		idToken := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".c2ln"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idToken})
	})
	p.Server = httptest.NewTLSServer(mux)
	t.Cleanup(p.Close)
	return p
}

// authorize plays the provider's sign-in page: it reads the request the
// app redirected to and returns the callback URL it would redirect back to.
func (p *provider) authorize(t *testing.T, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != "client-1" || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
		t.Fatalf("authorization request: %s", location)
	}
	code := "code-" + q.Get("state")[:8]
	p.mu.Lock()
	p.challenges[code] = q.Get("code_challenge")
	p.nonces[code] = q.Get("nonce")
	p.mu.Unlock()
	cb, _ := url.Parse(q.Get("redirect_uri"))
	cb.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	return cb.String()
}

type browser struct {
	t   *testing.T
	h   http.Handler
	ctx context.Context
	jar *cookiejar.Jar
}

var base, _ = url.Parse("https://app.test/")

type response struct {
	code     int
	location string
	body     string
	header   http.Header
}

func (b *browser) get(target string) response {
	b.t.Helper()
	u, _ := url.Parse(target)
	u = base.ResolveReference(u)
	r := httptest.NewRequestWithContext(b.ctx, http.MethodGet, u.String(), nil)
	r.Header.Set("Accept", "text/html")
	for _, c := range b.jar.Cookies(base) {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	b.jar.SetCookies(base, (&http.Response{Header: w.Header()}).Cookies())
	return response{w.Code, w.Header().Get("Location"), w.Body.String(), w.Header()}
}

// lastSocial is the Social the last setup made.
var lastSocial *social.Social[*user]

func setup(t *testing.T, p *provider, providers ...social.Provider) (*users, *browser) {
	t.Helper()
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if _, err := cache.ForApp(app); err != nil {
		t.Fatal(err)
	}
	sessions, err := session.ForApp(app)
	if err != nil {
		t.Fatal(err)
	}
	store := &users{byID: map[string]*user{}}
	a, err := auth.ForApp(app, store.auth())
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) == 0 {
		providers = []social.Provider{social.OIDC("fake", p.URL)}
	}
	creds := map[string]social.Credentials{}
	for _, pr := range providers {
		creds[pr.Name] = social.Credentials{ClientID: "client-1", ClientSecret: "secret-1"}
	}
	s, err := social.New(a, store.resolve, "https://app.test", creds, providers, social.WithHTTPClient(p.Client()))
	if err != nil {
		t.Fatal(err)
	}
	lastSocial = s
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	r := srv.Router().Group("", sessions.Middleware, a.Middleware)
	r.Get("/login", func(c *web.Ctx) error { return c.Text(http.StatusOK, "login: "+view.Errors(c).Get("social")) })
	r.Get("/auth/{provider}/redirect", s.Redirect)
	r.Get("/auth/{provider}/callback", s.Callback)
	r.Group("", a.Require).Get("/home", func(c *web.Ctx) error {
		u, _ := auth.User[*user](c)
		return c.Text(http.StatusOK, "hello "+u.Email)
	})
	jar, _ := cookiejar.New(nil)
	return store, &browser{t: t, h: srv.Router(), ctx: app.Context(context.Background()), jar: jar}
}

func signIn(t *testing.T, p *provider, b *browser, provider string) response {
	t.Helper()
	res := b.get("/auth/" + provider + "/redirect")
	if res.code != http.StatusSeeOther {
		t.Fatalf("redirect: %d %s", res.code, res.body)
	}
	return b.get(p.authorize(t, res.location))
}

func TestOIDCSignIn(t *testing.T) {
	p := newProvider(t)
	store, b := setup(t, p)
	b.get("/login") // a session before the sign-in
	before := sessionCookie(b)
	res := signIn(t, p, b, "fake")
	if after := sessionCookie(b); after == "" || after == before {
		t.Error("the session wasn't regenerated at sign-in")
	}
	if res.code != http.StatusSeeOther || res.location != "/" {
		t.Fatalf("callback: %d %s", res.code, res.location)
	}
	if got := b.get("/home"); got.body != "hello ada@example.com" {
		t.Errorf("signed in: %d %q", got.code, got.body)
	}
	prof := store.seen[0]
	if prof.Provider != "fake" || prof.Subject != "u-42" || !prof.EmailVerified || prof.Name != "Ada" || prof.Token.AccessToken != "at" {
		t.Errorf("profile: %+v", prof)
	}
}

func TestCallbackChecks(t *testing.T) {
	p := newProvider(t)
	store, b := setup(t, p)
	fails := func(name, target, want string) {
		t.Helper()
		res := b.get(target)
		if res.code != http.StatusSeeOther || res.location != "/login" {
			t.Errorf("%s: %d %s", name, res.code, res.location)
			return
		}
		if msg := b.get("/login").body; !strings.Contains(msg, want) {
			t.Errorf("%s: login page says %q", name, msg)
		}
	}

	// A callback without a sign-in started from this session, or a replay.
	res := b.get("/auth/fake/redirect")
	cb := p.authorize(t, res.location)
	forged, _ := url.Parse(cb)
	q := forged.Query()
	q.Set("state", "forged")
	forged.RawQuery = q.Encode()
	fails("forged state", forged.String(), "didn't complete")
	fails("state used", cb, "didn't complete") // the forged attempt used up the flow

	// The user cancels at the provider.
	res = b.get("/auth/fake/redirect")
	u, _ := url.Parse(res.location)
	fails("denied", "/auth/fake/callback?error=access_denied&state="+u.Query().Get("state"), "canceled")

	// ID tokens that must be refused.
	for name, claims := range map[string]map[string]any{
		"wrong audience":  {"aud": "someone-else"},
		"wrong issuer":    {"iss": "https://evil.example"},
		"expired":         {"exp": time.Now().Add(-time.Hour).Unix()},
		"wrong nonce":     {"nonce": "replayed"},
		"no subject":      {"sub": ""},
		"several aud":     {"aud": []string{"client-1", "other"}},
		"azp for another": {"azp": "other-client"},
		"no expiry":       {"exp": 0},
		"future iat":      {"iat": time.Now().Add(time.Hour).Unix()},
	} {
		p.mu.Lock()
		p.claims = claims
		p.mu.Unlock()
		fails(name, p.authorize(t, b.get("/auth/fake/redirect").location), "couldn't sign you in with that account")
	}
	p.claims = nil

	// The resolver refuses.
	p.email = false
	fails("refused", p.authorize(t, b.get("/auth/fake/redirect").location), "no verified email")
	if len(store.byID) != 0 {
		t.Errorf("users created: %v", store.byID)
	}
	// An unknown provider.
	if res := b.get("/auth/nope/redirect"); res.code != http.StatusNotFound {
		t.Errorf("unknown provider: %d", res.code)
	}
}

func TestRemember(t *testing.T) {
	p := newProvider(t)
	_, b := setup(t, p)
	res := b.get("/auth/fake/redirect?remember=1")
	b.get(p.authorize(t, res.location))
	found := false
	for _, c := range b.jar.Cookies(base) {
		found = found || c.Name == "anetos_remember"
	}
	if !found {
		t.Error("remember=1 set no remember-me cookie")
	}
}

func TestDiscovery(t *testing.T) {
	for name, doc := range map[string]map[string]string{
		"mismatched issuer":   {"issuer": "https://evil.example"},
		"http token endpoint": {"token_endpoint": "http://plain.example/token"},
	} {
		p := newProvider(t)
		p.discovery = doc
		_, b := setup(t, p)
		if res := b.get("/auth/fake/redirect"); res.location != "/login" {
			t.Errorf("%s: %d %s", name, res.code, res.location)
		}
	}

	// A failure is remembered for a while, then tried again.
	p := newProvider(t)
	p.discovery = map[string]string{"issuer": "https://evil.example"}
	_, b := setup(t, p)
	now := time.Now()
	social.SetNow(lastSocial, func() time.Time { return now })
	b.get("/auth/fake/redirect")
	p.mu.Lock()
	p.discovery = nil
	p.mu.Unlock()
	if res := b.get("/auth/fake/redirect"); res.location != "/login" {
		t.Errorf("within the backoff: %s", res.location)
	}
	now = now.Add(31 * time.Second)
	if res := b.get("/auth/fake/redirect"); !strings.HasPrefix(res.location, p.URL+"/authorize?") {
		t.Errorf("after the backoff: %s", res.location)
	}

	// Trailing slashes are part of the issuer (Auth0's has one).
	p = newProvider(t)
	p.discovery = map[string]string{"issuer": p.URL + "/"}
	p.claims = map[string]any{"iss": p.URL + "/"}
	_, b = setup(t, p, social.OIDC("fake", p.URL+"/"))
	if res := signIn(t, p, b, "fake"); res.location != "/" {
		t.Errorf("issuer with a trailing slash: %d %s", res.code, res.location)
	}
}

func TestInsecureRedirects(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"at","token_type":"Bearer","id_token":"x.e30.x"}`)
	}))
	defer plain.Close()
	// A token endpoint redirecting to http is refused.
	p := newProvider(t)
	mux := http.NewServeMux()
	mux.Handle("/", p.Config.Handler)
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/token", http.StatusTemporaryRedirect)
	})
	p.Config.Handler = mux
	store, b := setup(t, p)
	res := signIn(t, p, b, "fake")
	if res.location != "/login" || len(store.seen) != 0 {
		t.Errorf("redirect to http: %d %s, profiles %v", res.code, res.location, store.seen)
	}
	// So is a discovery document that redirects to http.
	p = newProvider(t)
	mux = http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/doc", http.StatusFound)
	})
	p.Config.Handler = mux
	_, b = setup(t, p)
	if res := b.get("/auth/fake/redirect"); res.location != "/login" {
		t.Errorf("discovery redirected to http: %s", res.location)
	}
}

func TestSlowDiscovery(t *testing.T) {
	p := newProvider(t)
	release := make(chan struct{})
	inner := p.Config.Handler
	p.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "openid-configuration") {
			<-release
		}
		inner.ServeHTTP(w, r)
	})
	_, b := setup(t, p)
	// Requests waiting for the discovery give up with their context.
	ctx, cancel := context.WithTimeout(b.ctx, 50*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			waiter := &browser{t: t, h: b.h, ctx: ctx, jar: b.jar}
			waiter.get("/auth/fake/redirect")
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("requests waited for the discovery past their context")
	}
	close(release)
}

func TestGitHub(t *testing.T) {
	p := newProvider(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"gh","token_type":"bearer"}`)
	})
	mux.HandleFunc("GET /api/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"id":7,"login":"octo","name":"","avatar_url":"https://a/7"}`)
	})
	mux.HandleFunc("GET /api/user/emails", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"email":"old@example.com","primary":false,"verified":true},{"email":"octo@example.com","primary":true,"verified":true}]`)
	})
	gh := httptest.NewTLSServer(mux)
	defer gh.Close()
	p.Server = gh // the browser's client trusts it
	store, b := setup(t, p, social.GitHubAt(gh.URL, gh.URL+"/api"))
	res := b.get("/auth/github/redirect")
	u, _ := url.Parse(res.location)
	q := u.Query()
	if q.Get("scope") != "read:user user:email" || q.Get("nonce") != "" {
		t.Errorf("GitHub authorization request: %s", res.location)
	}
	cb := "/auth/github/callback?" + url.Values{"code": {"c"}, "state": {q.Get("state")}}.Encode()
	if res := b.get(cb); res.code != http.StatusSeeOther || res.location != "/" {
		t.Fatalf("callback: %d %s", res.code, res.location)
	}
	prof := store.seen[0]
	if prof.Subject != "7" || prof.Email != "octo@example.com" || !prof.EmailVerified || prof.Name != "octo" {
		t.Errorf("GitHub profile: %+v", prof)
	}
}

func TestConfigErrors(t *testing.T) {
	a := &auth.Auth[*user]{}
	resolve := func(context.Context, social.Profile) (*user, error) { return nil, errors.New("x") }
	creds := map[string]social.Credentials{"google": {ClientID: "i", ClientSecret: "s"}}
	cases := map[string]struct {
		base      string
		creds     map[string]social.Credentials
		providers []social.Provider
		want      string
	}{
		"no APP_URL":   {"", creds, []social.Provider{social.Google()}, "APP_URL"},
		"no secret":    {"https://app.test", map[string]social.Credentials{"google": {ClientID: "i"}}, []social.Provider{social.Google()}, "SOCIAL_GOOGLE_CLIENT_SECRET"},
		"bad name":     {"https://app.test", creds, []social.Provider{{Name: "Google!"}}, "lower-case"},
		"twice":        {"https://app.test", creds, []social.Provider{social.Google(), social.Google()}, "twice"},
		"http auth":    {"https://app.test", map[string]social.Credentials{"x": {ClientID: "i", ClientSecret: "s"}}, []social.Provider{{Name: "x", Issuer: "https://x", Endpoint: oauth2.Endpoint{AuthURL: "http://x/a", TokenURL: "https://x/t"}}}, "isn't an https URL"},
		"http api":     {"https://app.test", map[string]social.Credentials{"github": {ClientID: "i", ClientSecret: "s"}}, []social.Provider{social.GitHubAt("https://ghe", "http://ghe/api")}, "isn't an https URL"},
		"APP_URL path": {"https://app.test/app", creds, []social.Provider{social.Google()}, "APP_URL"},
	}
	for name, c := range cases {
		if _, err := social.New(a, resolve, c.base, c.creds, c.providers); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	s, err := social.New(a, resolve, "https://app.test/", creds, []social.Provider{social.Google()})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.CallbackURL("google"); got != "https://app.test/auth/google/callback" {
		t.Errorf("CallbackURL = %q", got)
	}
	if _, err := social.New(a, resolve, "https://app.test", creds, []social.Provider{social.Google()}, social.WithHTTPClient(nil)); err != nil {
		t.Errorf("WithHTTPClient(nil): %v", err)
	}
}

func TestForAppAndConfigured(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(),
		"APP_URL": "https://app.test", "SOCIAL_GITHUB_CLIENT_ID": "id", "SOCIAL_GITHUB_CLIENT_SECRET": "secret",
		"SOCIAL_MY_SSO_CLIENT_ID": "only-id"}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err := cache.ForApp(app); err != nil {
		t.Fatal(err)
	}
	store := &users{byID: map[string]*user{}}
	a, err := auth.ForApp(app, store.auth())
	if err != nil {
		t.Fatal(err)
	}
	got := social.Configured(app, social.Google(), social.GitHub(), social.OIDC("my-sso", "https://sso.example"))
	if len(got) != 1 || got[0].Name != "github" {
		t.Fatalf("Configured = %v", got)
	}
	s, err := social.ForApp(app, a, store.resolve, got)
	if err != nil {
		t.Fatal(err)
	}
	if names := s.Providers(); len(names) != 1 || names[0] != "github" {
		t.Errorf("Providers = %v", names)
	}
	if _, err := social.ForApp(app, a, store.resolve, []social.Provider{social.Google()}); err == nil || !strings.Contains(err.Error(), "SOCIAL_GOOGLE_CLIENT_ID") {
		t.Errorf("unconfigured provider: %v", err)
	}
}

func sessionCookie(b *browser) string {
	for _, c := range b.jar.Cookies(base) {
		if c.Name == "anetos_session" {
			return c.Value
		}
	}
	return ""
}

func TestFlowExpiryAndErrors(t *testing.T) {
	p := newProvider(t)
	_, b := setup(t, p)
	now := time.Now()
	social.SetNow(lastSocial, func() time.Time { return now })
	res := b.get("/auth/fake/redirect")
	cb := p.authorize(t, res.location)
	now = now.Add(11 * time.Minute) // took too long at the provider
	if res := b.get(cb); res.location != "/login" {
		t.Errorf("expired flow: %s", res.location)
	}
	now = time.Now()
	res = b.get("/auth/fake/redirect")
	u, _ := url.Parse(res.location)
	b.get("/auth/fake/callback?error=server_error&state=" + u.Query().Get("state"))
	if msg := b.get("/login").body; !strings.Contains(msg, "reported an error") {
		t.Errorf("provider error: %q", msg)
	}
}

func TestResolverWithoutUser(t *testing.T) {
	p := newProvider(t)
	store, b := setup(t, p)
	store.nilUser = true // a Resolver returning (nil, nil)
	if res := signIn(t, p, b, "fake"); res.location != "/login" {
		t.Errorf("no user: %d %s", res.code, res.location)
	}
}

func TestDiscoveryRefresh(t *testing.T) {
	p := newProvider(t)
	_, b := setup(t, p)
	now := time.Now()
	social.SetNow(lastSocial, func() time.Time { return now })
	if res := b.get("/auth/fake/redirect"); !strings.HasPrefix(res.location, p.URL+"/authorize?") {
		t.Fatalf("first: %s", res.location)
	}
	// A day later the provider moved its endpoints: the next requests use
	// the old ones while the document is read again, then the new ones.
	p.mu.Lock()
	p.discovery = map[string]string{"authorization_endpoint": p.URL + "/v2/authorize"}
	p.mu.Unlock()
	now = now.Add(25 * time.Hour)
	b.get("/auth/fake/redirect")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if res := b.get("/auth/fake/redirect"); strings.HasPrefix(res.location, p.URL+"/v2/authorize?") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the discovery document wasn't read again")
}
