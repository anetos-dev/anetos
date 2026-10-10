// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"

	"anetos.dev/anetos"
)

// T returns the message of key in ctx's locale ([Locale]), with its
// placeholders filled from args, pairs of names and values:
//
//	i18n.T(ctx, "welcome", "name", user.Name) // welcome: "Welcome, {name}"
//
// A key missing from every catalog returns the key itself (and is logged
// once in development). Values are formatted with fmt.Sprint; placeholders
// without a value are kept as they are. A plural message gives its
// "other" form: use [Plural].
func T(ctx context.Context, key string, args ...any) string {
	tr := From(ctx)
	locale := Locale(ctx)
	m, _ := tr.lookup(locale, key)
	if m == nil {
		tr.missingKey(ctx, locale, key)
		return key
	}
	text := m.text
	if m.plural != nil {
		text = m.plural[plural.Other]
	}
	return fill(text, args)
}

// Plural returns the form of key's plural message that fits n in ctx's
// locale, with {count} filled with n (formatted as [Number] does: 1,234
// in English, ১,২৩৪ in Bangla) and the other placeholders from args, as
// for [T]:
//
//	i18n.Plural(ctx, "posts.count", n) // posts.count: {one: "{count} post", other: "{count} posts"}
//
// The forms are the CLDR categories zero, one, two, few, many and other;
// a form the message lacks falls back to other. The form follows the
// rules of the language of the catalog the message came from, so an
// English fallback message says "1 post" to a Japanese visitor. A plain
// message is used for every n.
func Plural(ctx context.Context, key string, n int, args ...any) string {
	tr := From(ctx)
	locale := Locale(ctx)
	m, from := tr.lookup(locale, key)
	if m == nil {
		tr.missingKey(ctx, locale, key)
		return key
	}
	args = append([]any{"count", tr.formatCount(locale, n)}, args...)
	if m.plural == nil {
		return fill(m.text, args)
	}
	text, ok := m.plural[pluralForm(from, n)]
	if !ok {
		text = m.plural[plural.Other]
	}
	return fill(text, args)
}

// pluralForm returns the CLDR plural category of the integer n in tag's
// language.
func pluralForm(tag language.Tag, n int) plural.Form {
	if n < 0 {
		n = -n
	}
	return plural.Cardinal.MatchPlural(tag, n, 0, 0, 0, 0)
}

// Lookup returns key's message in ctx's locale without filling its
// placeholders (a plural message's "other" form), and whether a catalog
// has it. Packages with their own placeholders, such as validate, use it.
func Lookup(ctx context.Context, key string) (string, bool) {
	m, _ := From(ctx).lookup(Locale(ctx), key)
	switch {
	case m == nil:
		return "", false
	case m.plural != nil:
		return m.plural[plural.Other], true
	}
	return m.text, true
}

// Has reports whether a catalog has key, in ctx's locale or the ones it
// falls back to.
func Has(ctx context.Context, key string) bool {
	m, _ := From(ctx).lookup(Locale(ctx), key)
	return m != nil
}

// fill replaces {name} placeholders with the values of args, pairs of
// names and values. Unknown placeholders are kept.
func fill(text string, args []any) string {
	if len(args) < 2 || !strings.Contains(text, "{") {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + 16)
	for {
		i := strings.IndexByte(text, '{')
		if i < 0 {
			break
		}
		end := strings.IndexByte(text[i:], '}')
		if end < 0 {
			break
		}
		name := text[i+1 : i+end]
		v, ok := arg(args, name)
		b.WriteString(text[:i])
		if ok {
			b.WriteString(v)
			text = text[i+end+1:]
		} else {
			b.WriteByte('{')
			text = text[i+1:]
		}
	}
	b.WriteString(text)
	return b.String()
}

