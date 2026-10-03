// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"context"
	"math"
	"time"

	"golang.org/x/text/feature/plural"

	"anetos.dev/anetos"
)

// Ago says how long ago t was, or how long until it, from the app's
// clock ([anetos.Now]), for ctx's locale: "just now" (under 45 seconds),
// "3 minutes ago", "in 2 days". The keys are relative.now,
// relative.past ("{time} ago") and relative.future ("in {time}"), with
// the amount from [Duration]. A zero t gives "".
func Ago(ctx context.Context, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	tr, locale := From(ctx), Locale(ctx)
	d := anetos.Now(ctx).Sub(t)
	key := "relative.past"
	if d < 0 {
		key = "relative.future"
	}
	d = abs(d)
	if d < 45*time.Second {
		return tr.relative(ctx, locale, "relative.now", -1)
	}
	return fill(tr.relative(ctx, locale, key, -1), []any{"time", duration(ctx, d, false)})
}

// Duration says d for people in ctx's locale, in its largest unit,
// rounded to the nearest: "40 seconds", "3 minutes", "5 hours", "2 days",
// "3 months", "1 year" (from 45 seconds a minute, from 45 minutes an
// hour, from 22 hours a day, from 26 days a month of 30 days, from 345
// days a year of 365). The units are plural messages,
// relative.units.<unit>. Use [DurationUp] for a wait.
func Duration(ctx context.Context, d time.Duration) string { return duration(ctx, d, false) }

// DurationUp is [Duration] rounded up, for how long to wait: 89 minutes
// is "2 hours" ("1 hour" with Duration), and 300ms "1 second".
func DurationUp(ctx context.Context, d time.Duration) string { return duration(ctx, d, true) }

func abs(d time.Duration) time.Duration {
	if d == math.MinInt64 {
		return math.MaxInt64
	}
	if d < 0 {
		return -d
	}
	return d
}

func duration(ctx context.Context, d time.Duration, up bool) string {
	d = abs(d)
	const day = 24 * time.Hour
	units := []struct {
		key  string
		size time.Duration
		from time.Duration // the smallest d shown in this unit
	}{
		{"year", 365 * day, 345 * day},
		{"month", 30 * day, 26 * day},
		{"day", day, 22 * time.Hour},
		{"hour", time.Hour, 45 * time.Minute},
		{"minute", time.Minute, 45 * time.Second},
		{"second", time.Second, 0},
	}
	for _, u := range units {
		if d < u.from {
			continue
		}
		n := d / u.size // no overflow: rounding below adds at most 1
		rest := d % u.size
		if (up && rest > 0) || (!up && rest >= u.size/2) {
			n++
		}
		if u.key != "second" {
			n = max(n, 1)
		}
		tr, locale := From(ctx), Locale(ctx)
		return fill(tr.relative(ctx, locale, "relative.units."+u.key, int(n)), []any{"count", tr.formatCount(locale, int(n))})
	}
	return ""
}

// relative returns the relative message key for locale, from the
// locale's catalogs or English (never another fallback language); n >= 0
// picks a plural form for n.
func (tr *Translator) relative(ctx context.Context, locale, key string, n int) string {
	m, from := tr.formatLookup(locale, key)
	if m == nil {
		tr.missingKey(ctx, locale, key)
		return key
	}
	if m.plural == nil {
		return m.text
	}
	if n >= 0 {
		if text, ok := m.plural[pluralForm(from, n)]; ok {
			return text
		}
	}
	return m.plural[plural.Other]
}
