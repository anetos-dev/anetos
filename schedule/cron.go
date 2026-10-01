// SPDX-License-Identifier: Apache-2.0

package schedule

import (
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// spec is a parsed cron expression: a bit per allowed value of each
// field.
type spec struct {
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool // the field was "*" (or "*/n"): see match
}

type field struct {
	name     string
	min, max int
	names    []string // names for min, min+1, …
}

var fields = []field{
	{name: "minute", min: 0, max: 59},
	{name: "hour", min: 0, max: 23},
	{name: "day of month", min: 1, max: 31},
	{name: "month", min: 1, max: 12, names: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}},
	{name: "day of week", min: 0, max: 7, names: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}},
}

var macros = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

// parse parses a cron expression of five fields (minute, hour, day of
// month, month, day of week) or a macro.
func parse(expr string) (spec, error) {
	if m, ok := macros[strings.ToLower(strings.TrimSpace(expr))]; ok {
		expr = m
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return spec{}, fmt.Errorf("schedule: cron expression %q must have 5 fields (minute hour day-of-month month day-of-week), or be a macro such as @daily", expr)
	}
	var sets [5]uint64
	for i, p := range parts {
		set, err := parseField(p, fields[i])
		if err != nil {
			return spec{}, fmt.Errorf("schedule: cron expression %q: %w", expr, err)
		}
		sets[i] = set
	}
	if sets[4]&(1<<7) != 0 { // 7 is Sunday too
		sets[4] = sets[4]&^(1<<7) | 1
	}
	return spec{minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domStar: strings.HasPrefix(parts[2], "*"), dowStar: strings.HasPrefix(parts[4], "*")}, nil
}

// parseField parses one field: a list of "*", "n", "n-m", each with an
// optional "/step" ("n/step" means from n to the maximum).
func parseField(s string, f field) (uint64, error) {
	var set uint64
	for item := range strings.SplitSeq(s, ",") {
		rng, stepStr, hasStep := strings.Cut(item, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n < 1 || n > f.max-f.min+1 {
				return 0, fmt.Errorf("the %s step %q isn't a number from 1 to %d", f.name, stepStr, f.max-f.min+1)
			}
			step = n
		}
		lo, hi := f.min, f.max
		switch {
		case rng == "*":
			if f.name == "day of week" {
				hi = 6 // 7 is Sunday again
			}
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if lo, err = value(a, f); err != nil {
				return 0, err
			}
			if hi, err = value(b, f); err != nil {
				return 0, err
			}
			if lo > hi {
				return 0, fmt.Errorf("the %s range %q goes backwards", f.name, rng)
			}
		default:
			v, err := value(rng, f)
			if err != nil {
				return 0, err
			}
			lo, hi = v, v
			if hasStep {
				hi = f.max
			}
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

// value parses a number or a name of field f.
func value(s string, f field) (int, error) {
	for i, n := range f.names {
		if strings.EqualFold(s, n) {
			return f.min + i, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < f.min || v > f.max {
		return 0, fmt.Errorf("%q isn't a valid %s (%d to %d)", s, f.name, f.min, f.max)
	}
	return v, nil
}

// startOfDay returns the first minute of a date in loc (normalized, as
// time.Date does: day 32 is the next month). Where a clock change skips
// midnight, that is the first minute after the change: time.Date may
// resolve a missing midnight to the evening before.
func startOfDay(year int, month time.Month, day int, loc *time.Location) time.Time {
	d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC) // the normalized date
	t := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	for {
		y, m, dd := t.Date()
		if y > d.Year() || y == d.Year() && (m > d.Month() || m == d.Month() && dd >= d.Day()) {
			return t
		}
		t = t.Add(time.Minute)
	}
}

func has(set uint64, v int) bool { return set&(1<<v) != 0 }

// dayMatches reports whether t's day is allowed: as in cron, when both
// the day of month and the day of week are restricted, either may match.
func (s spec) dayMatches(t time.Time) bool {
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// horizon is how far next looks: the Gregorian calendar repeats every 400
// years, days of the week included, so a spec that doesn't match within
// it never does.
const horizon = 401

// next returns the first time after t (to the minute) that s allows, in
// t's location, or the zero time if there is none.
func (s spec) next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(horizon, 0, 0)
	for t.Before(limit) {
		switch {
		case !has(s.month, int(t.Month())):
			t = startOfDay(t.Year(), t.Month()+1, 1, t.Location())
		case !s.dayMatches(t):
			t = startOfDay(t.Year(), t.Month(), t.Day()+1, t.Location())
		case !has(s.hour, t.Hour()):
			// The next hour on the wall clock. Add, not time.Date: hours
			// repeated or skipped when clocks change are visited as they
			// come. (Truncate works on absolute time, which isn't on the
			// hour in zones with half-hour offsets.)
			t = t.Add(time.Duration(60-t.Minute()) * time.Minute)
		case !has(s.minute, t.Minute()):
			// The next allowed minute in this hour, or the next hour.
			rest := s.minute >> (t.Minute() + 1) << (t.Minute() + 1)
			if rest == 0 {
				t = t.Add(time.Duration(60-t.Minute()) * time.Minute)
			} else {
				t = t.Add(time.Duration(bits.TrailingZeros64(rest)-t.Minute()) * time.Minute)
			}
		default:
			return t
		}
	}
	return time.Time{}
}
