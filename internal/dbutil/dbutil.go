// SPDX-License-Identifier: Apache-2.0

// Package dbutil holds SQL helpers shared by the stores that keep their
// data in the app's database (the cache and the queue).
package dbutil

import (
	"context"
	"math/rand/v2"
	"strings"
	"time"
)

// NowMillis is the database server's time in Unix milliseconds, in SQL,
// for the dialect named (postgres, mysql or sqlite). Stores use the
// server's clock so instances with drifting clocks agree.
func NowMillis(dialect string) string {
	switch dialect {
	case "postgres":
		// statement_timestamp, unlike clock_timestamp, lets the planner
		// use an index on the compared column.
		return "CAST(EXTRACT(EPOCH FROM statement_timestamp()) * 1000 AS BIGINT)"
	case "mysql":
		// UTC_TIMESTAMP doesn't depend on the session's time zone.
		return "CAST(TIMESTAMPDIFF(MICROSECOND, '1970-01-01 00:00:00', UTC_TIMESTAMP(6)) DIV 1000 AS SIGNED)"
	default:
		return "CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)"
	}
}

// Retry runs fn again when the database chose it as a deadlock victim
// (MySQL's gap locks make that possible between concurrent inserts and
// deletes of one key) or failed it for serialization, a few times.
func Retry(ctx context.Context, fn func() error) error {
	var err error
	for i := range 10 {
		if err = fn(); err == nil || !Transient(err) {
			return err
		}
		t := time.NewTimer(time.Duration(1+rand.IntN(5<<i)) * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
	}
	return err
}

// Transient reports whether err is a deadlock or serialization failure
// (SQLSTATE 40001 or 40P01, MySQL error 1213).
func Transient(err error) bool {
	msg := err.Error()
	for _, s := range []string{"SQLSTATE 40001", "SQLSTATE 40P01", "Error 1213 (40001)"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
