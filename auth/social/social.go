// SPDX-License-Identifier: Apache-2.0

// Package social signs users in with an account at another service:
// Google, GitHub, or any OpenID Connect provider. It runs the OAuth 2.0
// authorization-code flow (with PKCE, state and, for OpenID Connect, a
// nonce), and hands the account's profile to an app function that finds
// or creates the user, whom package auth then signs in.
//
//	s, err := social.New(app, a, findOrCreate, social.Configured(app, social.Google(), social.GitHub()))
//	guests.Get("/auth/{provider}/redirect", s.Redirect)
//	guests.Get("/auth/{provider}/callback", s.Callback)
//
// Client IDs and secrets come from SOCIAL_<NAME>_CLIENT_ID and
// SOCIAL_<NAME>_CLIENT_SECRET, and callback URLs from APP_URL. See
// docs/site/guides/social-login.md.
package social

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/internal/socialstub"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
	"golang.org/x/oauth2"
)

// Profile is what a provider says about the account that signed in.
type Profile struct {
	// Provider is the provider's name ("google").
	Provider string
	// Subject is the account's stable identifier at the provider. Link
	// users by it, not by email: addresses change and can be reused.
	Subject string
	// Email is the account's address, if the provider shared it.
	Email string
	// EmailVerified reports whether the provider verified Email. Only a
	// verified address may be used to find an existing user.
	EmailVerified bool
	// Name is the account's display name.
	Name string
	// AvatarURL is the account's picture.
	AvatarURL string
	// Token is the provider's token, for calling its API on the user's
	// behalf (with the scopes asked for).
	Token *oauth2.Token
}

// Provider is a service users sign in with. [Google], [GitHub] and [OIDC]
// return the built-in ones; their fields can be changed (Scopes, say)
// before they are passed to [New].
type Provider struct {
	// Name is the provider's name in URLs and settings: lower-case
	// letters, digits, "-" and "_".
	Name string
	// Title is its name for people, on sign-in buttons ("Google");
	// [Social.Title] falls back to Name.
	Title string
	// Endpoint is the provider's OAuth 2.0 authorization and token URLs.
	// Empty for OpenID Connect providers found by discovery.
	Endpoint oauth2.Endpoint
	// Scopes are the scopes asked for.
	Scopes []string
	// Issuer is the OpenID Connect issuer, or "" for plain OAuth 2.0
	// providers (GitHub). With it, the flow asks for an ID token and
	// checks it.
	Issuer string
	// Issuers are other issuer values the ID token may carry (Google
	// uses "accounts.google.com" as well as its URL).
	Issuers []string
	// Discover says to read Endpoint from the issuer's
	// /.well-known/openid-configuration.
	Discover bool
	// Profile reads the profile of a plain OAuth 2.0 provider with its
	// token; nil for OpenID Connect providers, whose ID token has it.
	Profile func(ctx context.Context, client *http.Client, tok *oauth2.Token) (Profile, error)
	// API is the base URL of the provider's API that Profile calls, if
	// any, so that it is checked to be https like the endpoints.
	API string
}

// provider is a Provider with its credentials and discovered endpoints.
type provider struct {
	Provider
	clientID, secret string

	mu       sync.Mutex
	endpoint oauth2.Endpoint // discovered
	loading  chan struct{}   // closed when the discovery in progress ends
	found    bool
	foundAt  time.Time // when discovery last succeeded: refreshed after discoveryTTL
	failed   error     // the last discovery's error
	failedAt time.Time // when it failed: retried after discoveryBackoff
}

// Google is Google's sign-in (OpenID Connect), asking for the email
// address and profile.
func Google() Provider {
	return Provider{
		Name:  "google",
		Title: "Google",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
		Scopes:  []string{"openid", "email", "profile"},
		Issuer:  "https://accounts.google.com",
		Issuers: []string{"accounts.google.com"},
	}
}

// OIDC is any OpenID Connect provider (Okta, Auth0, Microsoft Entra ID,
// Keycloak, GitLab…), whose endpoints are read from the issuer's
// discovery document when first needed.
//
//	social.OIDC("okta", "https://example.okta.com")
//
// The issuer must be exactly the "issuer" of the provider's discovery
// document and of its ID tokens, trailing slash included (Auth0's has
// one). Use a tenant's own issuer, not a multi-tenant one (Microsoft
// Entra ID's /common). Providers that don't send email_verified
// (Microsoft Entra ID, by default) give profiles whose address isn't
// verified.
//
// Set its Title for sign-in buttons ("Okta"); it defaults to name.
func OIDC(name, issuer string) Provider {
	return Provider{Name: name, Issuer: issuer, Discover: true, Scopes: []string{"openid", "email", "profile"}}
}

