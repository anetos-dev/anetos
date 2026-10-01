// SPDX-License-Identifier: Apache-2.0

package schedule_test

import (
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/schedule"
)

func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// runs returns the next n run times after start, formatted.
func runs(s schedule.Schedule, start time.Time, n int) []string {
	var out []string
	for range n {
		start = s.Next(start)
		if start.IsZero() {
			out = append(out, "never")
			break
		}
		out = append(out, start.Format("2006-01-02 15:04 Mon MST"))
	}
	return out
}

func TestCronNext(t *testing.T) {
	utc := time.UTC
	for _, tt := range []struct {
		sched schedule.Schedule
		from  string
		want  []string
	}{
		{schedule.EveryMinute(), "2026-01-01 23:58", []string{"2026-01-01 23:59 Thu UTC", "2026-01-02 00:00 Fri UTC"}},
		{schedule.Cron("*/15 9-10 * * *"), "2026-01-01 10:40", []string{"2026-01-01 10:45 Thu UTC", "2026-01-02 09:00 Fri UTC"}},
		{schedule.Cron("5,35 * * * *"), "2026-01-01 00:35", []string{"2026-01-01 01:05 Thu UTC", "2026-01-01 01:35 Thu UTC"}},
		{schedule.Cron("0 12 * * MON-FRI"), "2026-01-02 13:00", []string{"2026-01-05 12:00 Mon UTC", "2026-01-06 12:00 Tue UTC"}},
		{schedule.Cron("0 0 * jan,Jul sun"), "2026-01-01 00:00", []string{"2026-01-04 00:00 Sun UTC", "2026-01-11 00:00 Sun UTC"}},
		{schedule.Cron("0 0 * * 7"), "2026-01-01 00:00", []string{"2026-01-04 00:00 Sun UTC"}},
		// Day of month and day of week both restricted: either.
		{schedule.Cron("0 0 13 * FRI"), "2026-02-01 00:00", []string{"2026-02-06 00:00 Fri UTC", "2026-02-13 00:00 Fri UTC", "2026-02-20 00:00 Fri UTC", "2026-02-27 00:00 Fri UTC", "2026-03-06 00:00 Fri UTC", "2026-03-13 00:00 Fri UTC"}},
		{schedule.Cron("0 0 */10 * *"), "2026-01-05 00:00", []string{"2026-01-11 00:00 Sun UTC", "2026-01-21 00:00 Wed UTC", "2026-01-31 00:00 Sat UTC", "2026-02-01 00:00 Sun UTC"}},
		{schedule.Cron("30 */6 * * *"), "2026-01-01 06:30", []string{"2026-01-01 12:30 Thu UTC"}},
		{schedule.Cron("0 0 29 2 *"), "2026-01-01 00:00", []string{"2028-02-29 00:00 Tue UTC"}},
		{schedule.Cron("0 0 30 2 *"), "2026-01-01 00:00", []string{"never"}},
		{schedule.Cron("@weekly"), "2026-01-01 00:00", []string{"2026-01-04 00:00 Sun UTC"}},
		{schedule.Cron("@HOURLY"), "2026-01-01 00:00", []string{"2026-01-01 01:00 Thu UTC"}},
		{schedule.Every(5 * time.Minute), "2026-01-01 00:02", []string{"2026-01-01 00:05 Thu UTC"}},
		{schedule.Every(6 * time.Hour), "2026-01-01 01:00", []string{"2026-01-01 06:00 Thu UTC"}},
		{schedule.Every(24 * time.Hour), "2026-01-01 01:00", []string{"2026-01-02 00:00 Fri UTC"}},
		{schedule.HourlyAt(17), "2026-01-01 00:17", []string{"2026-01-01 01:17 Thu UTC"}},
		{schedule.DailyAt("02:30"), "2026-01-01 03:00", []string{"2026-01-02 02:30 Fri UTC"}},
		{schedule.WeeklyOn(time.Wednesday, "08:00"), "2026-01-01 00:00", []string{"2026-01-07 08:00 Wed UTC"}},
		{schedule.MonthlyOn(15, "23:59"), "2026-01-20 00:00", []string{"2026-02-15 23:59 Sun UTC"}},
		// Half-hour offsets: on the hour, local time.
		{schedule.Hourly().In("Asia/Kolkata"), "2026-01-01 00:00", []string{"2026-01-01 06:00 Thu IST", "2026-01-01 07:00 Thu IST"}}, // from 05:30 IST
		// Spring forward (New York, 2026-03-08 02:00 → 03:00): 02:30 is skipped.
		{schedule.DailyAt("02:30").In("America/New_York"), "2026-03-07 00:00", []string{"2026-03-07 02:30 Sat EST", "2026-03-09 02:30 Mon EDT"}},
		// Fall back (2026-11-01 02:00 → 01:00): 01:30 comes twice.
		{schedule.DailyAt("01:30").In("America/New_York"), "2026-11-01 00:00", []string{"2026-11-01 01:30 Sun EDT", "2026-11-01 01:30 Sun EST", "2026-11-02 01:30 Mon EST"}},
		{schedule.Hourly().In("America/New_York"), "2026-11-01 04:30", []string{"2026-11-01 01:00 Sun EDT", "2026-11-01 01:00 Sun EST", "2026-11-01 02:00 Sun EST"}},
		// Clock changes at midnight (Santiago 2026-09-06, Havana 2026-03-08,
		// Azores 2026-03-29): the day starts at 01:00, so midnight runs are
		// skipped, and later ones aren't.
		{schedule.Cron("@weekly").In("America/Santiago"), "2026-08-31 15:00", []string{"2026-09-13 00:00 Sun -03"}},
		{schedule.WeeklyOn(time.Sunday, "09:00").In("America/Santiago"), "2026-08-31 15:00", []string{"2026-09-06 09:00 Sun -03"}},
		{schedule.Cron("0 9 * * 1-5").In("America/Havana"), "2026-03-06 15:00", []string{"2026-03-09 09:00 Mon CDT"}},
		{schedule.Cron("* * 8 3 *").In("America/Havana"), "2026-03-01 00:00", []string{"2026-03-08 01:00 Sun CDT", "2026-03-08 01:01 Sun CDT"}},
		{schedule.MonthlyOn(1, "00:30").In("Atlantic/Azores"), "2026-03-20 00:00", []string{"2026-04-01 00:30 Wed +00"}},
		// "*/n" days count as unrestricted (as in cron): odd days that are Mondays.
		{schedule.Cron("0 0 */2 * MON"), "2026-01-01 00:00", []string{"2026-01-05 00:00 Mon UTC", "2026-01-19 00:00 Mon UTC"}},
		// Rare matches far away are found.
		{schedule.Cron("0 0 29 2 *"), "2026-01-01 00:00", []string{"2028-02-29 00:00 Tue UTC"}},
		{schedule.Cron("0 0 */30 2 1"), "2027-02-02 00:00", []string{"2038-02-01 00:00 Mon UTC"}}, // 1 February on a Monday
	} {
		got := runs(tt.sched, at(t, utc, tt.from), len(tt.want))
		if strings.Join(got, " | ") != strings.Join(tt.want, " | ") {
			t.Errorf("%s from %s:\n got %v\nwant %v", tt.sched, tt.from, got, tt.want)
		}
	}
}

