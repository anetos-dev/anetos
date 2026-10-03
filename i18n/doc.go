// SPDX-License-Identifier: Apache-2.0

// Package i18n translates an app's messages, the framework's included,
// into the user's language.
//
// Messages live in YAML catalogs in the app's locales folder, embedded in
// the binary: one file per locale (bn.yaml) or a folder of files
// (bn/app.yaml, bn/auth.yaml). Nested keys join with dots, {name} is a
// placeholder, and a map of CLDR plural categories is a plural message:
//
//	nav:
//	  home: "হোম"
//	welcome: "স্বাগতম, {name}"
//	posts:
//	  count:
//	    one: "{count}টি পোস্ট"
//	    other: "{count}টি পোস্ট"
//
// [ForApp] loads them; [T] and [Plural] translate in the locale of a
// context:
//
//	tr, err := i18n.ForApp(app, locales.FS)
//	i18n.T(ctx, "welcome", "name", user.Name)
//	i18n.Plural(ctx, "posts.count", n)
//
// A key missing from a locale comes from its parents (bn-BD, then bn),
// then APP_FALLBACK_LOCALE (default en), then the framework's English
// catalog, which has the validation messages, error pages and other
// messages the framework shows; an app overrides one by defining its key.
//
// A request's locale is resolved when first needed. With LOCALE_URL=none
// (the default): the locale cookie, the session, the signed-in user's
// preference ([LocalePreference]), Accept-Language, then APP_LOCALE. With
// prefix or subdomain: the URL's, else APP_LOCALE. Queue jobs carry the
// locale and time zone of the context that dispatched them. [ForUser]
// switches a context to a user's language and zone for mail.
//
// See docs/site/guides/translations.md.
package i18n
