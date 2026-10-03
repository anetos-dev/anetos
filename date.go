// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"database/sql/driver"
	"fmt"
	"time"
)

// Date is a calendar date: a year, month and day, without a time of day
// or a time zone. Use it for birthdays, due dates and DATE columns: a
// date kept in a time.Time is a midnight somewhere, which is another
// day in UTC (midnight in Dhaka is 18:00 the day before), and the
// database stores times in UTC.
//
// A Date is a column of a model (written as "2006-01-02" text, read from
// text or from the time the driver returns), binds from form and query
// values ("2026-10-03", what <input type="date"> sends), and is a JSON
// string. The zero Date is no date: it is written as NULL, shown as "",
// and "" reads as it. The date rules of package validate (after, before,
// …) compare Dates.
type Date struct {
	Year  int        // the year, 2026
	Month time.Month // the month, January = 1
	Day   int        // the day of the month, from 1
}

// DateLayout is the layout of a [Date] as text: ISO 8601, "2006-01-02".
const DateLayout = time.DateOnly

// NewDate returns the date year-month-day, normalized as time.Date
// normalizes (October 32 is November 1).
func NewDate(year int, month time.Month, day int) Date {
	return DateOf(time.Date(year, month, day, 0, 0, 0, 0, time.UTC))
}

// DateOf returns the date of t in t's own zone. Convert t first to see
// it elsewhere: DateOf(t.In(loc)).
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// Today returns today's date in the app's zone (APP_TIMEZONE), on the
// app's clock ([Now]).
func Today(ctx context.Context) Date { return DateOf(Now(ctx)) }

// ParseDate parses "2006-01-02". An empty string is the zero Date.
func ParseDate(s string) (Date, error) {
	if s == "" {
		return Date{}, nil
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("anetos: %q is not a date (YYYY-MM-DD)", s)
	}
	return DateOf(t), nil
}

// IsZero reports whether d is the zero Date (no date).
func (d Date) IsZero() bool { return d == Date{} }

// IsValid reports whether d is a day of the calendar (not February 30)
// in the years 1 to 9999, which every database stores.
func (d Date) IsValid() bool {
	return d.Year >= 1 && d.Year <= 9999 && NewDate(d.Year, d.Month, d.Day) == d
}

// normalized returns d as NewDate would give it, so that out-of-range
// fields (October 32) compare as the day they stand for.
func (d Date) normalized() Date {
	if d.IsZero() {
		return d
	}
	return NewDate(d.Year, d.Month, d.Day)
}

// String returns d as "2006-01-02", or "" for the zero Date.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return d.In(time.UTC).Format(DateLayout)
}

// In returns the start of d (midnight) in loc.
func (d Date) In(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// AddDays returns d plus n days (minus, for a negative n).
func (d Date) AddDays(n int) Date { return NewDate(d.Year, d.Month, d.Day+n) }

// AddDate returns d plus the given years, months and days, normalized
// as time.Time.AddDate normalizes (January 31 plus a month is March 3,
// or March 2 in a leap year).
func (d Date) AddDate(years, months, days int) Date {
	return NewDate(d.Year+years, d.Month+time.Month(months), d.Day+days)
}

// DaysSince returns the number of days from e to d: positive when d is
// later.
func (d Date) DaysSince(e Date) int {
	return int((d.In(time.UTC).Unix() - e.In(time.UTC).Unix()) / 86400)
}

// Weekday returns d's day of the week.
func (d Date) Weekday() time.Weekday { return d.In(time.UTC).Weekday() }

// Compare returns -1 if d is before e, +1 if after, and 0 if they are
// the same day. Fields out of range count as the day they stand for
// (October 32 is November 1).
func (d Date) Compare(e Date) int {
	d, e = d.normalized(), e.normalized()
	switch {
	case d.Year != e.Year:
		return cmpInt(d.Year, e.Year)
	case d.Month != e.Month:
		return cmpInt(int(d.Month), int(e.Month))
	}
	return cmpInt(d.Day, e.Day)
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// Before reports whether d is before e.
func (d Date) Before(e Date) bool { return d.Compare(e) < 0 }

// After reports whether d is after e.
func (d Date) After(e Date) bool { return d.Compare(e) > 0 }

// MarshalText returns d as "2006-01-02", or nothing for the zero Date.
// A date that isn't valid ([Date.IsValid]) is an error.
func (d Date) MarshalText() ([]byte, error) {
	if err := d.check(); err != nil {
		return nil, err
	}
	return []byte(d.String()), nil
}

// check returns an error for a non-zero date that isn't valid, so it
// isn't written as another day.
func (d Date) check() error {
	if d.IsZero() || d.IsValid() {
		return nil
	}
	return fmt.Errorf("anetos: %04d-%02d-%02d is not a date", d.Year, int(d.Month), d.Day)
}

// UnmarshalText parses "2006-01-02"; empty text is the zero Date.
func (d *Date) UnmarshalText(b []byte) error {
	v, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// Value writes d as "2006-01-02" text, or NULL for the zero Date. A date
// that isn't valid ([Date.IsValid]) is an error.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	if err := d.check(); err != nil {
		return nil, err
	}
	return d.String(), nil
}

// Scan reads a date from a time (its day in its own zone: drivers
// return DATE columns as midnight UTC) or from text "2006-01-02",
// possibly followed by a time (SQLite); NULL, and the zero time (MySQL's
// 0000-00-00), are the zero Date.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
		return nil
	case time.Time:
		if v.IsZero() {
			*d = Date{}
			return nil
		}
		*d = DateOf(v)
		return nil
	case string:
		return d.scanText(v)
	case []byte:
		return d.scanText(string(v))
	}
	return fmt.Errorf("anetos: can't scan a %T into a Date", src)
}

func (d *Date) scanText(s string) error {
	if n := len(DateLayout); len(s) > n && (s[n] == ' ' || s[n] == 'T') {
		s = s[:n] // "2026-03-15 00:00:00"
	}
	v, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}