// Every zone with clock changes at midnight, every day of two years: Next
// returns, and a time after the start.
func TestNextTerminates(t *testing.T) {
	scheds := []schedule.Schedule{schedule.Cron("@weekly"), schedule.Cron("0 9 * * 1-5"), schedule.MonthlyOn(1, "00:00"), schedule.Cron("30 0 * * *")}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, tz := range []string{"America/Santiago", "America/Havana", "Atlantic/Azores", "America/Asuncion", "Asia/Beirut", "America/New_York", "Australia/Lord_Howe"} {
			for _, s := range scheds {
				s = s.In(tz)
				for d := range 730 {
					from := time.Date(2026, 1, 1+d, 12, 0, 0, 0, time.UTC)
					if next := s.Next(from); !next.After(from) {
						t.Errorf("%s from %s: %s", s, from, next)
					}
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("Next doesn't return")
	}
}

func TestScheduleErrors(t *testing.T) {
	for _, s := range []schedule.Schedule{
		schedule.Cron("* * * *"), schedule.Cron("60 * * * *"), schedule.Cron("* 24 * * *"), schedule.Cron("* * 0 * *"),
		schedule.Cron("* * * 13 *"), schedule.Cron("* * * * 8"), schedule.Cron("5-1 * * * *"), schedule.Cron("*/0 * * * *"),
		schedule.Cron("*/61 * * * *"), schedule.Cron("5/9223372036854775807 * * * *"),
		schedule.Cron("x * * * *"), schedule.Cron("* * * FOO *"), schedule.Cron("@reboot"),
		schedule.Every(7 * time.Minute), schedule.Every(90 * time.Second), schedule.Every(5 * time.Hour), schedule.Every(0),
		schedule.DailyAt("24:00"), schedule.DailyAt("02:60"), schedule.DailyAt("noon"),
		schedule.WeeklyOn(9, "08:00"), schedule.MonthlyOn(31, "08:00"), schedule.MonthlyOn(1, "8"),
		schedule.Daily().In("Mars/Olympus"), schedule.HourlyAt(60),
	} {
		if s.Err() == nil {
			t.Errorf("%s: no error", s)
		}
	}
	if got := schedule.Cron("0  2 * *  *").In("Asia/Dhaka").String(); got != "0 2 * * * (Asia/Dhaka)" {
		t.Errorf("String = %q", got)
	}
	if got := schedule.DailyAt("02:30").String(); got != "30 2 * * *" {
		t.Errorf("String = %q", got)
	}
}
