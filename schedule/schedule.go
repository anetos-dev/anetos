// SPDX-License-Identifier: Apache-2.0

package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule says when a task runs: a cron expression, to the minute, in a
// time zone (the scheduler's, SCHEDULE_TIMEZONE, unless [Schedule.In]
// sets one). Build one with [Cron] or the helpers ([Daily], [Every], …).
type Schedule struct {
	expr string
	spec spec
	loc  *time.Location // nil: the scheduler's
	err  error
}

// Cron returns the schedule of a cron expression: five fields (minute,
// hour, day of month, month, day of week) of numbers, "*", ranges
// ("1-5"), lists ("1,15"), steps ("*/10", "0-30/5") and names ("MON",
// "JAN"); or a macro: @yearly, @monthly, @weekly, @daily, @hourly. When
// both the day of month and the day of week are restricted, either
// matches, as in cron; a field starting with "*" ("*/2" too) isn't
// restricted, so "0 0 */2 * MON" is odd days that are Mondays.
//
//	schedule.Cron("*/15 9-17 * * MON-FRI") // every 15 minutes, 9:00 to 17:45 on weekdays
func Cron(expr string) Schedule {
	s, err := parse(expr)
	return Schedule{expr: strings.Join(strings.Fields(expr), " "), spec: s, err: err}
}

// EveryMinute runs every minute.
func EveryMinute() Schedule { return Cron("* * * * *") }

// Every runs every d, counted from midnight: d is a whole number of
// minutes dividing an hour (1m, 5m, 15m, 30m…), or of hours dividing a
// day (1h, 2h, 6h, 12h).
//
//	schedule.Every(15 * time.Minute) // at :00, :15, :30, :45
func Every(d time.Duration) Schedule {
	switch {
	case d > 0 && d < time.Hour && d%time.Minute == 0 && time.Hour%d == 0:
		return Cron(fmt.Sprintf("*/%d * * * *", d/time.Minute))
	case d >= time.Hour && d <= 24*time.Hour && d%time.Hour == 0 && (24*time.Hour)%d == 0:
		return Cron(fmt.Sprintf("0 */%d * * *", d/time.Hour))
	}
	return Schedule{expr: "every " + d.String(), err: fmt.Errorf("schedule: Every(%s): use whole minutes dividing an hour, or whole hours dividing a day (or Cron)", d)}
}

// Hourly runs at the start of every hour.
func Hourly() Schedule { return Cron("0 * * * *") }

// HourlyAt runs every hour at minute.
func HourlyAt(minute int) Schedule { return Cron(fmt.Sprintf("%d * * * *", minute)) }

// Daily runs at midnight.
func Daily() Schedule { return Cron("0 0 * * *") }

// DailyAt runs every day at hhmm, "15:04" (24-hour clock).
func DailyAt(hhmm string) Schedule {
	h, m, err := timeOfDay(hhmm)
	if err != nil {
		return Schedule{expr: "daily at " + hhmm, err: err}
	}
	return Cron(fmt.Sprintf("%d %d * * *", m, h))
}

// WeeklyOn runs every week on day at hhmm.
func WeeklyOn(day time.Weekday, hhmm string) Schedule {
	h, m, err := timeOfDay(hhmm)
	if err == nil && (day < time.Sunday || day > time.Saturday) {
		err = fmt.Errorf("schedule: WeeklyOn(%d, %q): no such day", day, hhmm)
	}
	if err != nil {
		return Schedule{expr: "weekly at " + hhmm, err: err}
	}
	return Cron(fmt.Sprintf("%d %d * * %d", m, h, day))
}

// MonthlyOn runs every month on day (1 to 28, so every month has it) at
// hhmm. For the last day of the month, use Cron with a check in the task.
func MonthlyOn(day int, hhmm string) Schedule {
	h, m, err := timeOfDay(hhmm)
	if err == nil && (day < 1 || day > 28) {
		err = fmt.Errorf("schedule: MonthlyOn(%d, %q): the day must be from 1 to 28", day, hhmm)
	}
	if err != nil {
		return Schedule{expr: "monthly at " + hhmm, err: err}
	}
	return Cron(fmt.Sprintf("%d %d %d * *", m, h, day))
}

// timeOfDay parses "15:04".
func timeOfDay(hhmm string) (h, m int, err error) {
	hs, ms, ok := strings.Cut(hhmm, ":")
	if ok {
		h, err = strconv.Atoi(hs)
		if err == nil {
			m, err = strconv.Atoi(ms)
		}
	}
	if !ok || err != nil || len(ms) != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("schedule: %q isn't a time of day like \"02:30\"", hhmm)
	}
	return h, m, nil
}

// In sets the schedule's time zone, an IANA name such as "Asia/Dhaka".
// When clocks change, a time the change skips doesn't run that day, and
// one it repeats runs twice: schedule important tasks away from the
// zone's clock changes (usually between midnight and 03:00), or in UTC.
func (s Schedule) In(tz string) Schedule {
	if s.err != nil {
		return s
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		s.err = fmt.Errorf("schedule: time zone %q: %w", tz, err)
	}
	s.loc = loc
	return s
}

// Err returns the schedule's error, if building it failed.
func (s Schedule) Err() error { return s.err }

// String returns the cron expression, and the time zone if [In] set one.
func (s Schedule) String() string {
	if s.loc != nil {
		return s.expr + " (" + s.loc.String() + ")"
	}
	return s.expr
}

// Next returns the first time after t that the schedule runs, in its
// time zone (UTC if it has none), or the zero time if it never does.
func (s Schedule) Next(t time.Time) time.Time {
	return s.next(t, time.UTC)
}

// next is Next with the scheduler's default location.
func (s Schedule) next(t time.Time, def *time.Location) time.Time {
	if s.err != nil {
		return time.Time{}
	}
	loc := s.loc
	if loc == nil {
		loc = def
	}
	if loc == nil {
		loc = time.UTC
	}
	return s.spec.next(t.In(loc))
}
