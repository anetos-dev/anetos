// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

//go:embed locales
var coreFiles embed.FS

// Config configures translation. See docs/site/reference/configuration.md.
type Config struct {
	// Locale is the default locale: of requests that don't ask for a
	// supported one, and of code outside requests. APP_LOCALE, default en.
	Locale string `env:"APP_LOCALE" default:"en"`

	// Fallback is the locale whose messages a locale's missing ones come
	// from (after the locale's parents: bn-BD, then bn). The framework's
	// own English messages come last. APP_FALLBACK_LOCALE, default en.
	Fallback string `env:"APP_FALLBACK_LOCALE" default:"en"`

	// Locales are the locales the app supports, which requests can ask
	// for. APP_LOCALES, comma-separated; default APP_LOCALE and every
	// locale with a catalog.
	Locales []string `env:"APP_LOCALES"`

	// URL is where a request's locale is in its URL: none (default; the
	// locale comes from the locale cookie, the session, the logged-in
	// user's preference or the browser),
	// prefix (/bn/about; the default locale has none) or subdomain
	// (bn.example.com; APP_URL's host is the default locale's). APP_LOCALE_STRATEGY.
	Strategy string `env:"APP_LOCALE_STRATEGY" was:"LOCALE_URL" default:"none"`
}

// The locale strategies ([Config.Strategy]): where a request's locale
// is in its URL.
const (
	StrategyNone      = "none"
	StrategyPrefix    = "prefix"
	StrategySubdomain = "subdomain"
)

// The former names of the strategies.
//
// Deprecated: Use StrategyNone, StrategyPrefix and StrategySubdomain; these
// are removed in v0.6.
const (
	//go:fix inline
	URLNone = StrategyNone
	//go:fix inline
	URLPrefix = StrategyPrefix
	//go:fix inline
	URLSubdomain = StrategySubdomain
)

// Validate implements config.Validator.
func (c Config) Validate() error {
	var errs []error
	if _, err := parseLocale(c.Locale); err != nil {
		errs = append(errs, fmt.Errorf("APP_LOCALE: %w", err))
	}
	if _, err := parseLocale(c.Fallback); err != nil {
		errs = append(errs, fmt.Errorf("APP_FALLBACK_LOCALE: %w", err))
	}
	for _, l := range c.Locales {
		if _, err := parseLocale(l); err != nil {
			errs = append(errs, fmt.Errorf("APP_LOCALES: %w", err))
		}
	}
	switch c.Strategy {
	case StrategyNone, StrategyPrefix, StrategySubdomain:
	default:
		errs = append(errs, fmt.Errorf("APP_LOCALE_STRATEGY %q is not one of none, prefix, subdomain", c.Strategy))
	}
	return errors.Join(errs...)
}

// LoadConfig reads the APP_LOCALE, APP_FALLBACK_LOCALE, APP_LOCALES and
// APP_LOCALE_STRATEGY settings.
func LoadConfig(src config.Source) (Config, error) { return config.Get[Config](src) }

// Translator translates messages from catalogs: the app's, then the
// framework's own English ones. Create one with [New] (or [NewTranslator]); it
// is safe for concurrent use.
type Translator struct {
	cfg       Config
	def       language.Tag
	fallback  language.Tag
	supported []language.Tag
	matcher   language.Matcher
	cats      map[language.Tag]*catalog // the app's
	core      *catalog                  // the framework's, English
	chains    sync.Map                  // locale → []*catalog
	nchains   atomic.Int32
	// formatters: locale → *formatter (numbers and dates)
	formatters  sync.Map
	nformatters atomic.Int32
	fchains     sync.Map // locale → []*catalog, for format and relative keys
	nfchains    atomic.Int32
	log         *slog.Logger
	warn        bool     // log missing keys (development)
	missing     sync.Map // locale + key → struct{}: logged once
}

// Option configures [NewTranslator].
type Option func(*options)

type options struct {
	files []fs.FS
	log   *slog.Logger
	warn  bool
}

// WithLocales adds the catalogs in fsys: <locale>.yaml files at its root
// and the YAML files of <locale>/ folders (en.yaml, bn/app.yaml). Later
// catalogs can't redefine a key of earlier ones of the same locale.
func WithLocales(fsys fs.FS) Option { return func(o *options) { o.files = append(o.files, fsys) } }

// WithLogger sets the logger, which reports keys missing from every
// catalog once each when warnings are on ([WithWarnMissing]).
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.log = l } }

// WithWarnMissing logs keys missing from every catalog, once per locale and
// key. New turns it on in development.
func WithWarnMissing() Option { return func(o *options) { o.warn = true } }

// WarnMissing is [WithWarnMissing].
//
// Deprecated: Use WithWarnMissing; WarnMissing is removed in v0.6.
//
//go:fix inline
func WarnMissing() Option { return WithWarnMissing() }

