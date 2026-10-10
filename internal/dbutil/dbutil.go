// SPDX-License-Identifier: Apache-2.0

// Package dbutil holds SQL helpers shared by the stores that keep their
// data in the app's database (the cache and the queue).
package dbutil

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strings"
	"time"
)

// SearchIndexesTable is the table where migrations record the search
// indexes they make (packages db and migrate).
const SearchIndexesTable = "search_indexes"

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
// (SQLSTATE 40001 or 40P01, MySQL error 1213, and MariaDB's 1020, "Record
// has changed since last read", from innodb_snapshot_isolation, on by
// default since 11.6).
func Transient(err error) bool {
	msg := err.Error()
	for _, s := range []string{"SQLSTATE 40001", "SQLSTATE 40P01", "Error 1213 (40001)", "Error 1020 (HY000)"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// MaxIdent is the longest identifier every database keeps whole
// (PostgreSQL truncates at 63 bytes, MySQL refuses more than 64).
const MaxIdent = 63

// IndexName names an index the way migrations do, following Laravel:
// posts_author_id_created_at_index, lower-case. Names longer than
// MaxIdent are shortened with a hash of the full name, so different long
// names stay different, and code that looks an index up by name (Drop*,
// a search's BM25 index) computes the same name.
func IndexName(table string, columns []string, suffix string) string {
	name := strings.ToLower(strings.NewReplacer(".", "_", "-", "_").Replace(table + "_" + strings.Join(columns, "_") + "_" + suffix))
	if len(name) <= MaxIdent {
		return name
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	return fmt.Sprintf("%s_%08x", name[:MaxIdent-9], h.Sum32())
}
