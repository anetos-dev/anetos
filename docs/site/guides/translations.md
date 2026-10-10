---
title: Translations
since: v0.3.0
group: "Languages and time"
weight: 600
---

# Translations

Show your app, its validation messages and error pages, and its emails
in each user's language. Adding a language is adding a file.

## Before you start

- An app made with `anetos new` has translations set up: `locales/` holds
  the English text and `main.go` loads it. For an older app, see step 2.
- Settings: `APP_LOCALE` (default `en`), `APP_FALLBACK_LOCALE` (default
  `en`), `APP_LOCALES`, `APP_LOCALE_STRATEGY` ([reference](../reference/configuration.md)).

## Steps

### 1. Write the catalogs

A catalog is a YAML file in `locales/`, one per locale (`locales/en.yaml`)
or a folder of files (`locales/bn/app.yaml`, `locales/bn/auth.yaml`).
Nested keys join with dots: `home.welcome` below.

```yaml
# locales/en.yaml
app:
  title: "Greenhouse"
home:
  welcome: "Welcome, {name}!"
  plants:
    one: "You have {count} plant."
    other: "You have {count} plants."
```

```yaml
# locales/bn/app.yaml
app:
  title: "গ্রিনহাউস"
home:
  welcome: "স্বাগতম, {name}!"
  plants:
    one: "আপনার {count}টি গাছ আছে।"
    other: "আপনার {count}টি গাছ আছে।"
```

- `{name}` is a placeholder, filled when the message is used.
- A map of plural categories (`zero`, `one`, `two`, `few`, `many`,
  `other`) is a plural message. `other` is required; each language uses
  the categories its grammar has (English `one` and `other`, Arabic all
  six).
- A locale's folder may have subfolders (`locales/bn/admin/users.yaml`).
- Locale names are BCP 47 tags: `en`, `bn`, `pt-BR`. A key missing from
  `bn-BD` comes from `bn`, then from `APP_FALLBACK_LOCALE`, so a regional
  file only needs what differs.
- A plural form is also a key: `home.plants.one`. English never uses
  `zero` (0 is `other`); `locale:check` notes forms a language doesn't use.
- Quote values: `count: 3` is a number, not text, and is refused.

### 2. Load them

Embed the folder and give it to `i18n.New`, before the web server:

```go
// illustrative (locales/locales.go)
package locales

import "embed"

// FS holds the catalogs, for i18n.New.
//
//go:embed *
var FS embed.FS
```

```go
// setup loads the catalogs and adds the routes. The HTTP server finds
// each request's locale with the app's translator.
func setup(app *anetos.App) (*web.Server, error) {
	// locales.FS embeds locales/: en.yaml and bn/*.yaml.
	if _, err := i18n.New(app, locales.FS); err != nil {
		return nil, err
	}
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	r := srv.Router()
	r.Get("/", home).Name("home")
	r.Post("/signup", web.H(signUp)).Name("signup")
	return srv, nil
}
```

(Copied from [`examples/i18n/main.go`](../../../examples/i18n/main.go), region `setup`.)

The supported locales are `APP_LOCALE` and every locale with a catalog,
unless `APP_LOCALES` lists them. In development, `anetos dev` rebuilds
when a catalog changes, and keys missing from every catalog are logged
once.

### 3. Translate in code and templates

`i18n.T` returns a message in the request's language; `i18n.Plural`
picks the form for a number and fills `{count}`:

```go
// home greets the visitor in their language: ?name= and ?plants= fill
// the messages. Dates, prices and times follow the language too.
func home(c *web.Ctx) error {
	name := c.Query("name")
	if name == "" {
		name = "Ada"
	}
	plants, _ := strconv.Atoi(c.Query("plants"))
	now := anetos.Now(c)
	return c.Render(http.StatusOK, view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, `<!doctype html><html lang="%s" dir="%s"><title>%s</title><h1>%s</h1><p>%s</p><p>%s</p><p>%s</p><p>%s</p>%s`,
			i18n.Locale(ctx), i18n.Dir(ctx),
			html.EscapeString(i18n.T(ctx, "app.title")),
			html.EscapeString(i18n.T(ctx, "home.welcome", "name", name)),
			html.EscapeString(i18n.Plural(ctx, "home.plants", plants)),
			html.EscapeString(i18n.T(ctx, "home.today", "date", i18n.Date(ctx, now, i18n.Full))),
			html.EscapeString(i18n.T(ctx, "home.price", "price", i18n.Currency(ctx, 1250, "BDT"))),
			html.EscapeString(i18n.T(ctx, "home.watered", "when", i18n.Ago(ctx, now.Add(-3*time.Hour)))),
			switcher(ctx))
		return err
	}))
}

// switcher links to this page in each language the app supports, by the
// language's own name.
func switcher(ctx context.Context) string {
	var b strings.Builder
	b.WriteString("<nav>" + html.EscapeString(i18n.T(ctx, "nav.language")) + ":")
	for _, l := range i18n.From(ctx).Supported() {
		fmt.Fprintf(&b, ` <a href="%s" hreflang="%s">%s</a>`, html.EscapeString(web.LocaleURL(ctx, l)), l, html.EscapeString(i18n.LanguageName(ctx, l)))
	}
	b.WriteString("</nav>")
	return b.String()
}
```

