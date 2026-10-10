// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	pathpkg "path"
	"strings"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/web"
)

// security is what the admin needs of the app's auth.Auth to protect
// itself, without its user type.
type security struct {
	// confirmed reports whether the user confirmed their password lately.
	confirmed func(ctx context.Context) bool
	// confirm checks the user's password.
	confirm func(ctx context.Context, pw string) error
	// hasPassword reports whether the user has a password to confirm.
	hasPassword func(ctx context.Context) bool
	// twoFactorOn reports whether the user has two-factor authentication on;
	// nil if the app has none.
	twoFactorOn func(ctx context.Context) (bool, error)
	// twoFactorURL is where users turn it on (AUTH_TWO_FACTOR_URL).
	twoFactorURL string
	// settingsURL is the user's account settings (AUTH_SETTINGS_URL).
	settingsURL string
}

func securityOf[U auth.Authenticatable](a *auth.Auth[U]) security {
	s := security{
		confirmed: a.PasswordConfirmed,
		confirm:   a.ConfirmPassword,
		hasPassword: func(ctx context.Context) bool {
			u, ok := auth.User[U](ctx)
			return ok && u.AuthPassword() != ""
		},
		twoFactorURL: a.Config().TwoFactorURL,
		settingsURL:  a.Config().SettingsURL,
	}
	if a.SupportsTwoFactor() {
		s.twoFactorOn = func(ctx context.Context) (bool, error) {
			u, err := auth.Current[U](ctx)
			if err != nil {
				return false, err
			}
			st, err := a.TwoFactor(u)
			return st.On, err
		}
	}
	return s
}

// parseAllowed parses ADMIN_ALLOW_IPS: addresses and networks.
func parseAllowed(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("admin: ADMIN_ALLOW_IPS: %q is neither an address nor a network (10.0.0.0/8)", s)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// allowIPs lets in only the addresses of ADMIN_ALLOW_IPS (the client's,
// as web.RealIP finds it behind trusted proxies); others get 404, as if
// there were no admin.
func (p *Panel) allowIPs(next http.Handler) http.Handler {
	if len(p.allowed) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, err := netip.ParseAddr(web.ClientIP(r))
		if err == nil {
			a = a.Unmap()
			for _, pre := range p.allowed {
				if pre.Contains(a) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		web.WriteError(w, r, web.Error(http.StatusNotFound, ""))
	})
}

// twoFactorPath is the page telling users to turn on two-factor authentication.
const twoFactorPath = "/two-factor-required"

// requireTwoFactor, with ADMIN_TWO_FACTOR=required, lets in only users
// with two-factor authentication on; others are sent to a page telling them to
// turn it on (requests other than GET get 403).
func (p *Panel) requireTwoFactor(next http.Handler) http.Handler {
	if p.cfg.TwoFactor != "required" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		on, err := p.sec.twoFactorOn(r.Context())
		switch {
		case err != nil:
			web.WriteError(w, r, err)
		case on:
			next.ServeHTTP(w, r)
		case r.Method == http.MethodGet && r.Header.Get("HX-Request") == "":
			http.Redirect(w, r, p.base+twoFactorPath, http.StatusSeeOther)
		default:
			web.WriteError(w, r, web.Error(http.StatusForbidden, "Turn on two-factor authentication to use the admin."))
		}
	})
}

func (p *Panel) twoFactorRequired(c *web.Ctx) error {
	if on, err := p.sec.twoFactorOn(c); err != nil {
		return err
	} else if on {
		return c.Redirect(http.StatusSeeOther, p.URL())
	}
	return p.render(c, "twofactor", page{Title: "Two-factor authentication required", status: http.StatusForbidden,
		Data: struct{ URL string }{p.appURL(c, p.sec.twoFactorURL)}})
}

// confirmPath is the admin's password confirmation page.
const confirmPath = "/confirm"

// mustConfirm reports whether the user must confirm their password
// before a dangerous action (ADMIN_CONFIRM).
func (p *Panel) mustConfirm(c *web.Ctx) bool {
	return p.cfg.Confirm && !p.sec.confirmed(c)
}

// askConfirm sends the user to confirm their password, then back to
// back (the page the action was on) to do it again.
func (p *Panel) askConfirm(c *web.Ctx, back string) error {
	flash(c, "admin.status", "Confirm your password to go on.")
	return c.Redirect(http.StatusSeeOther, p.base+confirmPath+"?"+url.Values{"back": {back}}.Encode())
}

// confirmFirst runs h once the user confirmed their password; before,
// it asks them to, then sends them back: to the page for a GET (a form
// that will post), else to the page the form was on.
func (p *Panel) confirmFirst(h web.HandlerFunc) web.HandlerFunc {
	return func(c *web.Ctx) error {
		if !p.mustConfirm(c) {
			return h(c)
		}
		if r := c.Request(); r.Method == http.MethodGet {
			return p.askConfirm(c, p.local(r.URL.RequestURI(), p.URL()))
		}
		return p.askConfirm(c, p.back(c, p.URL()))
	}
}

// back returns the admin page a request came from (its Referer), or
// fallback.
func (p *Panel) back(c *web.Ctx, fallback string) string {
	r := c.Request()
	u, err := url.Parse(r.Referer())
	if err != nil || u.Host != r.Host || u.User != nil {
		return fallback
	}
	return p.local(u.RequestURI(), fallback)
}

// local returns u if it is a page of the admin, else fallback.
func (p *Panel) local(u, fallback string) string {
	if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") || strings.HasPrefix(u, "/\\") ||
		strings.ContainsFunc(u, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fallback
	}
	path, _, _ := strings.Cut(u, "?")
	// No dot segments or backslashes, which could lead out of the admin.
	if strings.Contains(path, "\\") || pathpkg.Clean(path) != strings.TrimSuffix(path, "/") && path != "/" {
		return fallback
	}
	if p.base != "" && path != p.base && !strings.HasPrefix(path, p.base+"/") || path == p.base+confirmPath {
		return fallback
	}
	return u
}

type confirmData struct {
	Action, Back, Error string
	NoPassword          bool
}

func (p *Panel) confirmPage(c *web.Ctx) error {
	d := confirmData{Action: p.base + confirmPath, Back: p.local(c.Query("back"), p.URL()), NoPassword: !p.sec.hasPassword(c)}
	return p.render(c, "confirm", page{Title: "Confirm your password", Data: d})
}

func (p *Panel) confirmPassword(c *web.Ctx) error {
	req := c.Request()
	d := confirmData{Action: p.base + confirmPath, Back: p.local(req.PostFormValue("back"), p.URL()), NoPassword: !p.sec.hasPassword(c)}
	err := p.sec.confirm(c, req.PostFormValue("password"))
	var throttled *auth.ThrottledError
	switch {
	case err == nil:
		return done(c, "Password confirmed: go on.", d.Back)
	case errors.Is(err, auth.ErrInvalidCredentials):
		d.Error = "That isn't your password."
	case errors.As(err, &throttled):
		d.Error = fmt.Sprintf("Too many tries: wait %d seconds.", int(throttled.RetryAfter.Seconds())+1)
	default:
		return err
	}
	return p.render(c, "confirm", page{Title: "Confirm your password", status: http.StatusUnprocessableEntity, Data: d})
}

// appURL is the URL of a page of the app's, path: on APP_URL when the
// admin has a host of its own.
func (p *Panel) appURL(c *web.Ctx, path string) string {
	to := web.LocalePath(c, path)
	if p.cfg.Host != "" {
		to = strings.TrimSuffix(p.app.Config().URL, "/") + to
	}
	return to
}
