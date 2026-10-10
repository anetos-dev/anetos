// SPDX-License-Identifier: Apache-2.0

package i18n_test

import (
	"context"
	"math"
	"testing"
	"testing/fstest"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/i18n"
)

var formats = fstest.MapFS{
	"bn.yaml": {Data: []byte(`
format:
  language: "বাংলা"
  months: [জানুয়ারী, ফেব্রুয়ারী, মার্চ, এপ্রিল, মে, জুন, জুলাই, আগস্ট, সেপ্টেম্বর, অক্টোবর, নভেম্বর, ডিসেম্বর]
  months_short: [জানু, ফেব, মার্চ, এপ্রি, মে, জুন, জুল, আগ, সেপ, অক্টো, নভে, ডিসে]
  days: [রবিবার, সোমবার, মঙ্গলবার, বুধবার, বৃহস্পতিবার, শুক্রবার, শনিবার]
  days_short: [রবি, সোম, মঙ্গল, বুধ, বৃহস্পতি, শুক্র, শনি]
  periods: [AM, PM]
  date: {short: "d/M/yy", medium: "d MMM, y", long: "d MMMM, y", full: "EEEE, d MMMM, y"}
  time: {short: "h:mm a", medium: "h:mm:ss a", long: "h:mm:ss a z", full: "h:mm:ss a zzzz"}
  datetime: {short: "{date}, {time}", medium: "{date}, {time}", long: "{date} এ {time}", full: "{date} এ {time}"}
  currency: "{amount}{symbol}"
relative:
  now: "এইমাত্র"
  past: "{time} আগে"
  future: "{time} পরে"
  units:
    minute: {one: "{count} মিনিট", other: "{count} মিনিট"}
    day: {one: "{count} দিন", other: "{count} দিন"}
`)},
	"bn-IN.yaml": {Data: []byte(`format: {numbering: "latn"}`)},
	"fr.yaml": {Data: []byte(`
format:
  months: [janvier, février, mars, avril, mai, juin, juillet, août, septembre, octobre, novembre, décembre]
  months_short: [janv., févr., mars, avr., mai, juin, juil., août, sept., oct., nov., déc.]
  days_short: [dim., lun., mar., mer., jeu., ven., sam.]
  date: {medium: "d MMM y"}
  time: {medium: "HH:mm:ss"}
  currency: "{amount} {symbol}"
`)},
	"ru.yaml": {Data: []byte(`
format:
  months: [января, февраля, марта, апреля, мая, июня, июля, августа, сентября, октября, ноября, декабря]
  months_standalone: [январь, февраль, март, апрель, май, июнь, июль, август, сентябрь, октябрь, ноябрь, декабрь]
`)},
	"ar.yaml": {Data: []byte(`x: "x"`)},
}

func formatCtx(t *testing.T) context.Context {
	t.Helper()
	tr, err := i18n.NewTranslator(i18n.Config{Locale: "en", Fallback: "en", URL: "none"}, i18n.WithLocales(formats))
	if err != nil {
		t.Fatal(err)
	}
	return i18n.WithTranslator(context.Background(), tr)
}