// NewTranslator returns a translator for cfg with the catalogs of [WithLocales]
// and the framework's English catalog.
func NewTranslator(cfg Config, opts ...Option) (*Translator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}
	o := options{log: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(&o)
	}
	core := map[language.Tag]*catalog{}
	sub, _ := fs.Sub(coreFiles, "locales")
	if err := loadFS(sub, core); err != nil {
		return nil, fmt.Errorf("i18n: the core catalog: %w", err)
	}
	tr := &Translator{
		cfg:  cfg,
		cats: map[language.Tag]*catalog{},
		core: core[language.English],
		log:  o.log,
		warn: o.warn,
	}
	var errs []error
	for _, fsys := range o.files {
		if err := loadFS(fsys, tr.cats); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}
	tr.def, _ = parseLocale(cfg.Locale)
	tr.fallback, _ = parseLocale(cfg.Fallback)
	if len(cfg.Locales) > 0 {
		for _, l := range cfg.Locales {
			tag, _ := parseLocale(l)
			tr.addSupported(tag)
		}
		if !slices.Contains(tr.supported, tr.def) {
			return nil, fmt.Errorf("i18n: APP_LOCALE %s is not in APP_LOCALES (%s)", tr.def, strings.Join(tr.Supported(), ", "))
		}
	} else {
		tr.addSupported(tr.def)
		tags := make([]language.Tag, 0, len(tr.cats))
		for tag := range tr.cats {
			tags = append(tags, tag)
		}
		slices.SortFunc(tags, func(a, b language.Tag) int { return strings.Compare(a.String(), b.String()) })
		for _, tag := range tags {
			tr.addSupported(tag)
		}
	}
	tr.matcher = language.NewMatcher(tr.supported)
	return tr, nil
}

func (tr *Translator) addSupported(tag language.Tag) {
	if !slices.Contains(tr.supported, tag) {
		tr.supported = append(tr.supported, tag)
	}
}

type translatorKey struct{}