(Copied from [`examples/i18n/main.go`](../../../examples/i18n/main.go), region `home`.)

In templ, `ctx` is there already:

```go
// illustrative (views/layout.templ)
<html lang={ i18n.Locale(ctx) } dir={ i18n.Dir(ctx) }>
	<h1>{ i18n.T(ctx, "home.welcome", "name", user.Name) }</h1>
```

Arguments are pairs of names and values. A key no catalog has shows the
key itself, so a mistake is visible on the page; `locale:check` (step 7)
finds them before users do.

### 4. Choose how visitors get their language

A request's locale is worked out the first time something asks for it.
`APP_LOCALE_STRATEGY` decides how:

| `APP_LOCALE_STRATEGY` | URLs | A visitor who prefers Bangla |
|---|---|---|
| `none` (default) | `/about` in every language | sees `/about` in Bangla |
| `prefix` | `/about` (English, the default), `/bn/about` | opening `/about` is redirected to `/bn/about` |
| `subdomain` | `example.com/about`, `bn.example.com/about` (needs `APP_URL`) | opening `example.com` is redirected to `bn.example.com` |

With `none`, the locale is the first supported one of:

1. the `locale` cookie, set when the visitor chose a language on this
   device;
2. the session's `locale`;
3. the logged-in user's preference (step 5);
4. the browser's `Accept-Language`;
5. `APP_LOCALE`.

With `prefix` and `subdomain` the URL decides; an address without a
locale is in `APP_LOCALE`, so an unprefixed JSON API answers in it. The
redirect to the visitor's language only happens for pages (GET requests
for HTML, not API calls or htmx), and visiting a language's URL remembers
it in the cookie. Route URLs (`web.URL`, `c.URL`, `web.RedirectRoute`)
keep the current locale; for a literal path, use
`web.LocalePath(ctx, "/dashboard")`.

A language switcher links to `web.LocaleURL(ctx, locale)`, the current
page in that language: `/bn/about` with `prefix`, `bn.example.com/about`
with `subdomain`, `/about?locale=bn` with `none` (handled for pages only,
and only for a supported locale; other `locale` parameters reach your
routes). Following one remembers the choice. A settings form can call
`c.SetLocale(locale)` instead, which stores it in the cookie and the
session; `c.ForgetLocale()` drops them, back to the user's preference or
the browser's language.

A locale matches itself or its nearest supported parent (`bn-BD` →
`bn`), else a close regional variant (`fr-CH` → `fr-CA`).

### 5. Respect users' preferences

A user model can carry the language they read the site in, the one their
email and notifications should be in, and their time zone. Implement any
of these methods:

```go
// illustrative
type User struct {
	db.Model
	Locale     string `db:"locale"`      // the site's language
	MailLocale string `db:"mail_locale"` // email; empty: the site's
	TimeZone   string `db:"time_zone"`   // an IANA name: Asia/Dhaka
}

func (u *User) PreferredLocale() string     { return u.Locale }     // i18n.LocalePreference
func (u *User) PreferredMailLocale() string { return u.MailLocale } // i18n.MailLocalePreference
func (u *User) PreferredTimeZone() string   { return u.TimeZone }   // i18n.TimeZonePreference
```

With `auth.New`, a logged-in user's `PreferredLocale` is the request's
with `APP_LOCALE_STRATEGY=none` (unless they chose another on this device), and
`i18n.TimeZone(ctx)` is their zone, which `i18n.Date` and `i18n.Time`
show times in ([Numbers, dates and languages](formatting.md)). A settings
page that saves their
language should also call `c.SetLocale`, so the cookie follows;
`i18n.TimeZones()` lists the zones to choose from (UTC, then one per
region of each country), as `make:auth`'s settings page does. To write
to a user, switch to their communication language first:

```go
// illustrative
err := mailer.Queue(i18n.ForUser(ctx, u), mails.Receipt{Order: o})
```

Mail is rendered when it is sent or queued, in the language of its
context. Queue jobs run in the locale and time zone of the context that
dispatched them, so a job dispatched by a Bangla page, or with
`i18n.ForUser`, writes Bangla.

### 6. Translate the framework's messages

The framework's own text (validation messages, error pages, login
messages, month names) is in its English catalog. For Bangla, French and
Spanish, copy the community's translations into your catalogs, with
`make:auth`'s pages and emails if you have them:

```sh
go tool anetos locale:add bn
```

