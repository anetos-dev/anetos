// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"anetos.dev/anetos/db"
)

var colCreatedAt = db.Col[time.Time]("created_at")

// region: task
// pruneAuditLog deletes the audit log's entries older than 90 days. It
// runs every night (see setup) on one instance, and is safe to run again:
// it deletes what is old when it runs.
func pruneAuditLog(ctx context.Context) error {
	n, err := db.Query[AuditEntry](ctx).Where(colCreatedAt.Lt(time.Now().AddDate(0, 0, -90))).Delete()
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "audit log pruned", "deleted", n)
	return nil
}

// endregion

// salesReports stands in for where reports go (a mail server, a chat
// channel): it keeps them.
var salesReports = &Outbox{}

// region: report-job
// SalesReport is a queue job that reports the orders paid in the hour
// before it runs. The scheduler dispatches it every hour; a worker runs
// it, with the queue's retries.
type SalesReport struct{}

// Handle counts the orders and sends the report.
func (SalesReport) Handle(ctx context.Context) error {
	n, err := db.Query[Order](ctx).Where(colStatus.Eq("paid"), colCreatedAt.Gte(time.Now().Add(-time.Hour))).Count()
	if err != nil {
		return err
	}
	salesReports.mu.Lock()
	defer salesReports.mu.Unlock()
	salesReports.sent = append(salesReports.sent, fmt.Sprintf("Sales report: orders paid in the last hour: %d", n))
	return nil
}

// endregion