// Resolver finds the app's user for a profile, creating or linking one as
// the app decides; see [FindLink] and [Link]. An error signs no one in;
// return [ErrNoAccount] to refuse with a message on the login page.
type Resolver[U auth.Authenticatable] func(ctx context.Context, p Profile) (U, error)

// ErrNoAccount is what a Resolver returns to refuse a sign-in: the user is
// sent back to the login page with Message as the "social" field error.
type ErrNoAccount struct {
	// Message is shown on the login page.
	Message string
}

// Error implements error.
func (e *ErrNoAccount) Error() string { return "social: " + e.Message }

// Social runs the sign-in flow for an app's providers.
type Social[U auth.Authenticatable] struct {
	auth      *auth.Auth[U]
	resolve   Resolver[U]
	providers map[string]*provider
	order     []string // provider names, as given
	baseURL   string
	callback  string // path, with {provider}
	home      string // where Callback goes without an intended page; "" for AUTH_HOME_URL
	client    *http.Client
	log       *slog.Logger
	now       func() time.Time
}

// Option configures [NewWithConfig].
type Option func(*options)

type options struct {
	client   *http.Client
	log      *slog.Logger
	callback string
	home     string
}

// WithHTTPClient sets the client for requests to providers. Default: one
// with a 10-second timeout. Keep its TLS verification on: ID tokens are
// trusted because they come over TLS. Redirects to anything but https are
// refused.
func WithHTTPClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithLogger sets the logger. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.log = l } }

// WithCallbackPath sets the path of the callback route, with
// "{provider}" where the provider's name goes. Default
// "/auth/{provider}/callback".
func WithCallbackPath(p string) Option { return func(o *options) { o.callback = p } }

// WithHomeURL sets where users go after signing in, when there's no page
// they were trying to reach (auth.Intended): a path of the app, such as
// "/dashboard". Default AUTH_HOME_URL.
func WithHomeURL(path string) Option { return func(o *options) { o.home = path } }

// newOptions applies opts to the defaults and checks the result.
func newOptions(opts []Option) (options, error) {
	o := options{client: &http.Client{Timeout: 10 * time.Second}, log: slog.Default(), callback: "/auth/{provider}/callback"}
	for _, opt := range opts {
		opt(&o)
	}
	if !strings.HasPrefix(o.callback, "/") || !strings.Contains(o.callback, "{provider}") {
		return o, fmt.Errorf("social: callback path %q must start with / and contain {provider}", o.callback)
	}
	if o.home != "" && !localPath(o.home) {
		return o, fmt.Errorf("social: WithHomeURL(%q): give a path of the app, such as /dashboard", o.home)
	}
	return o, nil
}

