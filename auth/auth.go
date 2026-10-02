// SPDX-License-Identifier: Apache-2.0

// Package auth signs users in and out, remembers them, finds the current
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
//	a, err := auth.ForApp(app, users)
//	pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
//	pages.Group("", a.Require).Get("/dashboard", …)
//
// Handlers sign users in with [Auth.Attempt] (checks the password, with
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
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
)

// Authenticatable is what package auth needs from the app's user type.
type Authenticatable interface {
	// AuthID returns the user's identifier, as stored in the session and
	// in API tokens: usually the primary key as text.
	AuthID() string
	// AuthPassword returns the user's password hash (from
	// password.Hash), or "" for users who can't sign in with a password.
	AuthPassword() string
}

// Users tells package auth how to find and update the app's users. ByID
// and ByLogin are required; they return [ErrNoUser] or db.ErrNotFound
// when there is no such user.
type Users[U Authenticatable] struct {
	// ByID returns the user with the identifier from U.AuthID.
	ByID func(ctx context.Context, id string) (U, error)
	// ByLogin returns the user who signs in with login: usually an email
	// address, compared without regard to case.
	ByLogin func(ctx context.Context, login string) (U, error)
	// RememberToken returns the user's remember-me token, stored with
	// the user (a remember_token column); "" if none yet. Optional: with
	// SetRememberToken, it enables "remember me".
	RememberToken func(u U) string
	// SetRememberToken stores a new remember-me token for the user. Auth
	// sets one at the first remembered login and replaces it at logout,
	// which signs the user out of every remembered browser.
	SetRememberToken func(ctx context.Context, u U, token string) error
	// SetPassword stores a new password hash for the user. Optional:
	// Attempt uses it to upgrade old hashes (password.NeedsRehash) after
	// a successful login.
	SetPassword func(ctx context.Context, u U, hash string) error
}

// Config holds the AUTH_* settings.
type Config struct {
	// LoginURL is where [Auth.Require] sends guests. AUTH_LOGIN_URL,
	// default /login.
	LoginURL string `env:"AUTH_LOGIN_URL" default:"/login"`
	// HomeURL is where [Auth.Guest] sends signed-in users, and
	// [Intended]'s usual fallback. AUTH_HOME_URL, default /.
	HomeURL string `env:"AUTH_HOME_URL" default:"/"`
	// RememberLifetime is how long "remember me" lasts.
	// AUTH_REMEMBER_LIFETIME, default 720h (30 days).
	RememberLifetime time.Duration `env:"AUTH_REMEMBER_LIFETIME" default:"720h"`
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
}

// Validate implements config.Validator.
func (c Config) Validate() error {
	var errs []error
	for name, u := range map[string]string{"AUTH_LOGIN_URL": c.LoginURL, "AUTH_HOME_URL": c.HomeURL} {
		if !localPath(u) {
			errs = append(errs, fmt.Errorf("%s %q must be a path on this site (starting with /)", name, u))
		}
	}
	if c.RememberLifetime < time.Minute || c.ResetTTL < time.Minute || c.VerifyTTL < time.Minute {
		errs = append(errs, errors.New("AUTH_REMEMBER_LIFETIME, AUTH_RESET_TTL and AUTH_VERIFY_TTL must be at least 1m"))
	}
	if c.Throttle < 1 || c.ThrottleIP < 1 {
		errs = append(errs, errors.New("AUTH_THROTTLE and AUTH_THROTTLE_IP must be at least 1"))
	}
	return errors.Join(errs...)
}

// LoadConfig reads the AUTH_* settings.
func LoadConfig(src config.Source) (Config, error) { return config.Get[Config](src) }

// Auth signs users of type U in and out. Create it with [ForApp] or
// [New]; it is safe for concurrent use.
type Auth[U Authenticatable] struct {
	cfg    Config
	users  Users[U]
	enc    *encryption.Encrypter
	log    *slog.Logger
	secure bool   // remember cookie Secure
	cookie string // remember cookie name
	now    func() time.Time
}

// Option configures [New].
type Option func(*options)

