// SPDX-License-Identifier: Apache-2.0

// Package auth logs users in and out, remembers them, finds the current
// user of a request, issues API tokens and password-reset and
// email-verification tokens, and checks typed policies. Passwords are
// hashed by package auth/password.
//
// The app describes its users with [Users], then adds the middleware
// after the session middleware:
//
//	users := auth.Users[*models.User]{
//		ByID:    func(ctx context.Context, id string) (*models.User, error) { … },
//		ByLogin: func(ctx context.Context, email string) (*models.User, error) { … },
//	}
//	a, err := auth.New(app, users)
//	pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
//	pages.Group("", a.Require).Get("/dashboard", …)
//
// Handlers log users in with [Auth.Attempt] (checks the password, with
// login throttling) and out with [Auth.Logout], and read the current user
// with [User]:
//
//	u, ok := auth.User[*models.User](c)
//
// See docs/site/guides/authentication.md.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/session"
)

// Authenticatable is what package auth needs from the app's user type.
type Authenticatable interface {
	// AuthID returns the user's identifier, as stored in the session and
	// in API tokens: usually the primary key as text.
	AuthID() string
	// AuthPassword returns the user's password hash (from
	// password.Hash), or "" for users who can't log in with a password.
	AuthPassword() string
}

// Users tells package auth how to find and update the app's users. ByID
// and ByLogin are required; they return [ErrUserNotFound] or db.ErrNotFound
// when there is no such user.
type Users[U Authenticatable] struct {
	// ByID returns the user with the identifier from U.AuthID.
	ByID func(ctx context.Context, id string) (U, error)
	// ByLogin returns the user who logs in with login: usually an email
	// address, compared without regard to case.
	ByLogin func(ctx context.Context, login string) (U, error)
	// RememberToken returns the user's remember-me token, stored with
	// the user (a remember_token column); "" if none yet. Optional: with
	// SetRememberToken, it enables "remember me".
	RememberToken func(u U) string
	// SetRememberToken stores a new remember-me token for the user. Auth
	// sets one at the first remembered login and replaces it at logout,
	// which logs the user out of every remembered browser.
	SetRememberToken func(ctx context.Context, u U, token string) error
	// SetPassword stores a new password hash for the user. Optional:
	// Attempt uses it to upgrade old hashes (password.NeedsRehash) after
	// a successful login.
	SetPassword func(ctx context.Context, u U, hash string) error
	// Disabled reports whether the user's account is disabled (a
	// disabled_at column). Optional. A disabled user is logged out on
	// their next request and can't log in ([ErrDisabled]); their API
	// tokens and remember-me cookies stop working.
	Disabled func(u U) bool
	// SessionKey returns the user's session key (a session_key column),
	// "" if none yet. Optional: with SetSessionKey, it enables
	// [Auth.LogoutEverywhere]. Sessions and remember-me cookies are
	// bound to it, as to the password hash.
	SessionKey func(u U) string
	// SetSessionKey stores a new session key for the user.
	SetSessionKey func(ctx context.Context, u U, key string) error
	// TwoFactor returns the user's two-factor authentication state, as stored
	// (a two_factor column, text), "" if none. Optional: with
	// SetTwoFactor, it enables two-factor authentication ([Auth.StartTwoFactor]).
	// Package auth writes it: the secret encrypted with APP_KEY, the
	// recovery codes hashed.
	TwoFactor func(u U) string
	// SetTwoFactor stores the user's two-factor state ("" for none).
	SetTwoFactor func(ctx context.Context, u U, state string) error
}