// arg returns the value of name in pairs, formatted.
func arg(pairs []any, name string) (string, bool) {
	for i := 0; i+1 < len(pairs); i += 2 {
		if k, ok := pairs[i].(string); ok && k == name {
			switch v := pairs[i+1].(type) {
			case string:
				return v, true
			case int:
				return strconv.Itoa(v), true
			default:
				return fmt.Sprint(v), true
			}
		}
	}
	return "", false
}

// ---- the locale and time zone of a context ----

type localeKey struct{}   // a locale set with WithLocale: a string
type resolverKey struct{} // a request's resolver: *request
type zoneKey struct{}     // a zone set with WithTimeZone
type resolvingKey struct{}

// request is a request's locale and time zone, resolved on first use.
type request struct {
	mu      sync.Mutex
	done    chan struct{} // closed once resolved
	started bool
	locale  string
	zone    *time.Location
	resolve func(ctx context.Context) (string, *time.Location)
}

// get returns the resolved locale and zone, resolving them on the first
// call; concurrent callers wait for it. ok is false when the resolution
// itself asks (its resolve function translates something).
func (r *request) get(ctx context.Context) (string, *time.Location, bool) {
	if ctx.Value(resolvingKey{}) == r {
		return "", nil, false
	}
	r.mu.Lock()
	if !r.started {
		r.started = true
		r.mu.Unlock()
		var locale string
		var zone *time.Location
		defer func() { // also when resolve panics: waiters get the default
			r.mu.Lock()
			r.locale, r.zone = locale, zone
			close(r.done)
			r.mu.Unlock()
		}()
		locale, zone = r.resolve(context.WithValue(ctx, resolvingKey{}, r))
		return locale, zone, true
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return r.locale, r.zone, true
	case <-ctx.Done():
		return "", nil, false
	}
}

// WithResolver returns ctx whose locale and time zone are resolve's
// results, computed the first time [Locale] or [TimeZone] asks and kept;
// concurrent first calls wait for the one resolving. The HTTP server
// uses it for each request: resolve gets the context of that first call,
// which has the request's session and signed-in user only when the first
// call comes from a handler or a middleware after theirs (a middleware of
// the app's that translates before them fixes the locale without them).
// While resolve runs, [Locale] in its context gives the default; if it
// panics, the default is kept. Other goroutines of the request wait for
// it (or for their context to end), so code resolve calls, such as
// loading the signed-in user, shouldn't wait for one of them.
func WithResolver(ctx context.Context, resolve func(ctx context.Context) (locale string, zone *time.Location)) context.Context {
	return context.WithValue(ctx, resolverKey{}, &request{resolve: resolve, done: make(chan struct{})})
}

// Locale returns the locale of ctx: the one set with [WithLocale] (or
// [ForUser]), else the request's (with APP_LOCALE_STRATEGY=none, the locale
// cookie, the session, the signed-in user's preference, Accept-Language;
// with prefix or subdomain, the URL's), else the default (APP_LOCALE).
// It is canonical: "en", "pt-BR".
func Locale(ctx context.Context) string {
	if l, ok := ctx.Value(localeKey{}).(string); ok {
		return l
	}
	if r, ok := ctx.Value(resolverKey{}).(*request); ok {
		if l, _, ok := r.get(ctx); ok && l != "" {
			return l
		}
	}
	return From(ctx).Default()
}

// WithLocale returns ctx with locale as its locale, matched against the
// supported locales of the translator in ctx (bn-BD becomes bn; an
// unsupported one becomes the default).
func WithLocale(ctx context.Context, locale string) context.Context {
	tr := From(ctx)
	l, ok := tr.Match(locale)
	if !ok {
		l = tr.Default()
	}
	return context.WithValue(ctx, localeKey{}, l)
}