// localPath reports whether u is a path of this site: not another
// site's URL ("//host", "/\\host"), and without control characters,
// which browsers drop ("/\t/host").
func localPath(u string) bool {
	return strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.HasPrefix(u, "/\\") &&
		!strings.ContainsFunc(u, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// Credentials are a provider's client ID and secret, registered with it.
type Credentials struct {
	// ClientID identifies the app to the provider.
	ClientID string
	// ClientSecret authenticates the app to the provider.
	ClientSecret anetos.Secret
}

// NewWithConfig returns a Social signing users in with a, for providers with the
// credentials given by name; baseURL is the app's public URL (APP_URL),
// to which the callback path is added for the redirect URIs registered
// with the providers.
func NewWithConfig[U auth.Authenticatable](a *auth.Auth[U], resolve Resolver[U], baseURL string, creds map[string]Credentials, providers []Provider, opts ...Option) (*Social[U], error) {
	o, err := newOptions(opts)
	if err != nil {
		return nil, err
	}
	if a == nil || resolve == nil {
		return nil, errors.New("social: nil Auth or Resolver")
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("social: APP_URL %q must be the app's public URL (https://example.com): providers send users back to it", baseURL)
	}
	s := &Social[U]{auth: a, resolve: resolve, providers: map[string]*provider{}, baseURL: strings.TrimSuffix(baseURL, "/"),
		callback: o.callback, home: o.home, client: httpsOnly(o.client), log: o.log, now: time.Now}
	for _, p := range providers {
		if !validName(p.Name) {
			return nil, fmt.Errorf("social: provider name %q must be lower-case letters, digits, - and _", p.Name)
		}
		if _, dup := s.providers[p.Name]; dup {
			return nil, fmt.Errorf("social: provider %q given twice", p.Name)
		}
		c := creds[p.Name]
		if c.ClientID == "" || c.ClientSecret == "" {
			env := envName(p.Name)
			return nil, fmt.Errorf("social: set %s_CLIENT_ID and %s_CLIENT_SECRET for %s", env, env, p.Name)
		}
		if p.Issuer == "" && p.Profile == nil {
			return nil, fmt.Errorf("social: provider %q needs an Issuer (OpenID Connect) or a Profile function", p.Name)
		}
		if !p.Discover && (p.Endpoint.AuthURL == "" || p.Endpoint.TokenURL == "") {
			return nil, fmt.Errorf("social: provider %q has no endpoints", p.Name)
		}
		for _, u := range []string{p.Endpoint.AuthURL, p.Endpoint.TokenURL, p.API, p.Issuer} {
			if pu, err := url.Parse(u); u != "" && (err != nil || pu.Scheme != "https" || pu.Host == "") {
				return nil, fmt.Errorf("social: provider %q: %q isn't an https URL", p.Name, u)
			}
		}
		s.providers[p.Name] = &provider{Provider: p, clientID: c.ClientID, secret: string(c.ClientSecret)}
		s.order = append(s.order, p.Name)
	}
	return s, nil
}

// New returns a Social for the app's Auth a, with the credentials of
// each provider from SOCIAL_<NAME>_CLIENT_ID and SOCIAL_<NAME>_CLIENT_SECRET
// ("SOCIAL_GOOGLE_CLIENT_ID") and callback URLs under APP_URL (https in
// production). Pass the providers through [Configured] to use only those
// with credentials; with none, the routes answer 404.
//
//	s, err := social.New(app, a, findOrCreate, social.Configured(app, social.Google(), social.GitHub()))
func New[U auth.Authenticatable](app *anetos.App, a *auth.Auth[U], resolve Resolver[U], providers []Provider, opts ...Option) (*Social[U], error) {
	if _, ok := anetos.Lookup[*Social[U]](app); ok {
		return nil, errors.New("social: New called twice for one app")
	}
	creds := map[string]Credentials{}
	for _, p := range providers {
		creds[p.Name] = credentials(app, p.Name)
	}
	if _, err := newOptions(opts); err != nil { // also when no provider is configured yet
		return nil, err
	}
	if stub, ok := anetos.Lookup[*socialstub.Stub](app); ok && stub != nil {
		if !app.Config().Env.IsTesting() {
			return nil, errors.New("social: a test stand-in provider (anetostest.FakeSocial) outside APP_ENV=testing")
		}
		providers = replace(stub, providers, creds)
		opts = append(slices.Clone(opts), WithHTTPClient(stub.Client))
	}
	if len(providers) > 0 && app.Config().Env.IsProduction() && !strings.HasPrefix(app.Config().URL, "https://") {
		return nil, fmt.Errorf("social: APP_URL %q must be https in production", app.Config().URL)
	}
	if len(providers) == 0 {
		s := &Social[U]{auth: a, resolve: resolve, providers: map[string]*provider{}, now: app.Now, log: app.Logger().With("component", "social")}
		anetos.Provide(app, s)
		return s, nil
	}
	opts = append([]Option{WithLogger(app.Logger().With("component", "social"))}, opts...)
	s, err := NewWithConfig(a, resolve, app.Config().URL, creds, providers, opts...)
	if err != nil {
		return nil, err
	}
	s.now = app.Now // tests can freeze it
	anetos.Provide(app, s)
	return s, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp[U auth.Authenticatable](app *anetos.App, a *auth.Auth[U], resolve Resolver[U], providers []Provider, opts ...Option) (*Social[U], error) {
	return New[U](app, a, resolve, providers, opts...)
}

// Configured returns the providers whose SOCIAL_<NAME>_CLIENT_ID and
// SOCIAL_<NAME>_CLIENT_SECRET are set, so that a provider is enabled by
// its settings.
//
// In tests with anetostest.FakeSocial, every provider is configured.
func Configured(app *anetos.App, providers ...Provider) []Provider {
	if stub, ok := anetos.Lookup[*socialstub.Stub](app); ok && stub != nil && app.Config().Env.IsTesting() {
		return slices.Clone(providers)
	}
	var out []Provider
	for _, p := range providers {
		if c := credentials(app, p.Name); c.ClientID != "" && c.ClientSecret != "" {
			out = append(out, p)
		}
	}
	return out
}

func credentials(app *anetos.App, name string) Credentials {
	env := envName(name)
	id, _ := app.Source().Lookup(env + "_CLIENT_ID")
	secret, _ := app.Source().Lookup(env + "_CLIENT_SECRET")
	return Credentials{ClientID: id, ClientSecret: anetos.Secret(secret)}
}

func envName(name string) string {
	return "SOCIAL_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func validName(n string) bool {
	if n == "" || len(n) > 50 {
		return false
	}
	for _, r := range n {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// Providers returns the names of the providers, in the order given, for
// sign-in buttons.
func (s *Social[U]) Providers() []string { return slices.Clone(s.order) }

// Title returns the provider's name for people ([Provider.Title], or its
// name), for sign-in buttons: "Sign in with Google".
func (s *Social[U]) Title(name string) string {
	if p, ok := s.providers[name]; ok && p.Title != "" {
		return p.Title
	}
	return name
}

// CallbackURL returns the redirect URI to register with the provider
// name.
func (s *Social[U]) CallbackURL(name string) string {
	return s.baseURL + strings.ReplaceAll(s.callback, "{provider}", url.PathEscape(name))
}

// flow is what the redirect keeps in the session for the callback.
type flow struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Nonce    string `json:"n,omitempty"`
	Remember bool   `json:"r,omitempty"`
	Expires  int64  `json:"x"`
}

func flowKey(provider string) string { return "_social." + provider }

// flowTTL is how long a user may take at the provider.
const flowTTL = 10 * time.Minute

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// httpsOnly returns a copy of c that refuses redirects to anything but
// https: tokens and profiles must only travel over TLS.
func httpsOnly(c *http.Client) *http.Client {
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	cp := *c
	next := c.CheckRedirect
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("social: refused a redirect to %s (not https)", req.URL.Redacted())
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 5 {
			return errors.New("social: too many redirects")
		}
		return nil
	}
	return &cp
}

// Redirect sends the browser to the provider named by the route's
// {provider} parameter to sign in; ?remember=1 asks for "remember me".
// Its route needs the session middleware.
func (s *Social[U]) Redirect(c *web.Ctx) error {
	r := c.Request()
	p, ok := s.providers[r.PathValue("provider")]
	sess := session.From(c)
	if !ok || sess == nil {
		return web.Error(http.StatusNotFound, "")
	}
	ctx := context.WithValue(c, oauth2.HTTPClient, s.client)
	conf, err := s.config(ctx, p)
	if err != nil {
		return s.fail(c, err, "The sign-in service isn't available right now. Try again later.")
	}
	f := flow{State: random(), Verifier: oauth2.GenerateVerifier(), Remember: r.URL.Query().Get("remember") == "1" && s.auth.CanRemember(),
		Expires: s.now().Add(flowTTL).Unix()}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(f.Verifier)}
	if p.Issuer != "" {
		f.Nonce = random()
		opts = append(opts, oauth2.SetAuthURLParam("nonce", f.Nonce))
	}
	sess.Put(flowKey(p.Name), f)
	return c.Redirect(http.StatusSeeOther, conf.AuthCodeURL(f.State, opts...))
}

// Callback finishes the sign-in when the provider sends the browser back:
// it checks the state, exchanges the code for tokens, reads the profile,
// asks the Resolver for the user and signs them in, then redirects to
// the page they wanted (auth.Intended) or AUTH_HOME_URL ([WithHomeURL]). Failures send
// the browser to AUTH_LOGIN_URL with a "social" field error. Its route
// needs the session and auth middleware.
func (s *Social[U]) Callback(c *web.Ctx) error {
	r := c.Request()
	p, ok := s.providers[r.PathValue("provider")]
	sess := session.From(c)
	if !ok || sess == nil {
		return web.Error(http.StatusNotFound, "")
	}
	var f flow
	found := sess.Pull(flowKey(p.Name), &f) // one use
	q := r.URL.Query()
	switch {
	case !found || f.State == "" || q.Get("state") != f.State || s.now().Unix() > f.Expires:
		return s.fail(c, errors.New("social: the callback's state doesn't match the session's (expired, replayed or forged)"),
			i18n.T(c, "auth.social_incomplete"))
	case q.Get("error") != "":
		msg := i18n.T(c, "auth.social_canceled")
		if q.Get("error") != "access_denied" {
			msg = i18n.T(c, "auth.social_error")
		}
		return s.fail(c, fmt.Errorf("social: %s: %s", q.Get("error"), q.Get("error_description")), msg)
	case q.Get("code") == "":
		return s.fail(c, errors.New("social: no code in the callback"), i18n.T(c, "auth.social_incomplete"))
	}
	ctx := context.WithValue(c, oauth2.HTTPClient, s.client)
	prof, err := s.profile(ctx, p, q.Get("code"), f)
	if err != nil {
		return s.fail(c, err, i18n.T(c, "auth.social_account"))
	}
	u, err := s.resolve(c, prof)
	if refused, ok := errors.AsType[*ErrNoAccount](err); ok {
		return s.fail(c, err, refused.Message)
	}
	if err != nil {
		return s.fail(c, err, i18n.T(c, "auth.social_failed"))
	}
	if v := reflect.ValueOf(any(u)); !v.IsValid() || (v.Kind() == reflect.Pointer && v.IsNil()) {
		return s.fail(c, errors.New("social: the Resolver returned no user and no error"), i18n.T(c, "auth.social_failed"))
	}
	// Two-factor sign-in, if the user has it on, still asks for a code.
	switch err := s.auth.SignIn(c, u, f.Remember); {
	case errors.Is(err, auth.ErrTwoFactorRequired):
		return c.Redirect(http.StatusSeeOther, web.LocalePath(c, s.auth.Config().ChallengeURL))
	case err != nil:
		return s.fail(c, err, i18n.T(c, "auth.social_failed"))
	}
	home := s.home
	if home == "" {
		home = s.auth.Config().HomeURL
	}
	return c.Redirect(http.StatusSeeOther, auth.Intended(c, home))
}

// fail logs err and sends the browser to the login page with msg.
func (s *Social[U]) fail(c *web.Ctx, err error, msg string) error {
	s.log.Warn("social: sign-in failed", "provider", c.Request().PathValue("provider"), "error", err)
	if sess := session.From(c); sess != nil {
		sess.FlashErrors(session.FieldError{Field: "social", Message: msg})
	}
	return c.Redirect(http.StatusSeeOther, web.LocalePath(c, s.auth.Config().LoginURL))
}

// config returns the provider's OAuth 2.0 configuration, discovering its
// endpoints first if needed.
func (s *Social[U]) config(ctx context.Context, p *provider) (*oauth2.Config, error) {
	endpoint := p.Endpoint
	if p.Discover {
		var err error
		if endpoint, err = s.discover(ctx, p); err != nil {
			return nil, err
		}
	}
	return &oauth2.Config{
		ClientID: p.clientID, ClientSecret: p.secret, Endpoint: endpoint,
		Scopes: p.Scopes, RedirectURL: s.CallbackURL(p.Name),
	}, nil
}

// profile exchanges the code and reads the account's profile.
func (s *Social[U]) profile(ctx context.Context, p *provider, code string, f flow) (Profile, error) {
	conf, err := s.config(ctx, p)
	if err != nil {
		return Profile{}, err
	}
	if u, err := url.Parse(conf.Endpoint.TokenURL); err != nil || u.Scheme != "https" {
		// The ID token's signature isn't checked: it is trusted because
		// it comes straight from the token endpoint over TLS (OpenID
		// Connect Core 3.1.3.7).
		return Profile{}, fmt.Errorf("social: %s's token endpoint %q isn't https", p.Name, conf.Endpoint.TokenURL)
	}
	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return Profile{}, fmt.Errorf("social: exchange the code with %s: %w", p.Name, err)
	}
	var prof Profile
	if p.Issuer != "" {
		raw, _ := tok.Extra("id_token").(string)
		if prof, err = s.idToken(p, raw, f.Nonce); err != nil {
			return Profile{}, err
		}
	} else if prof, err = p.Profile(ctx, s.client, tok); err != nil {
		return Profile{}, fmt.Errorf("social: read the profile from %s: %w", p.Name, err)
	}
	if prof.Subject == "" {
		return Profile{}, fmt.Errorf("social: %s gave no account identifier", p.Name)
	}
	prof.Provider = p.Name
	prof.Token = tok
	return prof, nil
}

// The credentials of providers whose settings are missing, when a test's
// stand-in provider replaces them.
const (
	stubClientID     = "test-client"
	stubClientSecret = "test-secret"
)

// replace returns providers going to the stand-in provider stub
// (anetostest.FakeSocial), and fills in missing credentials.
func replace(stub *socialstub.Stub, providers []Provider, creds map[string]Credentials) []Provider {
	issuer := strings.TrimSuffix(stub.Issuer, "/")
	out := make([]Provider, len(providers))
	for i, p := range providers {
		out[i] = Provider{
			Name:     p.Name,
			Title:    p.Title,
			Endpoint: oauth2.Endpoint{AuthURL: issuer + "/authorize", TokenURL: issuer + "/token"},
			Scopes:   p.Scopes,
			Issuer:   issuer,
		}
		if c := creds[p.Name]; c.ClientID == "" || c.ClientSecret == "" {
			creds[p.Name] = Credentials{ClientID: stubClientID, ClientSecret: stubClientSecret}
		}
	}
	return out
}