// Config holds the AUTH_* settings.
type Config struct {
	// LoginURL is where [Auth.Require] sends guests. AUTH_LOGIN_URL,
	// default /login.
	LoginURL string `env:"AUTH_LOGIN_URL" default:"/login"`
	// HomeURL is the app's page for logged-in users: where [Auth.Guest]
	// sends them, and where logging in leads when there's no page they
	// wanted ([Intended]'s usual fallback). AUTH_HOME_URL, default /
	// ([WithDefaultHomeURL] sets another default).
	HomeURL string `env:"AUTH_HOME_URL" default:"/"`
	// RememberTTL is how long "remember me" lasts.
	// AUTH_REMEMBER_TTL, default 720h (30 days).
	RememberTTL time.Duration `env:"AUTH_REMEMBER_TTL" was:"AUTH_REMEMBER_LIFETIME" default:"720h"`
	// Throttle is the number of failed logins allowed per minute for one
	// login from one IP address. AUTH_THROTTLE, default 5.
	Throttle int `env:"AUTH_THROTTLE" default:"5"`
	// ThrottleIP is the number of failed logins allowed per minute from
	// one IP address (an IPv6 /64), whatever the login. AUTH_THROTTLE_IP,
	// default 50.
	ThrottleIP int `env:"AUTH_THROTTLE_IP" default:"50"`
	// ResetTTL is how long a password-reset token works. AUTH_RESET_TTL,
	// default 60m.
	ResetTTL time.Duration `env:"AUTH_RESET_TTL" default:"60m"`
	// VerifyTTL is how long an email-verification token works.
	// AUTH_VERIFY_TTL, default 24h.
	VerifyTTL time.Duration `env:"AUTH_VERIFY_TTL" default:"24h"`
	// RevertTTL is how long a link that undoes a change of email address
	// works ([Auth.EmailRevertToken]). AUTH_REVERT_TTL, default 168h (7
	// days).
	RevertTTL time.Duration `env:"AUTH_REVERT_TTL" default:"168h"`
	// ChallengeURL is where a login waiting for a two-factor code
	// asks for it ([ErrTwoFactorRequired]). AUTH_CHALLENGE_URL, default
	// /two-factor-challenge.
	ChallengeURL string `env:"AUTH_CHALLENGE_URL" default:"/two-factor-challenge"`
	// TwoFactorURL is where users turn two-factor authentication on and off.
	// AUTH_TWO_FACTOR_URL, default /two-factor.
	TwoFactorURL string `env:"AUTH_TWO_FACTOR_URL" default:"/two-factor"`
	// SettingsURL is where logged-in users change their account settings
	// (make:auth's page; the admin links there). AUTH_SETTINGS_URL,
	// default /settings.
	SettingsURL string `env:"AUTH_SETTINGS_URL" default:"/settings"`
	// ConfirmURL is where [Auth.RequireConfirmed] sends users to confirm
	// their password. AUTH_CONFIRM_URL, default /confirm-password.
	ConfirmURL string `env:"AUTH_CONFIRM_URL" default:"/confirm-password"`
	// ConfirmTTL is how long a confirmed password holds.
	// AUTH_CONFIRM_TTL, default 15m.
	ConfirmTTL time.Duration `env:"AUTH_CONFIRM_TTL" default:"15m"`
	// ClientURL is the address of the app people use when the app is an
	// API (a single-page app, a mobile app's web pages): where emailed
	// links lead ([Auth.ClientLink]), such as https://app.example.com.
	// AUTH_CLIENT_URL, default none.
	ClientURL string `env:"AUTH_CLIENT_URL"`
}

