---
title: Numbers, dates and languages
since: v0.3.0
group: "Languages and time"
weight: 601
---

# Numbers, dates and languages

Show numbers, prices, dates and times the way each user reads them, in
their language and time zone, and add the framework's translations for
a language with one command.

## Before you start

- Translations set up with `i18n.New` ([Translations](translations.md)):
  an app made with `anetos new` has them.
- Everything below follows the request's locale (`i18n.Locale(ctx)`) and
  time zone (`i18n.TimeZone(ctx)`: the logged-in user's, or
  `APP_TIMEZONE`).

## Steps

### 1. Add a language's formats and messages

The framework's English catalog has its validation messages, error pages,
month and day names and date patterns. For another language, copy its
translations from [anetos.dev/locales](https://github.com/anetos-dev/locales):

```sh
go tool anetos lang:add bn fr
```

It writes `locales/bn/framework.yaml` (and `locales/bn/auth.yaml` if
your app has `make:auth`'s pages). The files are then yours: change any
message there.

- Keys your catalogs for the locale already define (a
  `locales/bn/validation.yaml` of your own) are left out, since a locale
  can't define a key twice; it lists them.
- A regional locale gets its language's translations: `bn-BD` writes
  `locales/bn/`.
- Without a locale it lists the languages available; `-force` replaces
  files you already have with the latest.
- Visitors can choose the language at once (unless `APP_LOCALES` lists
  the supported locales), and your own text shows in
  `APP_FALLBACK_LOCALE` until you translate it: `go run . lang:check`
  lists what's missing.

### 2. Format numbers and prices

```go
// illustrative
i18n.Number(ctx, 1234567.891)       // en: 1,234,567.891   bn: ১২,৩৪,৫৬৭.৮৯১   fr: 1 234 567,891
i18n.Fixed(ctx, 2.5, 2)             // en: 2.50
i18n.Percent(ctx, 0.256)            // en: 26%   fr: 26 %
i18n.Currency(ctx, 1234.5, "USD")   // en: $1,234.50   fr: 1 234,50 $US   bn: ১,২৩৪.৫০ US$
i18n.Currency(ctx, 1250, "BDT")     // en: BDT 1,250.00   bn: ১,২৫০.০০৳
```

- Grouping, separators and digits come from CLDR through
  `golang.org/x/text`: Indian grouping and Bangla digits for `bn`,
  Arabic-Indic digits for `ar`.
- `Currency` takes the amount in the currency's main unit (divide cents
  by 100) and an ISO 4217 code, rounds to the currency's usual decimals
  (none for JPY), and places the symbol with the catalog's
  `format.currency` pattern (`"{symbol}{amount}"` in English).
- `i18n.Plural` formats `{count}` the same way: "1,234 posts".
- Prefer Latin digits for a language? Set its numbering in the catalog:

  ```yaml
  # locales/bn/framework.yaml
  format:
    numbering: "latn"
  ```

### 3. Format dates and times

```go
// illustrative
i18n.Date(ctx, order.PlacedAt)               // en: Jan 15, 2026   bn: ১৫ জানু, ২০২৬
i18n.Date(ctx, user.Birthday, i18n.Long)     // an anetos.Date: January 15, 2026
i18n.Time(ctx, order.PlacedAt, i18n.Short)   // en: 8:04 PM   fr: 20:04
i18n.DateTime(ctx, order.PlacedAt)           // en: Jan 15, 2026, 8:04:05 PM
i18n.Format(ctx, order.PlacedAt, "EEE d MMM") // en: Thu 15 Jan   fr: jeu. 15 janv.
```

- A `time.Time` is shown in the user's zone, so "today" is their today:
  a time stored at 20:00 UTC is the next morning in Dhaka. An
  `anetos.Date` is a day and is shown as it is.
- The styles are `i18n.Short`, `i18n.Medium` (the default), `i18n.Long`
  and `i18n.Full`; each language's catalog has its patterns
  (`format.date.medium: "MMM d, y"`).
- `i18n.Format` takes a [CLDR pattern](../reference/i18n-formats.md)
  for a layout of your own, with the language's names and digits.
- A zero time or date, or a nil `*time.Time` or `*anetos.Date`, gives an
  empty string.
- Month names, patterns and relative times come from the locale's own
  catalogs, else English: never from a fallback locale in another
  language.

### 4. Say how long ago

```go
// illustrative
i18n.Ago(ctx, comment.CreatedAt)      // just now, 3 minutes ago, in 2 days
i18n.Duration(ctx, 80*time.Minute)    // 1 hour
i18n.DurationUp(ctx, 80*time.Minute)  // 2 hours: for "try again in …"
```

`Ago` measures from the app's clock (`anetos.Now`), so tests can freeze
it; a zero time gives "". Amounts are in their largest unit, from
seconds to years: `Duration` rounds to the nearest, `DurationUp` up, so
a wait is never understated.

### 5. Mark up the page's language and direction

Set `lang` and `dir` on `<html>`, so browsers pick fonts and lay out
right-to-left languages (Arabic, Hebrew, Persian, Urdu) from the right:

```go
// illustrative (views/layout.templ)
<html lang={ i18n.Locale(ctx) } dir={ i18n.Dir(ctx) }>
	<head>
		for _, a := range web.Alternates(ctx) {
			<link rel="alternate" hreflang={ a.Locale } href={ a.URL }/>
		}
	</head>
```

`web.Alternates` lists the page's address in each language, and
`x-default`, for search engines, when `APP_LOCALE_STRATEGY` is `prefix` or
`subdomain` (with `none` a page has one address, and it returns nothing).
Set `APP_URL` so the addresses are absolute. A language switcher can name
each language in itself with `i18n.LanguageName(ctx, "bn")` ("বাংলা"),
from the catalog's `format.language`.

`anetos new` writes this layout.

### 6. Answer in the user's language from an AI model

Tell the model which language to use; the user's message alone may not
say:

```go
// illustrative
system := "Answer in " + i18n.LanguageName(ctx, i18n.Locale(ctx)) + "."
```

## Complete example

[`examples/i18n`](../../../examples/i18n) shows a date, a price and a
relative time in English and Bangla:

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

## How it works

Numbers, percentages and currency symbols come from `golang.org/x/text`,
which carries CLDR's number data for every language. Dates don't: their
month and day names and patterns live in the catalogs' `format` section,
so a language brings its own and the framework needs no date library.
Relative times are plural messages (`relative.units.hour`). Formatters
are made once per locale. See
[internationalization](../concepts/internationalization.md).

## Testing it

Freeze the clock, choose a language, and check the page:

```go
// illustrative
app.Freeze(time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC))
app.WithHeader("Accept-Language", "bn")
app.Get("/").AssertSee("১৫ জানুয়ারী, ২০২৬", "১,২৫০.০০৳", "৩ ঘণ্টা আগে")
```

In a unit test, put a context in a locale and zone:
`i18n.WithTimeZone(i18n.WithLocale(ctx, "bn"), dhaka)`.

## Troubleshooting

| Problem | Cause | Fix |
|---|---|---|
| English month names on a Bangla page | The locale's catalog has no `format` section | `go tool anetos lang:add bn`, or add `format.months` and the rest |
| `format.months needs 12 names` from `lang:check` | A list of the wrong length (it is ignored) | Twelve months, seven days (Sunday first), two periods |
| A date is a day off | A `time.Time` shown in another zone, or a date kept in a `time.Time` | Use `anetos.Date` for days; check the user's `PreferredTimeZone` |
| `downloading anetos.dev/locales` fails | No network, or a proxy that blocks it | `lang:add -from <checkout of anetos-dev/locales>` |
| Digits you didn't expect | CLDR's digits for the language | `format.numbering: "latn"` in the catalog |