// New returns the app's translator, configured from APP_LOCALE,
// APP_FALLBACK_LOCALE, APP_LOCALES and APP_LOCALE_STRATEGY, with the catalogs in
// locales (the embedded files of the app's locales folder; nil for none).
// It adds the translator to every context the app creates, so [T] and
// the framework's messages use it, provides it as a service (the HTTP
// server resolves each request's locale with it), adds the locale:check
// command, and logs missing keys in development.
//
//	tr, err := i18n.New(app, locales.FS)
func New(app *anetos.App, locales fs.FS) (*Translator, error) {
	if _, err := anetos.Resolve[*Translator](app); err == nil {
		return nil, errors.New("i18n: New called twice for one app")
	}
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	if cfg.Strategy == StrategySubdomain && app.Config().URL == "" {
		return nil, errors.New("i18n: APP_LOCALE_STRATEGY=subdomain needs APP_URL, the default locale's address (https://example.com)")
	}
	opts := []Option{WithLogger(app.Logger().With("component", "i18n"))}
	if locales != nil {
		opts = append(opts, WithLocales(locales))
	}
	if app.Config().Env.IsDevelopment() {
		opts = append(opts, WithWarnMissing())
	}
	tr, err := NewTranslator(cfg, opts...)
	if err != nil {
		return nil, err
	}
	app.AddContextValue(translatorKey{}, tr)
	anetos.Provide(app, tr)
	if err := app.AddCommand(tr.checkCommand(locales)); err != nil {
		return nil, err
	}
	return tr, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp(app *anetos.App, locales fs.FS) (*Translator, error) {
	return New(app, locales)
}

// WithTranslator returns ctx with tr as its translator, for code outside
// an app (a package's tests).
func WithTranslator(ctx context.Context, tr *Translator) context.Context {
	return context.WithValue(ctx, translatorKey{}, tr)
}

var defaultTranslator = sync.OnceValue(func() *Translator {
	tr, err := NewTranslator(Config{Locale: "en", Fallback: "en", Strategy: StrategyNone})
	if err != nil {
		panic(err) // the embedded core catalog is broken
	}
	return tr
})

// From returns the translator in ctx, or one with only the framework's
// English messages when ctx has none, so validation and error pages
// speak English in apps without catalogs.
func From(ctx context.Context) *Translator {
	if tr, ok := ctx.Value(translatorKey{}).(*Translator); ok && tr != nil {
		return tr
	}
	return defaultTranslator()
}

// Config returns the translator's configuration.
func (tr *Translator) Config() Config { return tr.cfg }

// Default returns the default locale (APP_LOCALE), canonical: "en".
func (tr *Translator) Default() string { return tr.def.String() }

// Supported returns the supported locales, the default first.
func (tr *Translator) Supported() []string {
	out := make([]string, len(tr.supported))
	for i, t := range tr.supported {
		out[i] = t.String()
	}
	return out
}

// Match returns the supported locale that matches the first of locales
// the translator supports, and whether any did. A locale matches itself
// or its nearest supported parent (bn-BD matches bn, fr-CH matches fr),
// else a supported locale close enough to read (a regional variant of
// the same language and script: fr-CH matches fr-CA when fr isn't
// supported; zh doesn't match zh-Hant).
func (tr *Translator) Match(locales ...string) (string, bool) {
	for _, l := range locales {
		tag, err := language.Parse(l)
		if err != nil || tag == language.Und {
			continue
		}
		if s, ok := tr.match(tag); ok {
			return s, true
		}
	}
	return "", false
}

// match matches one tag: itself or a parent, then the closest supported
// locale with high confidence.
func (tr *Translator) match(tag language.Tag) (string, bool) {
	for t := tag; t != language.Und; t = t.Parent() {
		if i := slices.Index(tr.supported, t); i >= 0 {
			return tr.supported[i].String(), true
		}
	}
	if _, i, conf := tr.matcher.Match(tag); conf >= language.High && sameLanguage(tag, tr.supported[i]) {
		return tr.supported[i].String(), true
	}
	return "", false
}

// sameLanguage reports whether a and b are the same language in the same
// script (fr-CH and fr-CA; not zh-Hans and zh-Hant, nor hy and ru, which
// the matcher offers as a fallback).
func sameLanguage(a, b language.Tag) bool {
	ba, _ := a.Base()
	bb, _ := b.Base()
	sa, _ := a.Script()
	sb, _ := b.Script()
	return ba == bb && sa == sb
}

// MatchAcceptLanguage returns the supported locale that matches the
// first language of an Accept-Language header (in the visitor's order of
// preference) the translator supports, as [Translator.Match] does, and
// whether any did.
func (tr *Translator) MatchAcceptLanguage(header string) (string, bool) {
	type choice struct {
		tag language.Tag
		q   float32
	}
	var choices []choice
	for entry := range strings.SplitSeq(header, ",") { // one bad entry doesn't spoil the rest
		tags, qs, err := language.ParseAcceptLanguage(entry)
		if err != nil {
			continue
		}
		for i, t := range tags {
			choices = append(choices, choice{t, qs[i]})
		}
	}
	slices.SortStableFunc(choices, func(a, b choice) int { return cmp.Compare(b.q, a.q) })
	for _, c := range choices {
		if s, ok := tr.match(c.tag); ok {
			return s, true
		}
	}
	return "", false
}

// chain returns the catalogs a key is looked up in for locale: the
// locale's and its parents', the fallback's and its parents', and the
// core's (English) right after the app's English ones, so a non-English
// fallback never answers for English. Chains are kept by locale; the
// locales asked for are the supported ones, matched.
func (tr *Translator) chain(locale string) []*catalog {
	if c, ok := tr.chains.Load(locale); ok {
		return c.([]*catalog)
	}
	tag, err := language.Parse(locale)
	if err != nil {
		tag = tr.def
	}
	var out []*catalog
	add := func(t language.Tag) {
		for ; t != language.Und; t = t.Parent() {
			if c := tr.cats[t]; c != nil && !slices.Contains(out, c) {
				out = append(out, c)
			}
			if t == language.English && !slices.Contains(out, tr.core) {
				out = append(out, tr.core)
			}
		}
	}
	add(tag)
	add(tr.fallback)
	if !slices.Contains(out, tr.core) {
		out = append(out, tr.core)
	}
	if tr.nchains.Add(1) > maxChains { // locales not matched: don't keep
		return out
	}
	c, _ := tr.chains.LoadOrStore(locale, out)
	return c.([]*catalog)
}

// maxChains bounds the chains kept, against unmatched locale strings.
const maxChains = 256

// lookup returns the message of key for locale and the locale of the
// catalog it came from (whose plural rules apply), or nil. A key naming
// a plural message's form ("posts.count.one") gives that form, or its
// other form when the first catalog with the message lacks it.
func (tr *Translator) lookup(locale, key string) (*message, language.Tag) {
	for _, c := range tr.chain(locale) {
		if m := c.msgs[key]; m != nil {
			return m, c.tag
		}
		if i := strings.LastIndexByte(key, '.'); i > 0 {
			if f, ok := pluralForms[key[i+1:]]; ok {
				if m := c.msgs[key[:i]]; m != nil && m.plural != nil {
					text, ok := m.plural[f]
					if !ok {
						text = m.plural[plural.Other] // as Plural would
					}
					return &message{text: text}, c.tag
				}
			}
		}
	}
	return nil, language.Und
}

// missingKey logs a missing key once, if warnings are on.
func (tr *Translator) missingKey(ctx context.Context, locale, key string) {
	if !tr.warn {
		return
	}
	if _, seen := tr.missing.LoadOrStore(locale+"\x00"+key, struct{}{}); !seen {
		tr.log.WarnContext(ctx, "i18n: missing translation", "locale", locale, "key", key)
	}
}