// Validate implements config.Validator.
func (c Config) Validate() error {
	var errs []error
	for _, p := range []struct{ name, url string }{{"AUTH_LOGIN_URL", c.LoginURL}, {"AUTH_HOME_URL", c.HomeURL},
		{"AUTH_CHALLENGE_URL", c.ChallengeURL}, {"AUTH_TWO_FACTOR_URL", c.TwoFactorURL}, {"AUTH_CONFIRM_URL", c.ConfirmURL},
		{"AUTH_SETTINGS_URL", c.SettingsURL}} { // in order: the messages read the same each time
		if !localPath(p.url) {
			errs = append(errs, fmt.Errorf("%s %q must be a path on this site (starting with /)", p.name, p.url))
		}
	}
	if c.RememberTTL < time.Minute || c.ResetTTL < time.Minute || c.VerifyTTL < time.Minute || c.ConfirmTTL < time.Minute || c.RevertTTL < time.Minute {
		errs = append(errs, errors.New("AUTH_REMEMBER_TTL, AUTH_RESET_TTL, AUTH_VERIFY_TTL, AUTH_CONFIRM_TTL and AUTH_REVERT_TTL must be at least 1m"))
	}
	if c.ClientURL != "" {
		if u, err := url.Parse(c.ClientURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			errs = append(errs, fmt.Errorf("AUTH_CLIENT_URL %q must be an http or https address, such as https://app.example.com, without a query", c.ClientURL))
		}
	}
	if c.Throttle < 1 || c.ThrottleIP < 1 {
		errs = append(errs, errors.New("AUTH_THROTTLE and AUTH_THROTTLE_IP must be at least 1"))
	}
	return errors.Join(errs...)
}

// LoadConfig reads the AUTH_* settings.
func LoadConfig(src config.Source) (Config, error) { return config.Get[Config](src) }

// Auth logs users of type U in and out. Create it with [New] or
// [NewWithConfig]; it is safe for concurrent use.
type Auth[U Authenticatable] struct {
	cfg    Config
	users  Users[U]
	enc    *encryption.Encrypter
	log    *slog.Logger
	secure bool   // remember cookie Secure
	cookie string // remember cookie name
	issuer string // two-factor setups' issuer
	now    func() time.Time
}

// Option configures [NewWithConfig].
type Option func(*options)

type options struct {
	log    *slog.Logger
	secure bool
	issuer string
	home   string
}

// WithLogger sets the logger. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.log = l } }

// WithIssuer names the app in users' authenticator apps (two-factor
// authentication). New uses APP_NAME.
func WithIssuer(name string) Option { return func(o *options) { o.issuer = name } }

// WithDefaultHomeURL sets HomeURL's default, for [New]: the page users go
// to after logging in when AUTH_HOME_URL isn't set, instead of /.
// make:auth's setupAuth gives /dashboard. AUTH_HOME_URL still wins, so
// each deployment can choose. [NewWithConfig] takes its Config as it is and
// ignores it.
func WithDefaultHomeURL(path string) Option { return func(o *options) { o.home = path } }

// DefaultHomeURL is [WithDefaultHomeURL].
//
// Deprecated: Use WithDefaultHomeURL; DefaultHomeURL is removed in v0.6.
//
//go:fix inline
func DefaultHomeURL(path string) Option { return WithDefaultHomeURL(path) }

// WithInsecureCookies lets the remember-me cookie travel over plain HTTP,
// for development and tests. New uses it outside production-like
// environments, like the session cookie.
func WithInsecureCookies() Option { return func(o *options) { o.secure = false } }

// NewWithConfig returns an Auth for users, with keys from enc (APP_KEY).
func NewWithConfig[U Authenticatable](cfg Config, users Users[U], enc *encryption.Encrypter, opts ...Option) (*Auth[U], error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("auth: invalid config: %w", err)
	}
	if users.ByID == nil || users.ByLogin == nil {
		return nil, errors.New("auth: Users needs ByID and ByLogin")
	}
	if (users.RememberToken == nil) != (users.SetRememberToken == nil) {
		return nil, errors.New("auth: Users needs both RememberToken and SetRememberToken, or neither")
	}
	if (users.SessionKey == nil) != (users.SetSessionKey == nil) {
		return nil, errors.New("auth: Users needs both SessionKey and SetSessionKey, or neither")
	}
	if (users.TwoFactor == nil) != (users.SetTwoFactor == nil) {
		return nil, errors.New("auth: Users needs both TwoFactor and SetTwoFactor, or neither")
	}
	if enc == nil {
		return nil, errors.New("auth: nil Encrypter")
	}
	o := options{log: slog.Default(), secure: true}
	for _, opt := range opts {
		opt(&o)
	}
	name := "anetos_remember"
	if o.secure {
		name = "__Host-" + name
	}
	return &Auth[U]{cfg: cfg, users: users, enc: enc, log: o.log, secure: o.secure, cookie: name, issuer: o.issuer, now: time.Now}, nil
}