type options struct {
	log    *slog.Logger
	secure bool
}

// WithLogger sets the logger. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.log = l } }

// WithInsecureCookies lets the remember-me cookie travel over plain HTTP,
// for development and tests. ForApp uses it outside production-like
// environments, like the session cookie.
func WithInsecureCookies() Option { return func(o *options) { o.secure = false } }

// New returns an Auth for users, with keys from enc (APP_KEY).
func New[U Authenticatable](cfg Config, users Users[U], enc *encryption.Encrypter, opts ...Option) (*Auth[U], error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("auth: invalid config: %w", err)
	}
	if users.ByID == nil || users.ByLogin == nil {
		return nil, errors.New("auth: Users needs ByID and ByLogin")
	}
	if (users.RememberToken == nil) != (users.SetRememberToken == nil) {
		return nil, errors.New("auth: Users needs both RememberToken and SetRememberToken, or neither")
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
	return &Auth[U]{cfg: cfg, users: users, enc: enc, log: o.log, secure: o.secure, cookie: name, now: time.Now}, nil
}

// ForApp returns an Auth configured from the AUTH_* settings and APP_KEY,
// and provides it to the app. Login throttling needs the app's cache
// (cache.ForApp, called first). An app has one Auth: its session keys and
// cookie are fixed, so a second one would read the first one's users.
func ForApp[U Authenticatable](app *anetos.App, users Users[U]) (*Auth[U], error) {
	if _, ok := anetos.Lookup[appAuth](app); ok {
		return nil, errors.New("auth: ForApp was already called for this app; an app has one Auth")
	}
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	if _, err := anetos.Resolve[*cache.Cache](app); err != nil {
		return nil, errors.New("auth: login throttling needs the app's cache: call cache.ForApp before auth.ForApp")
	}
	enc, err := encryption.ForApp(app)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	opts := []Option{WithLogger(app.Logger().With("component", "auth"))}
	if m, err := anetos.Resolve[*session.Manager](app); err == nil {
		if s := m.Config().Secure; s != nil && !*s {
			opts = append(opts, WithInsecureCookies())
		}
	} else if env := app.Config().Env; env.IsDevelopment() || env.IsTesting() {
		opts = append(opts, WithInsecureCookies())
	}
	a, err := New(cfg, users, enc, opts...)
	if err != nil {
		return nil, err
	}
	a.now = app.Now // tests can freeze it
	anetos.Provide(app, a)
	anetos.Provide(app, appAuth{})
	app.AddContextValue(actorKey{}, actor(a))
	return a, nil
}

// appAuth marks an app whose Auth is set up.
type appAuth struct{}

// Config returns the configuration.
func (a *Auth[U]) Config() Config { return a.cfg }

// CanRemember reports whether "remember me" is available: Users has
// RememberToken and SetRememberToken.
func (a *Auth[U]) CanRemember() bool { return a.users.RememberToken != nil }

// Errors. Each has an HTTP status, so a handler can return it.
var (
	// ErrNoUser is what Users functions return when there is no such
	// user (db.ErrNotFound works too).
	ErrNoUser = errors.New("auth: no such user")
	// ErrInvalidCredentials is returned by Attempt for an unknown login
	// or a wrong password (which of the two isn't said). 401.
	ErrInvalidCredentials error = &statusError{http.StatusUnauthorized, "auth: invalid credentials"}
	// ErrUnauthenticated means the request has no signed-in user. 401.
	ErrUnauthenticated error = &statusError{http.StatusUnauthorized, "auth: not signed in"}
	// ErrForbidden means a policy refused the action. 403.
	ErrForbidden error = &statusError{http.StatusForbidden, "auth: not allowed"}
	// ErrInvalidToken means a password-reset or verification token is
	// malformed, expired or used. 400.
	ErrInvalidToken error = &statusError{http.StatusBadRequest, "auth: invalid or expired token"}
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

func notFound(err error) bool { return errors.Is(err, ErrNoUser) || errors.Is(err, db.ErrNotFound) }

// fingerprint identifies a password hash without revealing it: stored in
// the session, it signs the user out when the password changes.
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
