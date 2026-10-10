---
title: Internationalization
since: v0.3.0
group: "Web"
weight: 203
---

# Internationalization

How Anetos finds the words to show a user: where messages come from, how
a request's language is chosen, and how the language travels with work
done for the user later.

## Catalogs and lookup

Messages live in YAML catalogs embedded in the binary: the app's
(`locales/`), and the framework's own English one, which holds its
validation messages, error pages and login messages. They are read once,
when the app starts; a broken file stops it there, not on a user's page.

A key is looked up in a chain of catalogs, the first that has it wins:

1. the locale's (`bn-BD`),
2. its parents' (`bn`),
3. `APP_FALLBACK_LOCALE`'s and its parents' (`en`),
4. the framework's English catalog.

For an English locale the framework's catalog comes right after the app's
English one, so an app whose fallback is Bangla still shows English
visitors English validation messages.

So a language can be translated a little at a time: what it lacks shows
in the fallback language, and `lang:check` lists it. An app changes a
framework message by defining its key; there is no separate mechanism
for overrides.

Placeholders are `{name}`, the style of validation messages. Plural
messages are keyed by CLDR plural categories, and the category a number
falls in comes from the CLDR rules of the message's language
(`golang.org/x/text`), so Arabic gets six forms and Bangla two without
the app knowing the rules, and an English message shown in place of a
missing Japanese one still says "1 post".

## The request's language

Package `i18n` keeps a locale in the context. The HTTP server gives every
request a resolver that runs the first time something asks
(`i18n.Locale`, `i18n.T`, a validation message, the error page), and
keeps its answer for the request. Resolving lazily means a route that
never shows text never loads the user to find their language.

With a URL strategy (`APP_LOCALE_STRATEGY=prefix` or `subdomain`) the URL is
authoritative, so a page has one language per address, which search
engines and shared links need: the locale is the URL's, or `APP_LOCALE`
for an address without one. The cookie and the browser only decide where
a visitor asking for a page without a locale is sent.

Without one (`none`), the same address shows each visitor their language,
the first supported locale of: the `locale` cookie, the session, the
logged-in user's preference, `Accept-Language`, `APP_LOCALE`. The cookie
and session hold a choice made on this device, which beats the account's
default: someone who usually reads Bangla can switch to English on a
shared computer. Responses say they vary by `Accept-Language` and
`Cookie`, so caches keep one copy per language.

A locale matches itself or its nearest supported parent (`bn-BD` →
`bn`), else a supported locale close enough to read (`fr-CH` → `fr-CA`);
a Chinese visitor is never shown Traditional Chinese for Simplified.

## Language that outlives the request

Work done for a user often happens elsewhere: in a queue job, an email,
an AI agent's reply. The context carries the locale and the time zone, so:

- a queue job records them when dispatched and runs in them;
- an email is rendered in the context it is sent with, and
  `i18n.ForUser` switches that context to the recipient's communication
  language and zone, which can differ from the site's;
- anything that receives the context (a validation in a job, a message
  in a listener) speaks the same language.

## Numbers, dates and languages

Numbers, percentages and currency symbols come from `golang.org/x/text`,
which carries CLDR's number data for every language: grouping (Indian
lakhs for Bangla), separators, digits. The digits follow CLDR's choice
for the language (Bangla digits for `bn`), and a catalog can ask for
another numbering system; a plural's `{count}` and the numbers of size
rules' validation messages are formatted the same way, so a sentence
never mixes digit systems.

x/text has no localized dates, and the date libraries that do are large.
So a catalog's `format` section holds what dates need: month and day
names and CLDR patterns for four styles. A language's file brings them,
the core needs only its English, and a project fixes a pattern in its
own catalog like any message. Times are shown in the context's zone, the
logged-in user's, which is what makes "today" theirs; `anetos.Date`
values have no zone and are shown as they are.

The framework ships English only. Other languages' translations of its
messages live apart, in `anetos.dev/locales`, where native speakers can
improve them without waiting for a release. `anetos lang:add` copies a
language into the project, through the Go module proxy (versioned and
checksummed like any dependency), and from then on the files are the
project's: no runtime dependency, nothing replaced behind its back.

## What it deliberately doesn't do

- **No generated message functions.** Keys are strings, so a catalog is
  the only thing to edit; `lang:check` reads the source for the keys it
  uses and reports undefined ones.
- **No translated model content** (a post's title in several languages)
  yet: that is the app's data, and a search index has one language.
