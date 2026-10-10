// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"anetos.dev/anetos"
)

// Style is the length of a formatted date or time: [Medium] (the
// default), [Short], [Long] or [Full], each locale's patterns
// format.date.<style> and format.time.<style>.
type Style int

// Styles. As English date times: Short 1/15/26, 3:04 PM; Medium Jan 15,
// 2026, 3:04:05 PM; Long January 15, 2026 at 3:04:05 PM +06 (the zone's
// abbreviation); Full Thursday, January 15, 2026 at 3:04:05 PM
// Asia/Dhaka (the zone's IANA name).
const (
	Medium Style = iota
	Short
	Long
	Full
)

var styleNames = [...]string{Medium: "medium", Short: "short", Long: "long", Full: "full"}

func styleOf(styles []Style) string {
	if len(styles) > 0 && styles[0] >= Medium && styles[0] <= Full {
		return styleNames[styles[0]]
	}
	return styleNames[Medium]
}

// Moment is the types [Date] and [Format] show: a time (shown in ctx's
// time zone, [TimeZone]) or an [anetos.Date] (a day, shown as it is),
// or a pointer to one (nil shows as "", for nullable columns).
type Moment interface {
	time.Time | *time.Time | anetos.Date | *anetos.Date
}

// Date formats the day of v for ctx's locale, in the pattern
// format.date.<style> (Medium unless given):
//
//	i18n.Date(ctx, order.PlacedAt)           // en: Jan 15, 2026; bn: ১৫ জানু, ২০২৬
//	i18n.Date(ctx, user.Birthday, i18n.Long) // en: January 15, 2026
//
// A time is shown in ctx's time zone (the logged-in user's, or
// APP_TIMEZONE), so the day is theirs. A zero value gives "".
func Date[M Moment](ctx context.Context, v M, style ...Style) string {
	return pattern(ctx, any(v), "format.date."+styleOf(style))
}

// Time formats the time of day of t in ctx's time zone and locale, in
// the pattern format.time.<style> (Medium unless given): 3:04:05 PM in
// English, 15:04:05 in French.
func Time(ctx context.Context, t time.Time, style ...Style) string {
	return pattern(ctx, t, "format.time."+styleOf(style))
}

// DateTime formats t's date and time in ctx's time zone and locale: the
// date and time patterns of the style, joined by format.datetime.<style>
// ("{date}, {time}" in English).
func DateTime(ctx context.Context, t time.Time, style ...Style) string {
	if t.IsZero() {
		return ""
	}
	tr, locale := From(ctx), Locale(ctx)
	s := styleOf(style)
	join := tr.formatText(locale, "format.datetime."+s, "{date}, {time}")
	return fill(join, []any{"date", pattern(ctx, t, "format.date."+s), "time", pattern(ctx, t, "format.time."+s)})
}

// Format formats v with a CLDR date pattern, in ctx's time zone and
// locale's names and digits:
//
//	i18n.Format(ctx, t, "EEE d MMM") // en: Thu 15 Jan; fr: jeu. 15 janv.
//
// The fields: y (year; yy two digits), M (month: M 1, MM 01, MMM Jan,
// MMMM January, MMMMM J), L (the same, standalone forms), d and dd
// (day), E to EEE (Thu), EEEE (Thursday) and EEEEE (T), a (AM/PM), h and
// hh (1–12), H and HH (0–23), m and mm, s and ss, z (zone abbreviation:
// UTC, +06) and zzzz (the zone's IANA name). Text in single quotes is
// literal ('at'), and two single quotes stand for one, inside quotes or
// out. Other letters (Y, G, Q, w…) are kept as they are.
func Format[M Moment](ctx context.Context, v M, layout string) string {
	t, ok := momentTime(ctx, any(v))
	if !ok {
		return ""
	}
	return formatPattern(From(ctx), Locale(ctx), t, layout)
}

// pattern formats v with the catalog pattern key.
func pattern(ctx context.Context, v any, key string) string {
	t, ok := momentTime(ctx, v)
	if !ok {
		return ""
	}
	tr, locale := From(ctx), Locale(ctx)
	return formatPattern(tr, locale, t, tr.formatText(locale, key, ""))
}