([Numbers, dates and languages](formatting.md#1-add-a-languages-formats-and-messages).)
It leaves out the keys your catalogs for the locale already define. For
another language, or to change a message, define the same keys in a
locale's catalog (or edit the copied file), or in `en` to change the
English:

```yaml
# locales/bn/validation.yaml
validation:
  required: "{label} দিতে হবে।"
  email: "{label} একটি সঠিক ইমেইল ঠিকানা হতে হবে।"
  min:
    numeric: "{label} কমপক্ষে {0} হতে হবে।"
  attributes:
    name: "নাম"
    email: "ইমেইল"
    plant_count: "গাছের সংখ্যা"
```

- `validation.<rule>` (and `validation.<rule>.string|numeric|array` for
  size rules) are the messages; the keys are listed in the
  [validation rules](../reference/validation-rules.md#messages).
- `validation.attributes.<field>` is a field's label in messages; without
  it, the `label` tag (itself a key, if a catalog has it) or the field's
  name is used.
- `http.status.<code>` titles error pages; `http.csrf`,
  `http.invalid_data`, `binding.*` and `auth.*` are the other messages.
- A struct's `ValidationMessages` may give catalog keys instead of text,
  and a `web.HTTPError` its `Key` (and `Args` for its placeholders): they
  are translated.

`anetos make:auth` writes its pages' and emails' text to
`locales/en/auth.yaml`: `locale:add` brings its translation, or copy it to
`locales/bn/auth.yaml` and translate.

### 7. Check the catalogs

```sh
go run . locale:check
```

It reports, for each supported locale, the keys the fallback locale has
that it lacks, placeholders that differ, plural forms its language needs,
and keys the `.go` and `.templ` files use that no catalog has. A key the
code completes at run time, `i18n.T(ctx, "issues.status."+s)`, counts as
a prefix: some key must start with `issues.status.` (v0.3). It exits
with status 1 when there is a problem, so it can run in CI, and it notes
the framework messages a locale leaves in English.

## Complete example

[`examples/i18n`](../../../examples/i18n) has English and Bangla
catalogs, a switcher and translated validation:

```sh
go run ./examples/i18n
APP_LOCALE_STRATEGY=prefix go run ./examples/i18n
```

## How it works

Catalogs are read once, at startup. A key is looked up in the request's
locale, its parents, the fallback locale and its parents, then the
framework's English catalog. The request's locale is resolved when first
asked for, so routes that never translate don't pay for it. See
[internationalization](../concepts/internationalization.md).

## Testing it

Send `Accept-Language` (or follow a `?locale=` link), then check the
text:

```go
func TestLanguages(t *testing.T) {
	app := anetostest.New(t, setup)
	app.Freeze(time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC))

	// English by default; plurals follow the count.
	app.Get("/?plants=1").AssertOK().AssertSee(`lang="en"`, "Welcome, Ada!", "You have 1 plant.",
		"Today is Thursday, January 15, 2026.", "A seed pack costs BDT 1,250.00.", "Last watered 3 hours ago.")
	app.Get("/?plants=3").AssertSee("You have 3 plants.")

	// The browser's languages: words, digits, dates and prices in Bangla.
	app.WithHeader("Accept-Language", "bn-BD, en;q=0.8")
	app.Get("/?name=Rafi&plants=2").AssertSee(`lang="bn"`, "স্বাগতম, Rafi!", "আপনার ২টি গাছ আছে।",
		"আজ বৃহস্পতিবার, ১৫ জানুয়ারী, ২০২৬।", "১,২৫০.০০৳", "৩ ঘণ্টা আগে")

	// Validation messages and labels in the visitor's language.
	app.PostJSON("/signup", map[string]any{"email": "nope", "plant_count": 0}).AssertUnprocessable().
		AssertJSONPath("errors.name", "নাম দিতে হবে।").
		AssertJSONPath("errors.email", "ইমেইল একটি সঠিক ইমেইল ঠিকানা হতে হবে।").
		AssertJSONPath("errors.plant_count", "গাছের সংখ্যা কমপক্ষে ১ হতে হবে।")

	// A chosen language (?locale=, the switcher's links) beats the
	// browser's, and is remembered. The switcher names each language in
	// itself.
	app.Get("/?locale=en").AssertRedirect("/")
	app.Get("/").AssertSee(`lang="en"`, "Welcome, Ada!", `href="/?locale=bn" hreflang="bn">বাংলা</a>`)
}
```

(Copied from [`examples/i18n/main_test.go`](../../../examples/i18n/main_test.go), region `test`.)

`i18n.WithLocale(ctx, "bn")` puts any other code in a locale, and the
translator's `Check` method runs `locale:check`'s checks in a test.

## Troubleshooting

| Problem | Cause | Fix |
|---|---|---|
| A page shows `home.welcome` | No catalog has the key, or it's misspelled | `go run . locale:check` |
| A language is never chosen | It isn't supported | Add its catalog, or list it in `APP_LOCALES` |
| `… is not a locale` at startup | A catalog file's name isn't a locale (`english.yaml`) | Name it `en.yaml`, or the folder `en/` |
| `… is 3, not text; quote it` | A number or `true` as a message | Quote it: `"3"` |
| `APP_LOCALE_STRATEGY=subdomain needs APP_URL` | The default locale's host is unknown | Set `APP_URL=https://example.com` |
| Email in the site's language, not the user's | The mail was built with the request's context | `mailer.Send(i18n.ForUser(ctx, u), …)` |