// New returns an Auth configured from the AUTH_* settings and APP_KEY,
// and provides it to the app. Login throttling needs the app's cache
// (cache.New, called first). An app has one Auth: its session keys and
// cookie are fixed, so a second one would read the first one's users.
// The options come after New's own (the app's logger, APP_NAME as the
// issuer, insecure cookies outside production); [WithDefaultHomeURL] sets
// where users go after logging in, unless AUTH_HOME_URL is set.
func New[U Authenticatable](app *anetos.App, users Users[U], opts ...Option) (*Auth[U], error) {
	if _, ok := anetos.Lookup[appAuth](app); ok {
		return nil, errors.New("auth: New called twice for one app")
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	src := app.Source()
	if v, _ := src.Lookup("AUTH_HOME_URL"); v == "" && o.home != "" { // AUTH_HOME_URL wins
		src = config.Layers(config.Map{"AUTH_HOME_URL": o.home}, src)
	}
	cfg, err := LoadConfig(src)
	if err != nil {
		return nil, err
	}
	if _, err := anetos.Resolve[*cache.Cache](app); err != nil {
		return nil, errors.New("auth: login throttling needs the app's cache: call cache.New before auth.New")
	}
	enc, err := encryption.New(app)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	base := []Option{WithLogger(app.Logger().With("component", "auth")), WithIssuer(app.Config().Name)}
	if m, err := anetos.Resolve[*session.Manager](app); err == nil {
		if s := m.Config().Secure; s != nil && !*s {
			base = append(base, WithInsecureCookies())
		}
	} else if env := app.Config().Env; env.IsDevelopment() || env.IsTesting() {
		base = append(base, WithInsecureCookies())
	}
	a, err := NewWithConfig(cfg, users, enc, append(base, opts...)...)
	if err != nil {
		return nil, err
	}
	a.now = app.Now // tests can freeze it
	anetos.Provide(app, a)
	anetos.Provide(app, appAuth{})
	app.AddContextValue(actorKey{}, actor(a))
	// The logged-in user's language and time zone (i18n.LocalePreference,
	// i18n.TimeZonePreference) are the request's.
	i18n.SetCurrentUser(app, func(ctx context.Context) (any, bool) {
		st := stateFrom(ctx)
		if st == nil {
			return nil, false
		}
		u, err := st.get(ctx)
		return u, err == nil && u != nil
	})
	return a, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp[U Authenticatable](app *anetos.App, users Users[U], opts ...Option) (*Auth[U], error) {
	return New[U](app, users, opts...)
}

// appAuth marks an app whose Auth is set up.
type appAuth struct{}

// ClientLink returns the address of path (with query, if any) in the
// client app, AUTH_CLIENT_URL: the link an API's emails give, whose page
// sends the token in it back to the API.
//
//	link, err := a.ClientLink("/reset-password", url.Values{"token": {a.PasswordResetToken(u)}})
//
// It fails, naming the setting, when AUTH_CLIENT_URL isn't set.
func (a *Auth[U]) ClientLink(path string, query url.Values) (string, error) {
	if a.cfg.ClientURL == "" {
		return "", errors.New("auth: AUTH_CLIENT_URL isn't set: the address of the client app, where emailed links lead")
	}
	link := strings.TrimSuffix(a.cfg.ClientURL, "/") + "/" + strings.TrimPrefix(path, "/")
	if len(query) > 0 {
		link += "?" + query.Encode()
	}
	return link, nil
}

// Config returns the configuration.
func (a *Auth[U]) Config() Config { return a.cfg }

// RememberCookie returns the remember-me cookie's name.
func (a *Auth[U]) RememberCookie() string { return a.cookie }

// SupportsRemember reports whether "remember me" is available: Users has
// RememberToken and SetRememberToken.
func (a *Auth[U]) SupportsRemember() bool { return a.users.RememberToken != nil }

// CanRemember is [Auth.SupportsRemember].
//
// Deprecated: Use SupportsRemember; CanRemember is removed in v0.6.
//
//go:fix inline
func (a *Auth[U]) CanRemember() bool { return a.SupportsRemember() }

// Errors. Each has an HTTP status, so a handler can return it.
var (
	// ErrUserNotFound is what Users functions return when there is no
	// such user (db.ErrNotFound works too).
	ErrUserNotFound = errors.New("auth: user not found")
	// ErrNoUser is ErrUserNotFound.
	//
	// Deprecated: Use ErrUserNotFound; ErrNoUser is removed in v0.6.
	ErrNoUser error = ErrUserNotFound
	// ErrInvalidCredentials is returned by Attempt for an unknown login
	// or a wrong password (which of the two isn't said). 401.
	ErrInvalidCredentials error = &statusError{http.StatusUnauthorized, "auth: invalid credentials"}
	// ErrUnauthenticated means the request has no logged-in user. 401.
	ErrUnauthenticated error = &statusError{http.StatusUnauthorized, "auth: not logged in"}
	// ErrForbidden means a policy refused the action. 403.
	ErrForbidden error = &statusError{http.StatusForbidden, "auth: not allowed"}
	// ErrInvalidToken means a password-reset or verification token is
	// malformed, expired or used. 400.
	ErrInvalidToken error = &statusError{http.StatusBadRequest, "auth: invalid or expired token"}
	// ErrDisabled is returned by Attempt (once the password checks out)
	// and Login for a user whose account is disabled (Users.Disabled).
	// 403.
	ErrDisabled error = &statusError{http.StatusForbidden, "auth: this account is disabled"}
	// ErrNotImpersonating is returned by StopImpersonating when the
	// session isn't impersonating anyone. 409.
	ErrNotImpersonating error = &statusError{http.StatusConflict, "auth: not impersonating another user"}
)

type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string   { return e.msg }
func (e *statusError) HTTPStatus() int { return e.status }

// ThrottledError is returned by Attempt when a login has failed too often
// from one address. 429.
type ThrottledError struct {
	// RetryAfter is how long until attempts are allowed again.
	RetryAfter time.Duration
}

// Error implements error.
func (e *ThrottledError) Error() string {
	return fmt.Sprintf("auth: too many login attempts; try again in %d seconds", int(e.RetryAfter.Seconds())+1)
}

// HTTPStatus implements web.StatusCoder.
func (e *ThrottledError) HTTPStatus() int { return http.StatusTooManyRequests }

func notFound(err error) bool {
	return errors.Is(err, ErrUserNotFound) || errors.Is(err, db.ErrNotFound)
}

// fingerprint identifies a password hash without revealing it: stored in
// the session, it logs the user out when the password changes.
// sessionPrint is what sessions and remember-me cookies of u are bound
// to: the password hash and, if any, the session key. Without a key, it
// is the hash's fingerprint, as before keys.
func (a *Auth[U]) sessionPrint(u U, hash string) string {
	if a.users.SessionKey != nil {
		if key := a.users.SessionKey(u); key != "" {
			return fingerprint(hash + "\x00" + key)
		}
	}
	return fingerprint(hash)
}

// disabled reports whether u's account is disabled.
func (a *Auth[U]) disabled(u U) bool { return a.users.Disabled != nil && a.users.Disabled(u) }

func fingerprint(hash string) string {
	sum := sha256.Sum256([]byte("anetos/auth\x00" + hash))
	return hex.EncodeToString(sum[:12])
}

func randomToken() string {
	b := make([]byte, 30)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// localPath reports whether u is a path on this site.
func localPath(u string) bool {
	return strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.HasPrefix(u, "/\\") &&
		!strings.ContainsFunc(u, func(r rune) bool { return r < 0x20 || r == 0x7f }) // browsers drop control characters: "/\t/host"
}