func TestNumbers(t *testing.T) {
	ctx := formatCtx(t)
	en, bn, fr, in := ctx, i18n.WithLocale(ctx, "bn"), i18n.WithLocale(ctx, "fr"), i18n.WithLocale(ctx, "bn-IN")
	cases := []struct{ got, want string }{
		{i18n.Number(en, 1234567.891), "1,234,567.891"},
		{i18n.Number(bn, 1234567), "১২,৩৪,৫৬৭"},
		{i18n.Number(fr, 1234.5), "1 234,5"},
		{i18n.Number(in, 1234567), "12,34,567"}, // format.numbering: latn
		{i18n.Number(en, -42), "-42"},
		{i18n.Fixed(en, 2.5, 2), "2.50"},
		{i18n.Percent(en, 0.256), "26%"},
		{i18n.Percent(fr, 0.5), "50 %"},
		{i18n.Currency(en, 1234.5, "USD"), "$1,234.50"},
		{i18n.Currency(en, -5, "USD"), "-$5.00"},
		{i18n.Currency(en, 1234.5, "JPY"), "¥1,234"}, // no decimals; half to even
		{i18n.Currency(fr, 1234.5, "EUR"), "1 234,50 €"},
		{i18n.Currency(bn, 1234.5, "BDT"), "১,২৩৪.৫০৳"},
		{i18n.Currency(bn, 12.5, "USD"), "১২.৫০ US$"}, // a space before letters
		{i18n.Currency(en, 1250, "BDT"), "BDT 1,250.00"},
		{i18n.Currency(en, 3, "XXZ"), "3\u00a0XXZ"},
		{i18n.Currency(en, math.NaN(), "USD"), "NaN\u00a0USD"},
		{i18n.Currency(en, uint64(math.MaxUint64), "USD"), "$18,446,744,073,709,551,615.00"}, // exact
		{i18n.Currency(en, int64(math.MinInt64), "USD"), "-$9,223,372,036,854,775,808.00"},
		{i18n.Currency(en, -0.001, "USD"), "$0.00"}, // no minus for zero
		{i18n.Number(en, -0.0001), "0"},
		{i18n.Fixed(en, -0.001, 2), "0.00"},
		{i18n.Fixed(en, 1234.5, -1), "1,234"},  // places clamped to 0
		{i18n.LocalNumber(en, "2030"), "2030"}, // not grouped
		{i18n.LocalNumber(en, "9007199254740993"), "9007199254740993"},
		{i18n.LocalNumber(fr, "2.5"), "2,5"},
		{i18n.LocalNumber(bn, "99999999999999999999"), "৯৯৯৯৯৯৯৯৯৯৯৯৯৯৯৯৯৯৯৯"},
		{i18n.LocalNumber(bn, "12"), "১২"},
		{i18n.LocalNumber(bn, "2.50"), "২.৫০"},
		{i18n.LocalNumber(bn, "2026-01-01"), "2026-01-01"},
		{i18n.LocalNumber(bn, "1e3"), "1e3"},
		{i18n.Plural(en, "relative.units.day", 1234), "1,234 days"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("%d: got %q, want %q", i, c.got, c.want)
		}
	}
}

func TestDates(t *testing.T) {
	ctx := formatCtx(t)
	dhaka, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		t.Skip(err)
	}
	moment := time.Date(2026, time.January, 15, 20, 4, 5, 0, time.UTC) // Friday 02:04 in Dhaka
	day := anetos.NewDate(2026, time.March, 1)
	en := ctx
	inDhaka := i18n.WithTimeZone(ctx, dhaka)
	bn := i18n.WithTimeZone(i18n.WithLocale(ctx, "bn"), dhaka)
	fr, ru := i18n.WithLocale(ctx, "fr"), i18n.WithLocale(ctx, "ru")
	cases := []struct{ got, want string }{
		{i18n.Date(en, moment), "Jan 15, 2026"},
		{i18n.Date(inDhaka, moment), "Jan 16, 2026"}, // the user's day
		{i18n.Date(en, moment, i18n.Short), "1/15/26"},
		{i18n.Date(en, moment, i18n.Full), "Thursday, January 15, 2026"},
		{i18n.Date(inDhaka, day, i18n.Long), "March 1, 2026"}, // a date has no zone
		{i18n.Time(en, moment), "8:04:05 PM"},
		{i18n.Time(inDhaka, moment, i18n.Long), "2:04:05 AM +06"},
		{i18n.Time(inDhaka, moment, i18n.Full), "2:04:05 AM Asia/Dhaka"},
		{i18n.DateTime(en, moment, i18n.Short), "1/15/26, 8:04 PM"},
		{i18n.DateTime(en, moment, i18n.Long), "January 15, 2026 at 8:04:05 PM UTC"},
		{i18n.Date(bn, moment), "১৬ জানু, ২০২৬"},
		{i18n.Date(bn, moment, i18n.Full), "শুক্রবার, ১৬ জানুয়ারী, ২০২৬"},
		{i18n.DateTime(bn, moment, i18n.Short), "১৬/১/২৬, ২:০৪ AM"},
		{i18n.Date(fr, moment), "15 janv. 2026"},
		{i18n.Time(fr, moment), "20:04:05"},
		{i18n.Format(fr, moment, "EEE d MMMM"), "jeu. 15 janvier"},
		{i18n.Format(en, moment, "yyyy-MM-dd 'at' HH:mm, h a ''x''"), "2026-01-15 at 20:04, 8 PM 'x'"},
		{i18n.Format(en, moment, "h 'o''clock'"), "8 o'clock"},
		{i18n.Format(en, moment, "MMMMM EEEEE"), "J T"}, // narrow
		{i18n.Format(en, &moment, "d"), "15"},
		{i18n.Format(en, (*time.Time)(nil), "d"), ""},
		{i18n.Date(en, (*anetos.Date)(nil)), ""},
		{i18n.Format(i18n.WithTimeZone(ctx, time.FixedZone("", 5*3600+1800)), moment, "HH:mm zzzz"), "01:34 +0530"},
		{i18n.Format(ru, day, "d MMMM"), "1 марта"},
		{i18n.Format(ru, day, "LLLL y"), "март 2026"},
		{i18n.Format(en, day, "LLLL y"), "March 2026"},
		{i18n.Date(en, time.Time{}), ""},
		{i18n.Date(en, anetos.Date{}), ""},
		{i18n.DateTime(en, time.Time{}), ""},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("%d: got %q, want %q", i, c.got, c.want)
		}
	}
}

