// SPDX-License-Identifier: Apache-2.0

package anetostest_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
)

type member struct{ ID, Name string }

func (m *member) AuthID() string     { return m.ID }
func (*member) AuthPassword() string { return "" }

// club is an app with social login and an in-memory user table.
type club struct {
	opts     []social.Option
	mu       sync.Mutex
	members  map[string]*member
	profiles []social.Profile
}

func (c *club) setup(app *anetos.App) (*web.Server, error) {
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	sessions, err := session.New(app)
	if err != nil {
		return nil, err
	}
	if _, err := cache.New(app); err != nil { // login throttling
		return nil, err
	}
	a, err := auth.New(app, auth.Users[*member]{
		ByID: func(_ context.Context, id string) (*member, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if m, ok := c.members[id]; ok {
				return m, nil
			}
			return nil, auth.ErrUserNotFound
		},
		ByLogin: func(context.Context, string) (*member, error) { return nil, auth.ErrUserNotFound },
	})
	if err != nil {
		return nil, err
	}
	s, err := social.New(app, a, func(_ context.Context, p social.Profile) (*member, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.profiles = append(c.profiles, p)
		if !p.EmailVerified {
			return nil, &social.NoAccountError{Message: "No verified address."}
		}
		m := &member{ID: p.Provider + ":" + p.Subject, Name: p.Name}
		c.members[m.ID] = m
		return m, nil
	}, social.Configured(app, social.Google(), social.GitHub()), c.opts...) // no credentials set
	if err != nil {
		return nil, err
	}
	pages := srv.Router().Group("", sessions.Middleware, web.CSRF(), a.Middleware)
	pages.Get("/auth/{provider}/redirect", s.Redirect)
	pages.Get("/auth/{provider}/callback", s.Callback)
	pages.Get("/login", func(ctx *web.Ctx) error {
		var b strings.Builder
		for _, p := range s.Providers() {
			b.WriteString("Log in with " + s.Title(p) + ". ")
		}
		for _, e := range ctx.Session().Errors() {
			b.WriteString(e.Message)
		}
		return ctx.Text(http.StatusOK, b.String())
	})
	pages.Get("/me", func(ctx *web.Ctx) error {
		m, ok := auth.User[*member](ctx)
		if !ok {
			return ctx.Text(http.StatusOK, "guest")
		}
		return ctx.Text(http.StatusOK, m.ID+" "+m.Name)
	})
	return srv, nil
}

func TestFakeSocial(t *testing.T) {
	c := &club{members: map[string]*member{}}
	app := anetostest.New(t, c.setup, anetostest.FakeSocial())
	app.Get("/login").AssertSee("Log in with Google.", "Log in with GitHub.") // configured without settings

	app.Freeze(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) // the ID token follows the app's clock
	app.SocialLogin("/auth/google/redirect", anetostest.SocialAccount{ID: "g-1", Email: "ada@example.com", EmailVerified: true, Name: "Ada", AvatarURL: "https://example.com/a.png"}).
		AssertRedirect("/")
	app.Get("/me").AssertSee("google:g-1 Ada")

	// GitHub (OAuth 2.0, not OpenID Connect) goes through the stand-in too.
	app.SocialLogin("/auth/github/redirect", anetostest.SocialAccount{ID: "42", Email: "bob@example.com", EmailVerified: true, Name: "Bob"}).
		AssertRedirect("/")
	app.Get("/me").AssertSee("github:42 Bob")

	// The resolver refuses: back to the login page with its message.
	app.SocialLogin("/auth/google/redirect", anetostest.SocialAccount{ID: "g-2", Email: "eve@example.com"}).
		AssertRedirect("/login").Follow().AssertSee("No verified address.")

	// WithHomeURL: where users go without an intended page.
	app2 := anetostest.New(t, (&club{members: map[string]*member{}, opts: []social.Option{social.WithHomeURL("/me")}}).setup, anetostest.FakeSocial())
	app2.SocialLogin("/auth/google/redirect", anetostest.SocialAccount{ID: "g-1", EmailVerified: true}).AssertRedirect("/me")

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.profiles) != 3 {
		t.Fatalf("profiles: %+v", c.profiles)
	}
	p := c.profiles[0]
	if p.Provider != "google" || p.Subject != "g-1" || p.Email != "ada@example.com" || !p.EmailVerified ||
		p.Name != "Ada" || p.AvatarURL != "https://example.com/a.png" || p.Token == nil {
		t.Errorf("profile: %+v", p)
	}
	if c.profiles[2].EmailVerified {
		t.Error("an unverified address came verified")
	}
}

func TestFakeSocialMisuse(t *testing.T) {
	c := &club{members: map[string]*member{}}
	if msg := fatalOf(func() {
		anetostest.New(&fakeT{TB: t}, c.setup).SocialLogin("/auth/google/redirect", anetostest.SocialAccount{ID: "1"})
	}); !strings.Contains(msg, "needs the FakeSocial option") {
		t.Errorf("without FakeSocial: %q", msg)
	}
	app := anetostest.New(&fakeT{TB: t}, c.setup, anetostest.FakeSocial())
	if msg := fatalOf(func() { app.SocialLogin("/auth/google/redirect", anetostest.SocialAccount{}) }); !strings.Contains(msg, "needs an ID") {
		t.Errorf("no ID: %q", msg)
	}
	if msg := fatalOf(func() { app.SocialLogin("/login", anetostest.SocialAccount{ID: "1"}) }); !strings.Contains(msg, "didn't redirect to the provider") {
		t.Errorf("not a redirect route: %q", msg)
	}
}