// TimeZone returns the time zone times are shown in for ctx: the one set
// with [WithTimeZone] (or [ForUser]), else the signed-in user's, else the
// app's (APP_TIMEZONE).
func TimeZone(ctx context.Context) *time.Location {
	if loc, ok := ctx.Value(zoneKey{}).(*time.Location); ok && loc != nil {
		return loc
	}
	if r, ok := ctx.Value(resolverKey{}).(*request); ok {
		if _, z, ok := r.get(ctx); ok && z != nil {
			return z
		}
	}
	return anetos.Location(ctx)
}

// TimeZones returns the time zones users choose from: UTC, then an IANA
// zone for each region of each country ("Asia/Dhaka"), sorted. Each one
// loads with time.LoadLocation (the zone database is built in).
func TimeZones() []string { return slices.Clone(zones) }

// WithTimeZone returns ctx with loc as the time zone times are shown in.
func WithTimeZone(ctx context.Context, loc *time.Location) context.Context {
	return context.WithValue(ctx, zoneKey{}, loc)
}

// ---- users' preferences ----

// LocalePreference is implemented by user models with a preferred
// locale: the language the site is shown in to them.
type LocalePreference interface {
	// PreferredLocale returns a locale ("bn"), or "" for none.
	PreferredLocale() string
}

// CommunicationPreference is implemented by user models with a locale
// for mail and notifications, which may differ from the site's. Without
// it, or when it returns "", their [LocalePreference] is used.
type CommunicationPreference interface {
	// CommunicationLocale returns a locale ("en"), or "" for the
	// display one.
	CommunicationLocale() string
}

// TimeZonePreference is implemented by user models with a time zone (an
// IANA name) to show times in.
type TimeZonePreference interface {
	// PreferredTimeZone returns an IANA name ("Asia/Dhaka"), or "" for
	// the app's.
	PreferredTimeZone() string
}

// ForUser returns ctx in user's communication locale and time zone, for
// mail and notifications sent to them:
//
//	err := mailer.Send(i18n.ForUser(ctx, u), mails.Welcome{User: u})
//
// Preferences user doesn't have (or that are empty or unknown) leave ctx's:
// right for mail to the visitor making the request (a sign-up's welcome),
// but mail an admin's request sends to another user should start from a
// context without the request's locale (i18n.WithLocale(ctx,
// tr.Default())) when users may lack preferences.
func ForUser(ctx context.Context, user any) context.Context {
	locale := ""
	if p, ok := user.(LocalePreference); ok {
		locale = p.PreferredLocale()
	}
	if p, ok := user.(CommunicationPreference); ok {
		if l := p.CommunicationLocale(); l != "" {
			locale = l
		}
	}
	if locale != "" {
		if l, ok := From(ctx).Match(locale); ok {
			ctx = context.WithValue(ctx, localeKey{}, l)
		}
	}
	if loc := userZone(user); loc != nil {
		ctx = WithTimeZone(ctx, loc)
	}
	return ctx
}

// userZone returns user's preferred time zone, or nil.
func userZone(user any) *time.Location {
	p, ok := user.(TimeZonePreference)
	if !ok {
		return nil
	}
	name := p.PreferredTimeZone()
	if name == "" || name == "Local" {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

type userKey struct{}

// SetCurrentUser tells the app how to find the signed-in user of a
// request's context, for [Preferences]. Package auth calls it in New.
func SetCurrentUser(app *anetos.App, current func(ctx context.Context) (any, bool)) {
	app.AddContextValue(userKey{}, current)
}

// Preferences returns the signed-in user's preferred locale (matched
// against the supported locales; "" if none) and time zone (nil if none).
func Preferences(ctx context.Context) (locale string, zone *time.Location) {
	current, ok := ctx.Value(userKey{}).(func(context.Context) (any, bool))
	if !ok {
		return "", nil
	}
	user, ok := current(ctx)
	if !ok || user == nil {
		return "", nil
	}
	if p, ok := user.(LocalePreference); ok && p.PreferredLocale() != "" {
		locale, _ = From(ctx).Match(p.PreferredLocale())
	}
	return locale, userZone(user)
}
