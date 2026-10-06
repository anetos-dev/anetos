// SPDX-License-Identifier: Apache-2.0

// Package tasks holds the app's scheduled tasks, run by the scheduler
// (run --only=scheduler).
package tasks

import (
	"context"
	"fmt"
	"slices"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/mailer"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/mailers"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/views"
)

// SendDigests emails each user with open issues assigned to them the
// list, most pressing first. The schedule runs it on weekday mornings,
// on one server; the emails are queued, so a slow mail server doesn't
// hold the scheduler.
func SendDigests(ctx context.Context) error {
	issues, err := db.Query[models.Issue](ctx).
		Where(models.IssueCols.Status.Eq(models.Open), models.IssueCols.AssigneeID.NotNull()).
		WhereHas(models.IssueRels.Project, models.ProjectCols.ArchivedAt.IsNull()).
		With(models.IssueRels.Project, models.IssueRels.Assignee).
		OrderBy(db.OrderRaw("CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END"),
			models.IssueCols.UpdatedAt.Desc()).
		Get()
	if err != nil {
		return err
	}
	byUser := map[int64][]models.Issue{}
	var order []int64
	for _, issue := range issues {
		id := issue.Assignee.ID
		if _, ok := byUser[id]; !ok {
			order = append(order, id)
		}
		byUser[id] = append(byUser[id], issue)
	}
	sent := 0
	for _, id := range order {
		list := byUser[id]
		u := list[0].Assignee
		if !u.Notify || u.DisabledAt != nil {
			continue
		}
		// Only the projects they still see.
		g, err := rbac.Of(ctx, u.AuthID())
		if err != nil {
			return err
		}
		list = slices.DeleteFunc(list, func(i models.Issue) bool {
			return !g.CanIn(access.Project(i.Project.Key), access.ViewIssues)
		})
		if len(list) == 0 {
			continue
		}
		ctx := i18n.ForUser(ctx, u)
		lines := make([]views.DigestLine, len(list))
		for i, issue := range list {
			url, err := mailer.URL(ctx, fmt.Sprintf("/p/%s/issues/%d", issue.Project.Key, issue.Number))
			if err != nil {
				return err
			}
			lines[i] = views.DigestLine{Ref: issue.Ref(issue.Project.Key), Title: issue.Title, Priority: issue.Priority, URL: url}
		}
		dashboard, err := mailer.URL(ctx, "/dashboard")
		if err != nil {
			return err
		}
		if err := mailer.Queue(ctx, mailers.Digest{Name: u.Name, Email: u.Email, Issues: lines, URL: dashboard}); err != nil {
			return err
		}
		sent++
	}
	anetos.Logger(ctx).InfoContext(ctx, "digests queued", "users", sent)
	return nil
}