// momentTime returns v as a time in ctx's zone (a date: its midnight in
// UTC, shown as it is), and false for a zero value.
func momentTime(ctx context.Context, v any) (time.Time, bool) {
	switch v := v.(type) {
	case *time.Time:
		if v == nil {
			return time.Time{}, false
		}
		return momentTime(ctx, *v)
	case *anetos.Date:
		if v == nil {
			return time.Time{}, false
		}
		return momentTime(ctx, *v)
	case time.Time:
		if v.IsZero() {
			return time.Time{}, false
		}
		return v.In(TimeZone(ctx)), true
	case anetos.Date:
		if v.IsZero() {
			return time.Time{}, false
		}
		return v.In(time.UTC), true
	}
	return time.Time{}, false
}

// names returns the list at key for locale (months, days, periods), or
// nil when no catalog has it with n items.
func (tr *Translator) names(locale, key string, n int) []string {
	m, _ := tr.formatLookup(locale, key)
	if m == nil || len(m.list) != n {
		return nil
	}
	return m.list
}

// formatPattern formats t with a CLDR pattern for locale.
func formatPattern(tr *Translator, locale string, t time.Time, layout string) string {
	f := tr.formatter(locale)
	var b strings.Builder
	num := func(n, width int) {
		s := strconv.Itoa(n)
		for len(s) < width {
			s = "0" + s
		}
		b.WriteString(f.localDigits(s))
	}
	nameOf := func(key string, n, i int, fallback string) string {
		if list := tr.names(locale, key, n); list != nil {
			return list[i]
		}
		return fallback
	}
	name := func(key string, n, i int, fallback string) { b.WriteString(nameOf(key, n, i, fallback)) }
	narrow := func(s string) { // the first letter: J for January
		r, _ := utf8.DecodeRuneInString(s)
		b.WriteRune(r)
	}
	for i := 0; i < len(layout); {
		c := layout[i]
		if c == '\'' {
			if i+1 < len(layout) && layout[i+1] == '\'' { // ''
				b.WriteByte('\'')
				i += 2
				continue
			}
			// quoted text, in which '' is a quote
			i++
			for i < len(layout) {
				if layout[i] == '\'' {
					if i+1 < len(layout) && layout[i+1] == '\'' {
						b.WriteByte('\'')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(layout[i])
				i++
			}
			continue
		}
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			b.WriteByte(c)
			i++
			continue
		}
		n := 1
		for i+n < len(layout) && layout[i+n] == c {
			n++
		}
		i += n
		month := int(t.Month()) - 1
		day := int(t.Weekday())
		switch c {
		case 'y':
			if n == 2 {
				num(t.Year()%100, 2)
			} else {
				num(t.Year(), n)
			}
		case 'M', 'L':
			full := "format.months"
			if c == 'L' && tr.names(locale, "format.months_standalone", 12) != nil {
				full = "format.months_standalone"
			}
			switch {
			case n >= 5:
				narrow(nameOf(full, 12, month, t.Month().String()))
			case n == 4:
				name(full, 12, month, t.Month().String())
			case n == 3:
				name("format.months_short", 12, month, t.Month().String()[:3])
			default:
				num(month+1, n)
			}
		case 'd':
			num(t.Day(), n)
		case 'E':
			switch {
			case n >= 5:
				narrow(nameOf("format.days", 7, day, t.Weekday().String()))
			case n == 4:
				name("format.days", 7, day, t.Weekday().String())
			default:
				name("format.days_short", 7, day, t.Weekday().String()[:3])
			}
		case 'a':
			p := 0
			if t.Hour() >= 12 {
				p = 1
			}
			name("format.periods", 2, p, [2]string{"AM", "PM"}[p])
		case 'h':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			num(h, n)
		case 'H':
			num(t.Hour(), n)
		case 'm':
			num(t.Minute(), n)
		case 's':
			num(t.Second(), n)
		case 'z':
			if zone := t.Location().String(); n >= 4 && zone != "" && zone != "Local" {
				b.WriteString(zone)
			} else {
				b.WriteString(t.Format("MST"))
			}
		default:
			b.WriteString(strings.Repeat(string(c), n))
		}
	}
	return b.String()
}
