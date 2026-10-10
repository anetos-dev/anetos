// SPDX-License-Identifier: Apache-2.0

// Package dbhook lets the framework's test helpers (anetostest, dbtest)
// reach what package db keeps unexported.
package dbhook

import (
	"context"
	"database/sql"
)

// WithTestTx returns ctx with tx as a test's transaction, which is
// rolled back at the end of the test instead of committed: work done at
// its level counts as committed (db.AfterCommit callbacks registered in
// it run at once). Package db sets it.
var WithTestTx func(ctx context.Context, tx *sql.Tx) (context.Context, error)
