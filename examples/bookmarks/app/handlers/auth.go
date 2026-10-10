// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/qr"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"

	"bookmarks/app/mailers"
	"bookmarks/app/models"
)

// Accounts serves the API's accounts (anetos make:auth): registration,
// login with an API token (and a two-factor code for users who have it
// on), logout, the logged-in user, email verification, password reset
// and change, API tokens and two-factor authentication. Hashing, tokens and
// throttling are package auth's; this is your code to change.
type Accounts struct {
	Auth *auth.Auth[*models.User]
}

// The lifetimes of the tokens the API gives.
const (
	loginTTL = 30 * 24 * time.Hour // a registration's or login's token
	tokenTTL = 90 * 24 * time.Hour // a token made with POST /tokens
)

// UserResponse is how the API shows a user: the fields chosen here, so
// a new column of the users table never shows by accident.
type UserResponse struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Email           string     `json:"email"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

func userResponse(u *models.User) UserResponse {
	return UserResponse{ID: u.ID, Name: u.Name, Email: u.Email, EmailVerifiedAt: u.EmailVerifiedAt, CreatedAt: u.CreatedAt}
}

// LoginResponse answers registration and login: a token for the
// Authorization header, and its user. For a user with two-factor
// authentication on, login answers instead with TwoFactor and a Challenge, to
// send back with a code to POST /api/v1/login/two-factor.
type LoginResponse struct {
	Token     string        `json:"token,omitempty"`
	User      *UserResponse `json:"user,omitempty"`
	TwoFactor bool          `json:"two_factor,omitempty"`
	Challenge string        `json:"challenge,omitempty"`
}

// RegisterInput is a new account.
type RegisterInput struct {
	Name                 string `json:"name" validate:"required|max:100"`
	Email                string `json:"email" validate:"required|email|max:255"`
	Password             string `json:"password" validate:"required|min:8|max:1024|confirmed"`
	PasswordConfirmation string `json:"password_confirmation"`
	DeviceName           string `json:"device_name" validate:"max:100"` // the token's name: "Ada's phone"
}

// LoginInput is a login.
type LoginInput struct {
	Email      string `json:"email" validate:"required|email"`
	Password   string `json:"password" validate:"required"`
	DeviceName string `json:"device_name" validate:"max:100"`
}

// ChallengeInput is the second step of a login with two-factor authentication:
// the challenge the login answered, and a code of the user's
// authenticator app or one of their recovery codes.
type ChallengeInput struct {
	Challenge  string `json:"challenge" validate:"required|max:4096"`
	Code       string `json:"code" validate:"required|max:30"`
	DeviceName string `json:"device_name" validate:"max:100"`
}

// EmailInput is an email address, to send a password reset link to.
type EmailInput struct {
	Email string `json:"email" validate:"required|email|max:255"`
}

// LinkInput is the token of an emailed link, which the client app
// sends back.
type LinkInput struct {
	Token string `json:"token" validate:"required|max:4096"`
}

// ResetInput is a new password, with the token of a reset link.
type ResetInput struct {
	Token                string `json:"token" validate:"required|max:4096"`
	Password             string `json:"password" validate:"required|min:8|max:1024|confirmed"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// ChangePasswordInput is a new password, with the current one.
type ChangePasswordInput struct {
	CurrentPassword      string `json:"current_password" validate:"required"`
	Password             string `json:"password" validate:"required|min:8|max:1024|confirmed"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// PasswordInput is the current password, asked for again before
// something sensitive.
type PasswordInput struct {
	Password string `json:"password" validate:"required"`
}

// CodeInput is a code of the authenticator app.
type CodeInput struct {
	Code string `json:"code" validate:"required|max:30"`
}

// NewTokenInput is a new API token: its name, its abilities ("*", every
// one, if none; check them with auth.TokenCan), and the current password.
type NewTokenInput struct {
	Name      string   `json:"name" validate:"required|max:100"`
	Abilities []string `json:"abilities" validate:"max:20|distinct|token_abilities"`
	Password  string   `json:"password" validate:"required"`
}

func init() {
	// token_abilities: names of 1 to 100 printable ASCII characters,
	// without spaces ("orders.read"). A catalog's validation.token_abilities
	// translates the message.
	validate.Register("token_abilities", "The {label} field must hold names of 1 to 100 letters, digits or signs, without spaces.",
		func(_ context.Context, f validate.Field) (bool, error) {
			names, _ := f.Value.([]string)
			for _, n := range names {
				if n == "" || len(n) > 100 || strings.IndexFunc(n, func(r rune) bool { return r <= ' ' || r > '~' }) >= 0 {
					return false, nil
				}
			}
			return true, nil
		})
}

// TokenID reads {id} from the path.
type TokenID struct {
	ID int64 `path:"id"`
}

// TokenResponse is how the API shows an API token: never its secret,
// which only NewTokenResponse has, once.
type TokenResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Abilities  []string   `json:"abilities"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	Current    bool       `json:"current"` // the request's token
}