func TestRelative(t *testing.T) {
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	ctx := anetos.WithClock(formatCtx(t), func() time.Time { return now })
	bn := i18n.WithLocale(ctx, "bn")
	cases := []struct{ got, want string }{
		{i18n.Ago(ctx, now.Add(-10*time.Second)), "just now"},
		{i18n.Ago(ctx, now.Add(-50*time.Second)), "1 minute ago"},
		{i18n.Ago(ctx, now.Add(-3*time.Minute)), "3 minutes ago"},
		{i18n.Ago(ctx, now.Add(-44*time.Minute)), "44 minutes ago"},
		{i18n.Ago(ctx, now.Add(-50*time.Minute)), "1 hour ago"},
		{i18n.Ago(ctx, now.Add(-30*time.Hour)), "1 day ago"},
		{i18n.Ago(ctx, now.Add(2*24*time.Hour)), "in 2 days"},
		{i18n.Ago(ctx, now.Add(-90*24*time.Hour)), "3 months ago"},
		{i18n.Ago(ctx, now.Add(-400*24*time.Hour)), "1 year ago"},
		{i18n.Duration(ctx, 40*time.Second), "40 seconds"},
		{i18n.Duration(ctx, 0), "0 seconds"},
		{i18n.Ago(bn, now.Add(-3*time.Minute)), "৩ মিনিট আগে"},
		{i18n.Ago(bn, now.Add(2*24*time.Hour)), "২ দিন পরে"},
		{i18n.Ago(bn, now), "এইমাত্র"},
		{i18n.Ago(ctx, time.Time{}), ""},
		{i18n.Ago(ctx, now.AddDate(-300, 0, 0)), "292 years ago"}, // beyond time.Duration: Sub saturates
		{i18n.Duration(ctx, math.MinInt64), "292 years"},
		{i18n.Duration(ctx, 89*time.Minute), "1 hour"},
		{i18n.DurationUp(ctx, 89*time.Minute), "2 hours"},
		{i18n.DurationUp(ctx, 300*time.Millisecond), "1 second"},
		{i18n.Duration(ctx, 300*time.Millisecond), "0 seconds"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("%d: got %q, want %q", i, c.got, c.want)
		}
	}
}

func TestLanguages(t *testing.T) {
	ctx := formatCtx(t)
	for locale, want := range map[string]string{"bn": "বাংলা", "bn-IN": "বাংলা", "en": "English", "en-GB": "English", "fr": "fr", "!!": "!!"} {
		if got := i18n.LanguageName(ctx, locale); got != want {
			t.Errorf("LanguageName(%s) = %q, want %q", locale, got, want)
		}
	}
	for locale, want := range map[string]string{"ar": "rtl", "he": "rtl", "fa": "rtl", "ur": "rtl", "en": "ltr", "bn": "ltr", "az-Arab": "rtl", "!!": "ltr"} {
		if got := i18n.DirOf(locale); got != want {
			t.Errorf("DirOf(%s) = %q, want %q", locale, got, want)
		}
	}
	if i18n.Dir(i18n.WithLocale(ctx, "ar")) != "rtl" || i18n.Dir(ctx) != "ltr" {
		t.Error("Dir")
	}
}

// TestFormatsNotFromFallback: an app in Bangla (the fallback) shows a
// German visitor English formats, not Bangla ones.
func TestFormatsNotFromFallback(t *testing.T) {
	tr, err := i18n.NewTranslator(i18n.Config{Locale: "bn", Fallback: "bn", URL: "none", Locales: []string{"bn", "de"}}, i18n.WithLocales(formats))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	ctx := anetos.WithClock(i18n.WithTranslator(context.Background(), tr), func() time.Time { return now })
	de := i18n.WithLocale(ctx, "de")
	for got, want := range map[string]string{
		i18n.Date(de, now, i18n.Full):          "Thursday, January 15, 2026",
		i18n.Currency(de, 1234.5, "EUR"):       "€1.234,50",
		i18n.Ago(de, now.Add(-3*24*time.Hour)): "3 days ago",
		i18n.Ago(ctx, now.Add(-3*time.Minute)): "৩ মিনিট আগে",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
