// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"
	"strings"
)

// CheckTimeZone returns an error unless the database session's time
// zone is UTC, so times the database writes itself (CURRENT_TIMESTAMP,
// NOW(), MySQL's TIMESTAMP columns) are UTC like the ones the app
// writes. The drivers open sessions in UTC, so a zone comes from the
// connection string (timezone= or time_zone= in DB_URL); [DB.Check]
// runs it at boot unless DB_ALLOW_LOCAL_TIMEZONE is set. SQLite has no
// session zone.
func (d *DB) CheckTimeZone(ctx context.Context) error {
	var zone string
	var ok bool
	switch d.dialect.Name() {
	case "postgres":
		// The session zone's offset in winter and in summer: both zero
		// for UTC and its aliases, whatever the server calls it.
		var jan, jul float64
		if err := d.sql.QueryRowContext(ctx, `SELECT current_setting('TimeZone'), `+
			`EXTRACT(TIMEZONE FROM TIMESTAMPTZ '2026-01-15 12:00:00+00'), `+
			`EXTRACT(TIMEZONE FROM TIMESTAMPTZ '2026-07-15 12:00:00+00')`).Scan(&zone, &jan, &jul); err != nil {
			return fmt.Errorf("db: read the session's time zone: %w", err)
		}
		ok = jan == 0 && jul == 0
	case "mysql":
		// Two instants, in winter and in summer, shown in the session's
		// zone: both at 12:00 only in UTC. Named zones need the server's
		// time zone tables; this doesn't.
		var system string
		if err := d.sql.QueryRowContext(ctx, `SELECT @@session.time_zone, @@system_time_zone, `+
			`FROM_UNIXTIME(1768478400) = '2026-01-15 12:00:00' AND FROM_UNIXTIME(1784116800) = '2026-07-15 12:00:00'`).
			Scan(&zone, &system, &ok); err != nil {
			return fmt.Errorf("db: read the session's time zone: %w", err)
		}
		if strings.EqualFold(zone, "SYSTEM") {
			zone = "the server's (" + system + ")"
		}
	default:
		return nil
	}
	if ok {
		return nil
	}
	param := "timezone="
	if d.dialect.Name() == "mysql" {
		param = "time_zone="
	}
	return fmt.Errorf("db: the database session's time zone is %s, not UTC: times the database writes (CURRENT_TIMESTAMP, NOW()) "+
		"would be local while the app writes UTC. Remove %s from DB_URL, or set DB_ALLOW_LOCAL_TIMEZONE=true to keep it", zone, param)
}
