// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
)

func init() {
	extra = append(extra, test{"Dates", testDates}, test{"SessionTimeZone", testSessionTimeZone})
}

// stDated is st_events with its DATE column as an anetos.Date.
type stDated struct {
	ID     int64        `db:"id,pk"`
	Name   string       `db:"name"`
	Logged *time.Time   `db:"logged"`
	Day    *anetos.Date `db:"day"`
}

// TableName implements db.Tabler.
func (stDated) TableName() string { return "st_events" }

// testDates checks that an anetos.Date keeps its day on every database,
// and that times reach the database in UTC on every path.
func testDates(t *testing.T, ctx context.Context) {
	day := anetos.NewDate(2026, 3, 15)
	next := day.AddDays(1)
	e := stDated{Name: "dated", Day: &day}
	check(t, db.Create(ctx, &e))
	check(t, db.Create(ctx, &stDated{Name: "next", Day: &next}))
	check(t, db.Create(ctx, &stDated{Name: "none"}))

	got, err := db.Find[stDated](ctx, e.ID)
	check(t, err)
	if got.Day == nil || *got.Day != day {
		t.Fatalf("day = %v, want %v", got.Day, day)
	}
	col := db.Col[anetos.Date]("day")
	names := func(q *db.Q[stDated]) []string {
		t.Helper()
		rows, err := q.OrderBy(col.Asc()).Get()
		check(t, err)
		var out []string
		for _, r := range rows {
			out = append(out, r.Name)
		}
		return out
	}
	if got := names(db.Query[stDated](ctx).Where(col.Eq(day))); len(got) != 1 || got[0] != "dated" {
		t.Errorf("day = %v: %v", day, got)
	}
	if got := names(db.Query[stDated](ctx).Where(col.Gt(day))); len(got) != 1 || got[0] != "next" {
		t.Errorf("day > %v: %v", day, got)
	}
	if got := names(db.Query[stDated](ctx).Where(col.IsNull())); len(got) != 1 || got[0] != "none" {
		t.Errorf("day IS NULL: %v", got)
	}
	raw, err := db.RawFirst[anetos.Date](ctx, "SELECT day FROM st_events WHERE id = ?", e.ID)
	check(t, err)
	if raw != day {
		t.Errorf("raw day = %v", raw)
	}

	// A time in another zone, written by raw SQL, is the same instant.
	dhaka := time.Date(2026, 3, 16, 1, 0, 0, 0, time.FixedZone("BST", 6*3600))
	_, err = db.Exec(ctx, "UPDATE st_events SET logged = ? WHERE id = ?", dhaka, e.ID)
	check(t, err)
	got, err = db.Find[stDated](ctx, e.ID)
	check(t, err)
	if got.Logged == nil || !got.Logged.Equal(dhaka) || got.Logged.Location() != time.UTC {
		t.Errorf("logged = %v, want %v in UTC", got.Logged, dhaka)
	}
}

// testSessionTimeZone checks that the driver opens sessions in UTC.
func testSessionTimeZone(t *testing.T, ctx context.Context) {
	d, err := db.From(ctx)
	check(t, err)
	if err := d.CheckTimeZone(ctx); err != nil {
		t.Error(err)
	}
}

// RunLocalTimeZone checks a database opened with cfg, whose connection
// string sets a session time zone other than UTC: [db.DB.Check] refuses
// it, naming the zone, unless cfg allows local time zones.
func RunLocalTimeZone(t *testing.T, drv db.Driver, cfg db.Config) {
	t.Helper()
	for _, allow := range []bool{false, true} {
		cfg.AllowLocalTimeZone = allow
		d, err := db.Open(drv, cfg)
		check(t, err)
		err = d.Check(t.Context())
		d.Close()
		switch {
		case allow && err != nil:
			t.Errorf("DB_ALLOW_LOCAL_TIMEZONE=true: %v", err)
		case !allow && (err == nil || !strings.Contains(err.Error(), "not UTC") || !strings.Contains(err.Error(), "DB_ALLOW_LOCAL_TIMEZONE")):
			t.Errorf("a session in local time: %v", err)
		}
	}
}
