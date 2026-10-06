// SPDX-License-Identifier: Apache-2.0

package i18n_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/i18n"
)

var files = fstest.MapFS{
	"en.yaml": {Data: []byte(`
nav:
  home: "Home"
welcome: "Welcome, {name}"
posts:
  count:
    one: "{count} post"
    other: "{count} posts"
validation:
  required: "Please fill in {label}."
`)},
	"bn/app.yaml": {Data: []byte(`
nav:
  home: "হোম"
welcome: "স্বাগতম, {name}"
posts:
  count:
    one: "{count}টি পোস্ট"
    other: "{count}টি পোস্ট"
`)},
	"bn/extra.yml": {Data: []byte(`only_bn: "শুধু বাংলা"`)},
	"bn-BD.yaml":   {Data: []byte(`nav: {home: "নীড়"}`)},
	"ar.yaml": {Data: []byte(`
items:
  zero: "no items"
  one: "one item"
  two: "two items"
  few: "{count} items (few)"
  many: "{count} items (many)"
  other: "{count} items"
`)},
	"README.md":  {Data: []byte("not a catalog")},
	"locales.go": {Data: []byte("package locales")},
}

func newTranslator(t *testing.T, cfg i18n.Config, opts ...i18n.Option) *i18n.Translator {
	t.Helper()
	if cfg.Locale == "" {
		cfg.Locale = "en"
	}
	if cfg.Fallback == "" {
		cfg.Fallback = "en"
	}
	if cfg.URL == "" {
		cfg.URL = i18n.URLNone
	}
	tr, err := i18n.New(cfg, append([]i18n.Option{i18n.WithLocales(files)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestTranslate(t *testing.T) {
	tr := newTranslator(t, i18n.Config{})
	if got := strings.Join(tr.Supported(), ","); got != "en,ar,bn,bn-BD" {
		t.Errorf("Supported = %s", got)
	}
	ctx := i18n.WithTranslator(context.Background(), tr)
	bn := i18n.WithLocale(ctx, "bn")
	bd := i18n.WithLocale(ctx, "bn-BD")
	cases := []struct {
		ctx  context.Context
		got  string
		want string
	}{
		{ctx, i18n.T(ctx, "nav.home"), "Home"},
		{bn, i18n.T(bn, "nav.home"), "হোম"},
		{bd, i18n.T(bd, "nav.home"), "নীড়"},                       // the locale's own
		{bd, i18n.T(bd, "welcome", "name", "Ada"), "স্বাগতম, Ada"}, // its parent's
		{bn, i18n.T(bn, "only_bn"), "শুধু বাংলা"},
		{ctx, i18n.T(ctx, "only_bn"), "only_bn"},                                                                 // missing: the key
		{bn, i18n.T(bn, "validation.required", "label", "নাম"), "Please fill in নাম."},                           // the fallback's (an app override of a core key)
		{bn, i18n.T(bn, "validation.email", "label", "email"), "The email field must be a valid email address."}, // the core's
		{ctx, i18n.T(ctx, "welcome", "name", "Ada", "unused", 1), "Welcome, Ada"},
		{ctx, i18n.T(ctx, "welcome"), "Welcome, {name}"}, // no value: kept
		{ctx, i18n.T(ctx, "posts.count"), "{count} posts"},
		{ctx, i18n.Plural(ctx, "posts.count", 1), "1 post"},
		{ctx, i18n.Plural(ctx, "posts.count", 0), "0 posts"},
		{ctx, i18n.Plural(ctx, "posts.count", -1), "-1 post"},
		{bn, i18n.Plural(bn, "posts.count", 3), "৩টি পোস্ট"},                 // Bangla digits
		{ctx, i18n.Plural(ctx, "welcome", 2, "name", "Ada"), "Welcome, Ada"}, // a plain message
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("%d (%s): got %q, want %q", i, i18n.Locale(c.ctx), c.got, c.want)
		}
	}
	ar := i18n.WithLocale(ctx, "ar")
	for n, want := range map[int]string{0: "no items", 1: "one item", 2: "two items", 3: "٣ items (few)", 11: "١١ items (many)", 100: "١٠٠ items"} {
		if got := i18n.Plural(ar, "items", n); got != want {
			t.Errorf("ar %d: %q, want %q", n, got, want)
		}
	}
	if s, ok := i18n.Lookup(bn, "welcome"); !ok || s != "স্বাগতম, {name}" {
		t.Errorf("Lookup = %q, %v", s, ok)
	}
	if !i18n.Has(bn, "validation.min.string") || i18n.Has(bn, "nope") {
		t.Error("Has")
	}

	// Without a translator: the core's English.
	if got := i18n.T(context.Background(), "validation.accepted", "label", "terms"); got != "The terms field must be accepted." {
		t.Errorf("core = %q", got)
	}
}

func TestLocales(t *testing.T) {
	tr := newTranslator(t, i18n.Config{Locales: []string{"en", "bn"}})
	ctx := i18n.WithTranslator(context.Background(), tr)
	if got := strings.Join(tr.Supported(), ","); got != "en,bn" {
		t.Errorf("APP_LOCALES = %s", got)
	}
	for in, want := range map[string]string{"bn": "bn", "bn-BD": "bn", "BN": "bn", "en-GB": "en", "fr": "en", "": "en", "!!": "en"} {
		if got := i18n.Locale(i18n.WithLocale(ctx, in)); got != want {
			t.Errorf("WithLocale(%q) → %s, want %s", in, got, want)
		}
	}
	if l, ok := tr.Match("fr", "bn-IN"); !ok || l != "bn" {
		t.Errorf("Match = %s, %v", l, ok)
	}
	if _, ok := tr.Match("fr"); ok {
		t.Error("Match(fr) matched")
	}
	if l, ok := tr.MatchAcceptLanguage("fr-CH, fr;q=0.9, bn;q=0.8, en;q=0.5"); !ok || l != "bn" {
		t.Errorf("MatchAcceptLanguage = %s, %v", l, ok)
	}
	if _, ok := tr.MatchAcceptLanguage("de, fr"); ok {
		t.Error("MatchAcceptLanguage(de, fr) matched")
	}
	if i18n.Locale(context.Background()) != "en" || tr.Default() != "en" {
		t.Error("default locale")
	}

	bad := []i18n.Config{
		{Locale: "xx-!!", Fallback: "en", URL: "none"},
		{Locale: "en", Fallback: "en", URL: "path"},
		{Locale: "bn", Fallback: "en", URL: "none", Locales: []string{"en"}},
	}
	for _, cfg := range bad {
		if _, err := i18n.New(cfg); err == nil {
			t.Errorf("New(%+v) = nil error", cfg)
		}
	}
}

func TestCatalogErrors(t *testing.T) {
	cfg := i18n.Config{Locale: "en", Fallback: "en", URL: "none"}
	cases := map[string]fstest.MapFS{
		"is 3, not text":        {"en.yaml": {Data: []byte("count: 3")}},
		"also defined in":       {"en/a.yaml": {Data: []byte("x: a")}, "en/b.yaml": {Data: []byte("x: b")}},
		"is not a locale":       {"english!.yaml": {Data: []byte("x: a")}},
		"has no value":          {"en.yaml": {Data: []byte("x:")}},
		"item 2 is not text":    {"en.yaml": {Data: []byte("months: [Jan, 2]")}},
		"did not find expected": {"en.yaml": {Data: []byte("x: [unclosed")}},
		"the key 1.1":           {"en.yaml": {Data: []byte("a:\n  1.10: x")}},
		"has the key <nil>":     {"en.yaml": {Data: []byte("a:\n  ~: x")}},
	}
	for want, fsys := range cases {
		if _, err := i18n.New(cfg, i18n.WithLocales(fsys)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	// A map of plural categories without "other" is a namespace.
	tr, err := i18n.New(cfg, i18n.WithLocales(fstest.MapFS{"en.yaml": {Data: []byte("answer: {one: yes, two: no}\n404: gone")}}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := i18n.WithTranslator(context.Background(), tr)
	if i18n.T(ctx, "answer.one") != "yes" || i18n.T(ctx, "404") != "gone" {
		t.Error("namespace keys")
	}
}

func TestMissingLogged(t *testing.T) {
	var buf bytes.Buffer
	tr := newTranslator(t, i18n.Config{}, i18n.WarnMissing(), i18n.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
	ctx := i18n.WithTranslator(context.Background(), tr)
	i18n.T(ctx, "nope")
	i18n.T(ctx, "nope")
	i18n.Plural(i18n.WithLocale(ctx, "bn"), "nope", 2)
	if n := strings.Count(buf.String(), "missing translation"); n != 2 {
		t.Errorf("logged %d times:\n%s", n, buf.String())
	}
}

type user struct{ locale, mail, zone string }

func (u user) PreferredLocale() string     { return u.locale }
func (u user) CommunicationLocale() string { return u.mail }
func (u user) PreferredTimeZone() string   { return u.zone }

func TestPreferences(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_TIMEZONE": "UTC"}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := i18n.ForApp(app, files); err != nil {
		t.Fatal(err)
	}
	if _, err := i18n.ForApp(app, files); err == nil {
		t.Error("ForApp twice = nil")
	}
	var current any
	i18n.SetCurrentUser(app, func(context.Context) (any, bool) { return current, current != nil })
	ctx := app.Context(context.Background())

	if l, z := i18n.Preferences(ctx); l != "" || z != nil {
		t.Errorf("signed out: %q, %v", l, z)
	}
	current = user{locale: "bn-BD", zone: "Asia/Dhaka"}
	if l, z := i18n.Preferences(ctx); l != "bn-BD" || z == nil || z.String() != "Asia/Dhaka" {
		t.Errorf("signed in: %q, %v", l, z)
	}

	// ForUser: the communication locale, else the display one; the zone.
	u := i18n.ForUser(ctx, user{locale: "bn", mail: "en", zone: "Asia/Dhaka"})
	if i18n.Locale(u) != "en" || i18n.TimeZone(u).String() != "Asia/Dhaka" {
		t.Errorf("ForUser = %s, %v", i18n.Locale(u), i18n.TimeZone(u))
	}
	u = i18n.ForUser(ctx, user{locale: "bn"})
	if i18n.Locale(u) != "bn" || i18n.TimeZone(u).String() != "UTC" {
		t.Errorf("ForUser without mail locale or zone = %s, %v", i18n.Locale(u), i18n.TimeZone(u))
	}
	if u := i18n.ForUser(ctx, struct{}{}); i18n.Locale(u) != "en" {
		t.Error("ForUser of a user without preferences changed the locale")
	}
	if u := i18n.ForUser(ctx, user{locale: "fr", zone: "Mars/Olympus"}); i18n.Locale(u) != "en" || i18n.TimeZone(u).String() != "UTC" {
		t.Error("unknown preferences changed ctx")
	}
}

func TestResolver(t *testing.T) {
	tr := newTranslator(t, i18n.Config{})
	ctx := i18n.WithTranslator(context.Background(), tr)
	calls := 0
	dhaka, _ := time.LoadLocation("Asia/Dhaka")
	ctx = i18n.WithResolver(ctx, func(ctx context.Context) (string, *time.Location) {
		calls++
		_ = i18n.Locale(ctx) // asking during resolution doesn't deadlock: the default
		return "bn", dhaka
	})
	if i18n.Locale(ctx) != "bn" || i18n.T(ctx, "nav.home") != "হোম" || i18n.TimeZone(ctx) != dhaka || calls != 1 {
		t.Errorf("resolved %s, %v after %d calls", i18n.Locale(ctx), i18n.TimeZone(ctx), calls)
	}
	// WithLocale and WithTimeZone win.
	en := i18n.WithTimeZone(i18n.WithLocale(ctx, "en"), time.UTC)
	if i18n.Locale(en) != "en" || i18n.TimeZone(en) != time.UTC {
		t.Error("WithLocale/WithTimeZone after a resolver")
	}
}

func TestCheck(t *testing.T) {
	tr := newTranslator(t, i18n.Config{Locales: []string{"en", "bn", "ar"}})
	var out bytes.Buffer
	n := tr.Check(&out, []string{"nav.home", "nav.missing", "nav.missing"})
	report := out.String()
	for _, want := range []string{
		"bn: 1 key(s) missing: validation.required",
		"ar: 4 key(s) missing:",
		"1 key(s) used in the source but in no catalog: nav.missing",
		"bn: note:",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "ar: items lacks") {
		t.Errorf("ar has every form:\n%s", report)
	}
	if n == 0 {
		t.Error("no problems counted")
	}

	// Placeholders and plural forms.
	tr, err := i18n.New(i18n.Config{Locale: "en", Fallback: "en", URL: "none"}, i18n.WithLocales(fstest.MapFS{
		"en.yaml": {Data: []byte("hi: \"Hi {name}\"\nn: {zero: \"no x\", one: \"{count} x\", other: \"{count} xs\"}")},
		"ar.yaml": {Data: []byte("hi: \"Hi {nom}\"\nn: {one: \"x\", other: \"{count} xs\"}")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	tr.Check(&out, nil)
	if r := out.String(); !strings.Contains(r, "en: note: n has plural forms the language doesn't use: zero") {
		t.Errorf("unused forms:\n%s", r)
	}
	if r := out.String(); !strings.Contains(r, "ar: hi has placeholders {nom}; en has {name}") || !strings.Contains(r, "ar: n lacks the plural forms zero, two, few, many") || strings.Contains(r, "ar: n has placeholders") {
		t.Errorf("report:\n%s", r)
	}
}

func TestCheckCommand(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\nfunc f() { _ = i18n.T(ctx, \"nav.home\"); _ = i18n.Plural(c.Context(), \"gone.key\", 2) }\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page.templ"), []byte(`<p>{ i18n.T(ctx, "welcome", "name", n) }</p>`), 0o644); err != nil {
		t.Fatal(err)
	}
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_LOCALES": "en"}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := i18n.ForApp(app, files); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := app.ExecuteArgs(t.Context(), []string{"lang:check", dir}, &out, &errOut); code != 1 || !strings.Contains(out.String(), "gone.key") || strings.Contains(out.String(), "welcome") {
		t.Errorf("lang:check = %d\n%s%s", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := app.ExecuteArgs(t.Context(), []string{"lang:check", filepath.Join(dir, "none")}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "en OK") {
		t.Errorf("lang:check without source = %d\n%s%s", code, out.String(), errOut.String())
	}
}

func TestResolverConcurrent(t *testing.T) {
	var calls atomic.Int32
	ctx := i18n.WithResolver(context.Background(), func(ctx context.Context) (string, *time.Location) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return "bn", time.UTC
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if got := i18n.Locale(ctx); got != "bn" {
				t.Errorf("Locale: %q", got)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("resolved %d times", calls.Load())
	}
}

func TestResolverAsksItself(t *testing.T) {
	ctx := i18n.WithResolver(context.Background(), func(ctx context.Context) (string, *time.Location) {
		return "x" + i18n.Locale(ctx), nil // the default while resolving
	})
	if got := i18n.Locale(ctx); got != "xen" {
		t.Errorf("Locale: %q", got)
	}
}

func TestWithLocaleKeepsZone(t *testing.T) {
	dhaka, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		t.Skip(err)
	}
	ctx := i18n.WithResolver(context.Background(), func(context.Context) (string, *time.Location) { return "en", dhaka })
	ctx = i18n.WithLocale(ctx, "en")
	if got := i18n.TimeZone(ctx); got != dhaka {
		t.Errorf("TimeZone: %v", got)
	}
}

func TestLookupDetails(t *testing.T) {
	cfg := i18n.Config{Locale: "bn", Fallback: "bn", URL: "none", Locales: []string{"bn", "en", "ja"}}
	tr, err := i18n.New(cfg, i18n.WithLocales(fstest.MapFS{
		"bn.yaml":          {Data: []byte("validation:\n  required: \"{label} দিতে হবে।\"\nhi: \"নমস্কার\"\nfiles: {one: \"{count} ফাইল\", other: \"{count}টি ফাইল\"}")},
		"en.yaml":          {Data: []byte("posts: {one: \"{count} post\", other: \"{count} posts\"}\nreasons: {other: \"Other\"}")},
		"ja/nested/a.yaml": {Data: []byte("hi: \"こんにちは\"")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := i18n.WithTranslator(context.Background(), tr)
	en, ja := i18n.WithLocale(ctx, "en"), i18n.WithLocale(ctx, "ja")
	cases := []struct{ got, want string }{
		// A non-English fallback doesn't answer for English: the core does.
		{i18n.T(en, "validation.required", "label", "name"), "The name field is required."},
		{i18n.T(en, "hi"), "নমস্কার"}, // the fallback, for the app's own keys
		{i18n.T(ctx, "validation.required", "label", "নাম"), "নাম দিতে হবে।"},
		{i18n.T(en, "reasons.other"), "Other"}, // a plural's form by key
		{i18n.T(en, "posts.one"), "{count} post"},
		{i18n.T(en, "posts.zero"), "{count} posts"}, // a form the message lacks: other, as Plural
		{i18n.Plural(ja, "files", 1), "1 ফাইল"},     // the rules of the message's language (ja has only other)
		{i18n.T(ja, "hi"), "こんにちは"},                 // nested folders
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("%d: got %q, want %q", i, c.got, c.want)
		}
	}
}

func TestMatchQuality(t *testing.T) {
	tr, err := i18n.New(i18n.Config{Locale: "en", Fallback: "en", URL: "none", Locales: []string{"en", "fr", "fr-CA", "zh-Hant", "pt-BR"}})
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"fr-CH": "fr", "fr-BE": "fr", "fr-CA": "fr-CA", "zh-TW": "zh-Hant", "zh": "", "zh-CN": "", "pt-PT": "pt-BR", "en-GB": "en"} {
		got, _ := tr.Match(in)
		if got != want {
			t.Errorf("Match(%s) = %q, want %q", in, got, want)
		}
	}
	if l, _ := tr.MatchAcceptLanguage("zh-CN, fr-CH;q=0.8"); l != "fr" {
		t.Errorf("MatchAcceptLanguage = %q", l)
	}
}

func TestFill(t *testing.T) {
	tr, err := i18n.New(i18n.Config{Locale: "en", Fallback: "en", URL: "none"}, i18n.WithLocales(fstest.MapFS{
		"en.yaml": {Data: []byte("a: \"{x\"\nb: \"{{x}}\"\nc: \"{}\"\nd: \"{x} {x}\"")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := i18n.WithTranslator(context.Background(), tr)
	for key, want := range map[string]string{"a": "{x", "b": "{V}", "c": "{}", "d": "V V"} {
		if got := i18n.T(ctx, key, "x", "V"); got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
	if got := i18n.T(ctx, "d", "x", "{x}"); got != "{x} {x}" {
		t.Errorf("values aren't filled again: %q", got)
	}
}

func TestResolverPanics(t *testing.T) {
	ctx := i18n.WithResolver(context.Background(), func(context.Context) (string, *time.Location) { panic("boom") })
	func() {
		defer func() { _ = recover() }()
		i18n.Locale(ctx)
	}()
	done := make(chan string)
	go func() { done <- i18n.Locale(ctx) }()
	select {
	case got := <-done:
		if got != "en" {
			t.Errorf("after a panic: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Locale waits forever after the resolver panicked")
	}
}

func TestMatchOtherLanguages(t *testing.T) {
	tr, err := i18n.New(i18n.Config{Locale: "en", Fallback: "en", URL: "none", Locales: []string{"en", "ru", "es", "zh-Hans"}})
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"hy": "", "cy": "", "tlh": "", "und-US": "en", "eu": "", "es-419": "es", "zh": "zh-Hans", "zh-TW": ""} {
		if got, _ := tr.Match(in); got != want {
			t.Errorf("Match(%s) = %q, want %q", in, got, want)
		}
	}
	for header, want := range map[string]string{"hy, en;q=0.9": "en", "xx-!!, ru": "ru", "es, ru;q=abc": "es", "en;q=0.2, ru;q=0.8": "ru"} {
		if got, _ := tr.MatchAcceptLanguage(header); got != want {
			t.Errorf("MatchAcceptLanguage(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestRootKeys(t *testing.T) {
	cfg := i18n.Config{Locale: "en", Fallback: "en", URL: "none"}
	for _, data := range []string{"true: x", "~: x", "1.5: x", "- a\n- b"} {
		if _, err := i18n.New(cfg, i18n.WithLocales(fstest.MapFS{"en.yaml": {Data: []byte(data)}})); err == nil {
			t.Errorf("%q: no error", data)
		}
	}
	for _, data := range []string{"", "# only a comment", "404: gone"} {
		if _, err := i18n.New(cfg, i18n.WithLocales(fstest.MapFS{"en.yaml": {Data: []byte(data)}})); err != nil {
			t.Errorf("%q: %v", data, err)
		}
	}
}

func TestCheckFormat(t *testing.T) {
	tr, err := i18n.New(i18n.Config{Locale: "en", Fallback: "en", URL: "none"}, i18n.WithLocales(fstest.MapFS{
		"bn.yaml": {Data: []byte("format: {months: [a, b]}")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if n := tr.Check(&out, nil); n != 1 || !strings.Contains(out.String(), "bn: format.months needs 12 names, not 2") {
		t.Errorf("%d:\n%s", n, out.String())
	}
}

func TestCheckFrameworkPlaceholders(t *testing.T) {
	tr, err := i18n.New(i18n.Config{Locale: "en", Fallback: "en", URL: "none"}, i18n.WithLocales(fstest.MapFS{
		"fr.yaml": {Data: []byte("validation:\n  min:\n    numeric: \"{label} au moins {min}\"\n  requird: \"x\"\nhttp:\n  request_id: \"ID\"\nformat:\n  numbering: \"latn\"")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	n := tr.Check(&out, nil)
	r := out.String()
	for _, want := range []string{
		"fr: validation.min.numeric has placeholders {label}, {min}; the framework's English has {0}, {label}",
		"fr: http.request_id has placeholders none; the framework's English has {id}",
		"fr: note: 1 key(s) en and the framework don't have: validation.requird",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("report lacks %q:\n%s", want, r)
		}
	}
	if n != 2 {
		t.Errorf("%d problems:\n%s", n, r)
	}
}

func TestTimeZones(t *testing.T) {
	zones := i18n.TimeZones()
	if len(zones) < 300 || zones[0] != "UTC" || !slices.IsSorted(zones[1:]) || !slices.Contains(zones, "Asia/Dhaka") {
		t.Fatalf("%d zones: %v…", len(zones), zones[:3])
	}
	for _, z := range zones {
		if _, err := time.LoadLocation(z); err != nil {
			t.Errorf("%s: %v", z, err)
		}
	}
	zones[0] = "changed"
	if i18n.TimeZones()[0] != "UTC" {
		t.Error("TimeZones returns its own slice")
	}
}