// NewTokenResponse is a token just made: Token is for the Authorization
// header, and isn't shown again.
type NewTokenResponse struct {
	Token string `json:"token"`
	TokenResponse
}

// TwoFactorResponse is the user's two-factor authentication.
type TwoFactorResponse struct {
	Enabled       bool `json:"enabled"`        // login asks for a code
	Started       bool `json:"started"`        // a setup waits for POST /two-factor/confirm
	RecoveryCodes int  `json:"recovery_codes"` // unused recovery codes left
}

// TwoFactorSetupResponse starts turning on two-factor authentication: the key
// for the authenticator app, as a QR code (an SVG image) or typed.
type TwoFactorSetupResponse struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`     // otpauth://, what the QR code holds
	QRCode string `json:"qr_code"` // an SVG image
}

// RecoveryCodesResponse is the recovery codes, shown once: each logs in
// once without the authenticator app.
type RecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// Register creates the account, emails the verification link, and
// answers with a token (201: the route's Status).
func (h Accounts) Register(c *web.Ctx, in RegisterInput) (LoginResponse, error) {
	name := cleanName(in.Name)
	if name == "" {
		return LoginResponse{}, validate.Fail("name", i18n.T(c, "auth.errors.name_required"))
	}
	email := strings.ToLower(in.Email) // stored and looked up in lower case
	taken, err := emailTaken(c, email)
	if err != nil {
		return LoginResponse{}, err
	}
	if taken {
		return LoginResponse{}, validate.Fail("email", i18n.T(c, "auth.errors.email_taken"))
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		return LoginResponse{}, err
	}
	u := &models.User{Name: name, Email: email, Password: hash}
	// The user and the email, or neither: the email is queued once the
	// transaction commits.
	err = db.Tx(c, func(ctx context.Context) error {
		if err := db.Create(ctx, u); err != nil {
			return err
		}
		return SendVerification(ctx, h.Auth, u)
	})
	if err != nil {
		if taken, _ := emailTaken(c, email); taken { // registered meanwhile
			return LoginResponse{}, validate.Fail("email", i18n.T(c, "auth.errors.email_taken"))
		}
		return LoginResponse{}, err
	}
	res, err := h.loginResponse(c, u, in.DeviceName)
	if err != nil {
		return LoginResponse{}, err
	}
	return res, nil
}

// emailTaken reports whether an account has the address.
func emailTaken(ctx context.Context, email string) (bool, error) {
	return db.Query[models.User](ctx).Where(models.UserCols.Email.Eq(email)).Exists()
}

// cleanName returns name on one line: control characters dropped, runs
// of spaces made one.
func cleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	return strings.Join(strings.Fields(name), " ")
}

// loginResponse gives u a token named after the client's device, with every
// ability.
func (h Accounts) loginResponse(c *web.Ctx, u *models.User, device string) (LoginResponse, error) {
	device = cleanName(device)
	if device == "" {
		device = "API"
	}
	plain, _, err := h.Auth.CreateToken(c, u, device, []string{"*"}, loginTTL)
	if err != nil {
		return LoginResponse{}, err
	}
	c.SetHeader("Cache-Control", "no-store") // a token: kept by no cache
	user := userResponse(u)
	return LoginResponse{Token: plain, User: &user}, nil
}

// SendVerification emails u a link to the client app (AUTH_CLIENT_URL)
// that verifies their address, in the language of ctx.
func SendVerification(ctx context.Context, a *auth.Auth[*models.User], u *models.User) error {
	link, err := a.ClientLink("/verify-email", url.Values{"token": {a.VerificationToken(u, u.Email)}})
	if err != nil {
		return err
	}
	return mailer.Queue(ctx, mailers.VerifyEmail{Name: u.Name, Email: u.Email, URL: link}, queue.AfterCommit())
}

// SendPasswordReset emails u a link to the client app (AUTH_CLIENT_URL)
// to choose a new password, in the language of ctx.
func SendPasswordReset(ctx context.Context, a *auth.Auth[*models.User], u *models.User) error {
	link, err := a.ClientLink("/reset-password", url.Values{"token": {a.PasswordResetToken(u)}})
	if err != nil {
		return err
	}
	return mailer.Queue(ctx, mailers.ResetPassword{Name: u.Name, Email: u.Email, URL: link}, queue.AfterCommit())
}

// Login checks the email and password (throttled: AUTH_THROTTLE) and
// answers with a token; or, for a user with two-factor authentication on, with
// a challenge for POST /login/two-factor.
func (h Accounts) Login(c *web.Ctx, in LoginInput) (LoginResponse, error) {
	u, err := h.Auth.AttemptCredentials(c, in.Email, in.Password)
	var (
		challenge *auth.TwoFactorChallenge
		throttled *auth.ThrottledError
	)
	switch {
	case errors.As(err, &challenge):
		c.SetHeader("Cache-Control", "no-store")
		return LoginResponse{TwoFactor: true, Challenge: challenge.Token}, nil
	case errors.Is(err, auth.ErrInvalidCredentials):
		return LoginResponse{}, validate.Fail("email", i18n.T(c, "auth.errors.credentials"))
	case errors.Is(err, auth.ErrDisabled):
		return LoginResponse{}, validate.Fail("email", i18n.T(c, "auth.errors.disabled"))
	case errors.As(err, &throttled):
		return LoginResponse{}, tooMany(c, throttled)
	case err != nil:
		return LoginResponse{}, err
	}
	return h.loginResponse(c, u, in.DeviceName)
}

// LoginTwoFactor finishes a login with the challenge and a code of the
// authenticator app, or a recovery code, and answers with a token.
func (h Accounts) LoginTwoFactor(c *web.Ctx, in ChallengeInput) (LoginResponse, error) {
	u, err := h.Auth.AttemptTwoFactorChallenge(c, in.Challenge, in.Code)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCode):
		return LoginResponse{}, validate.Fail("code", i18n.T(c, "auth.errors.code"))
	case errors.Is(err, auth.ErrNoPendingLogin):
		// Expired (after 10 minutes), or the password changed: log in again.
		return LoginResponse{}, validate.Fail("challenge", i18n.T(c, "auth.errors.login_again"))
	case errors.Is(err, auth.ErrDisabled):
		return LoginResponse{}, validate.Fail("challenge", i18n.T(c, "auth.errors.disabled"))
	case errors.As(err, &throttled):
		return LoginResponse{}, tooMany(c, throttled)
	case err != nil:
		return LoginResponse{}, err
	}
	return h.loginResponse(c, u, in.DeviceName)
}

// tooMany is a 429 for throttled tries, saying when to try again.
func tooMany(c *web.Ctx, throttled *auth.ThrottledError) error {
	return tooManyFor(c, throttled.RetryAfter, "auth.errors.throttled")
}

// tooManyFor is a 429 with the catalog's message key, saying when to
// try again (Retry-After).
func tooManyFor(c *web.Ctx, retryAfter time.Duration, key string) error {
	c.SetHeader("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
	return web.Error(http.StatusTooManyRequests, i18n.T(c, key))
}

// Me answers the logged-in user.
func (Accounts) Me(c *web.Ctx, _ struct{}) (UserResponse, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return UserResponse{}, err
	}
	return userResponse(u), nil
}

// Logout revokes the request's token: 204.
func (h Accounts) Logout(c *web.Ctx, _ struct{}) (web.Empty, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return web.Empty{}, err
	}
	if tok, ok := auth.CurrentToken(c); ok {
		if err := h.Auth.RevokeToken(c, u, tok.ID); err != nil {
			return web.Empty{}, err
		}
	}
	return web.Empty{}, nil
}

// VerifyEmail marks the address of an emailed link as verified: 204. A
// bad or expired link (auth.ErrInvalidToken), or one for an address the
// user has since changed, is a 400: the client app says the link didn't
// work, and offers a new one.
func (h Accounts) VerifyEmail(c *web.Ctx, in LinkInput) (web.Empty, error) {
	u, email, err := h.Auth.CheckVerificationToken(c, in.Token)
	if err != nil {
		return web.Empty{}, err
	}
	if email != u.Email {
		return web.Empty{}, web.Error(http.StatusBadRequest, i18n.T(c, "auth.errors.link_other_email"))
	}
	if u.EmailVerifiedAt == nil {
		now := anetos.Now(c).UTC()
		if _, err := db.Query[models.User](c).Where(models.UserCols.ID.Eq(u.ID)).Update(models.UserCols.EmailVerifiedAt.Set(&now)); err != nil {
			return web.Empty{}, err
		}
	}
	return web.Empty{}, nil
}

// ResendVerification emails the verification link again, unless the
// address is verified: 204.
func (h Accounts) ResendVerification(c *web.Ctx, _ struct{}) (web.Empty, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return web.Empty{}, err
	}
	if u.EmailVerifiedAt != nil {
		return web.Empty{}, nil
	}
	// At most 6 an hour for one account, whatever the IP address.
	res, err := ratelimit.Allow(c, "verification:"+u.AuthID(), ratelimit.PerHour(6))
	if err != nil {
		return web.Empty{}, err
	}
	if !res.Allowed {
		return web.Empty{}, tooManyFor(c, res.RetryAfter(), "auth.errors.too_many_emails")
	}
	return web.Empty{}, SendVerification(c, h.Auth, u)
}

// ForgotPassword emails a password reset link to the address, if it has
// an account (and was sent fewer than 3 in the hour), and answers 204
// either way, so it doesn't reveal who has one.
func (h Accounts) ForgotPassword(c *web.Ctx, in EmailInput) (web.Empty, error) {
	res, err := ratelimit.Allow(c, "password-reset:"+strings.ToLower(in.Email), ratelimit.PerHour(3))
	if err != nil {
		return web.Empty{}, err
	}
	u, err := models.Users.ByLogin(c, in.Email)
	switch {
	case err == nil && !res.Allowed:
		// Enough links for now: answer as usual.
	case err == nil:
		if err := SendPasswordReset(c, h.Auth, u); err != nil {
			return web.Empty{}, err
		}
	case !errors.Is(err, db.ErrNotFound):
		return web.Empty{}, err
	}
	return web.Empty{}, nil
}

// ResetPassword sets the new password of a reset link: 204. The link
// works once: it is tied to the old password. The new one revokes every
// API token of the user. For an address never verified, the link
// verifies it, and turns off the two-factor authentication whoever registered
// it may have set up.
func (h Accounts) ResetPassword(c *web.Ctx, in ResetInput) (web.Empty, error) {
	u, err := h.Auth.CheckPasswordResetToken(c, in.Token)
	if errors.Is(err, auth.ErrInvalidToken) {
		return web.Empty{}, validate.Fail("token", i18n.T(c, "auth.errors.reset_invalid"))
	}
	if err != nil {
		return web.Empty{}, err
	}
	hash, err := password.Hash(in.Password)
	if errors.Is(err, password.ErrTooLong) {
		return web.Empty{}, validate.Fail("password", i18n.T(c, "auth.errors.password_too_long"))
	}
	if err != nil {
		return web.Empty{}, err
	}
	errUsed := errors.New("the link was used meanwhile")
	err = db.Tx(c, func(ctx context.Context) error {
		// Only if the password is still the one the link was made for:
		// two uses of the link at once change it once.
		n, err := db.Query[models.User](ctx).Where(models.UserCols.ID.Eq(u.ID), models.UserCols.Password.Eq(u.Password)).
			Update(models.UserCols.Password.Set(hash))
		if err != nil {
			return err
		}
		if n == 0 {
			return errUsed
		}
		if u.EmailVerifiedAt == nil {
			// The link proves the address is theirs. Someone else may have
			// registered it first: undo what they could have set up.
			now := anetos.Now(ctx).UTC()
			if _, err := db.Query[models.User](ctx).Where(models.UserCols.ID.Eq(u.ID)).
				Update(models.UserCols.EmailVerifiedAt.Set(&now)); err != nil {
				return err
			}
			if err := h.Auth.DisableTwoFactor(ctx, u); err != nil {
				return err
			}
		}
		// The API tokens stop working (older reset links and login
		// challenges did with the password).
		return h.Auth.RevokeAllTokens(ctx, u)
	})
	if errors.Is(err, errUsed) {
		return web.Empty{}, validate.Fail("token", i18n.T(c, "auth.errors.reset_invalid"))
	}
	return web.Empty{}, err
}

// ChangePassword sets a new password, given the current one: 204. The
// user's other API tokens are revoked (the request's keeps working),
// and so are older reset links and login challenges.
func (h Accounts) ChangePassword(c *web.Ctx, in ChangePasswordInput) (web.Empty, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return web.Empty{}, err
	}
	err = h.Auth.ChangePassword(c, u, in.CurrentPassword, in.Password)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return web.Empty{}, validate.Fail("current_password", i18n.T(c, "auth.errors.password"))
	case errors.Is(err, password.ErrTooLong):
		return web.Empty{}, validate.Fail("password", i18n.T(c, "auth.errors.password_too_long"))
	case errors.As(err, &throttled):
		return web.Empty{}, tooMany(c, throttled)
	case err != nil:
		return web.Empty{}, err
	}
	return web.Empty{}, h.revokeOthers(c, u)
}

// revokeOthers revokes u's API tokens but the request's.
func (h Accounts) revokeOthers(c *web.Ctx, u *models.User) error {
	var keep int64
	if tok, ok := auth.CurrentToken(c); ok {
		keep = tok.ID
	}
	return h.Auth.RevokeOtherTokens(c, u, keep)
}

// checkPassword asks for the password again before something sensitive:
// a 422 on the password field if it's wrong, a 429 after too many tries
// (AUTH_THROTTLE a minute, 50 wrong a day).
func (h Accounts) checkPassword(c *web.Ctx, u *models.User, pw string) error {
	err := h.Auth.CheckPassword(c, u, pw)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return validate.Fail("password", i18n.T(c, "auth.errors.password"))
	case errors.As(err, &throttled):
		return tooMany(c, throttled)
	}
	return err
}

// Tokens lists the user's API tokens, newest first.
func (h Accounts) Tokens(c *web.Ctx, _ struct{}) ([]TokenResponse, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return nil, err
	}
	tokens, err := h.Auth.Tokens(c, u)
	if err != nil {
		return nil, err
	}
	current, _ := auth.CurrentToken(c)
	out := make([]TokenResponse, len(tokens))
	for i, t := range tokens {
		out[i] = tokenResponse(&t, current)
	}
	return out, nil
}

func tokenResponse(t, current *auth.Token) TokenResponse {
	return TokenResponse{ID: t.ID, Name: t.Name, Abilities: t.Abilities, LastUsedAt: t.LastUsedAt,
		ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt, Current: current != nil && current.ID == t.ID}
}

// CreateToken makes an API token, given the current password: the
// token, shown this once (201: the route's Status). It expires in 90
// days.
func (h Accounts) CreateToken(c *web.Ctx, in NewTokenInput) (NewTokenResponse, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return NewTokenResponse{}, err
	}
	if err := h.checkPassword(c, u, in.Password); err != nil {
		return NewTokenResponse{}, err
	}
	abilities := in.Abilities
	if len(abilities) == 0 {
		abilities = []string{"*"}
	}
	name := cleanName(in.Name)
	if name == "" {
		return NewTokenResponse{}, validate.Fail("name", i18n.T(c, "auth.errors.name_required"))
	}
	plain, t, err := h.Auth.CreateToken(c, u, name, abilities, tokenTTL)
	if err != nil {
		return NewTokenResponse{}, err
	}
	return NewTokenResponse{Token: plain, TokenResponse: tokenResponse(t, nil)}, nil
}

// RevokeToken deletes one of the user's API tokens (the request's too):
// 204.
func (h Accounts) RevokeToken(c *web.Ctx, in TokenID) (web.Empty, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return web.Empty{}, err
	}
	return web.Empty{}, h.Auth.RevokeToken(c, u, in.ID)
}

// TwoFactor answers the user's two-factor authentication.
func (h Accounts) TwoFactor(c *web.Ctx, _ struct{}) (TwoFactorResponse, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return TwoFactorResponse{}, err
	}
	st, err := h.Auth.TwoFactor(u)
	if err != nil {
		return TwoFactorResponse{}, err
	}
	return TwoFactorResponse{Enabled: st.On, Started: st.Started, RecoveryCodes: st.RecoveryCodes}, nil
}

// StartTwoFactor starts turning on two-factor authentication, given the
// current password: the key for the authenticator app. It is on once
// POST /two-factor/confirm has a code of the app. 409 if it's on.
func (h Accounts) StartTwoFactor(c *web.Ctx, in PasswordInput) (TwoFactorSetupResponse, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return TwoFactorSetupResponse{}, err
	}
	if err := h.checkPassword(c, u, in.Password); err != nil {
		return TwoFactorSetupResponse{}, err
	}
	setup, err := h.Auth.StartTwoFactor(c, u, u.Email)
	if err != nil {
		return TwoFactorSetupResponse{}, err // auth.ErrTwoFactorOn: 409
	}
	svg, err := qr.SVG(setup.URI, qr.M, 200)
	if err != nil {
		return TwoFactorSetupResponse{}, err
	}
	return TwoFactorSetupResponse{Secret: setup.Secret, URI: setup.URI, QRCode: svg}, nil
}

// ConfirmTwoFactor turns on two-factor authentication with a code of the app,
// and answers the recovery codes. The user's other API tokens are
// revoked: they were made with the password alone.
func (h Accounts) ConfirmTwoFactor(c *web.Ctx, in CodeInput) (RecoveryCodesResponse, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return RecoveryCodesResponse{}, err
	}
	codes, err := h.Auth.ConfirmTwoFactor(c, u, in.Code)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCode):
		return RecoveryCodesResponse{}, validate.Fail("code", i18n.T(c, "auth.errors.code"))
	case errors.As(err, &throttled):
		return RecoveryCodesResponse{}, tooMany(c, throttled)
	case err != nil:
		return RecoveryCodesResponse{}, err // auth.ErrTwoFactorOff (not started) or On: 409
	}
	if err := h.revokeOthers(c, u); err != nil {
		return RecoveryCodesResponse{}, err
	}
	return RecoveryCodesResponse{RecoveryCodes: codes}, nil
}

// NewRecoveryCodes replaces the recovery codes, given the current
// password, and answers the new ones. 409 if two-factor authentication is off.
func (h Accounts) NewRecoveryCodes(c *web.Ctx, in PasswordInput) (RecoveryCodesResponse, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return RecoveryCodesResponse{}, err
	}
	if err := h.checkPassword(c, u, in.Password); err != nil {
		return RecoveryCodesResponse{}, err
	}
	codes, err := h.Auth.NewRecoveryCodes(c, u)
	if err != nil {
		return RecoveryCodesResponse{}, err // auth.ErrTwoFactorOff: 409
	}
	return RecoveryCodesResponse{RecoveryCodes: codes}, nil
}

// DisableTwoFactor turns off two-factor authentication, given the current
// password: 204.
func (h Accounts) DisableTwoFactor(c *web.Ctx, in PasswordInput) (web.Empty, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return web.Empty{}, err
	}
	if err := h.checkPassword(c, u, in.Password); err != nil {
		return web.Empty{}, err
	}
	return web.Empty{}, h.Auth.DisableTwoFactor(c, u)
}
