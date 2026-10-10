// SPDX-License-Identifier: Apache-2.0

// Package jobs holds the app's queue jobs, registered in setup and run by
// the workers (run --only=workers): the emails about issues.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/mailers"
	"anetos.dev/anetos/examples/tracker/app/models"
)

// NotifyAssigned emails the assignee of an issue. Dispatched when an
// issue is assigned, after the change commits.
type NotifyAssigned struct {
	IssueID    int64 `json:"issue_id"`
	AssigneeID int64 `json:"assignee_id"`
	ActorID    int64 `json:"actor_id"` // who assigned it
}

// Handle sends the email, unless the issue was reassigned or deleted
// since, or the assignee turned the emails off.
func (j NotifyAssigned) Handle(ctx context.Context) error {
	issue, err := db.Query[models.Issue](ctx).With(models.IssueRels.Project, models.IssueRels.Assignee).Find(j.IssueID)
	if errors.Is(err, db.ErrNotFound) {
		return nil // deleted since
	}
	if err != nil {
		return err
	}
	u := issue.Assignee
	if u == nil || u.ID != j.AssigneeID || !u.Notify || u.DisabledAt != nil || !canSee(ctx, *u, issue.Project.Key) {
		return nil // reassigned, or not to be told (anymore)
	}
	actor, err := db.Find[models.User](ctx, j.ActorID)
	if err != nil {
		return queue.Permanent(err)
	}
	ctx = i18n.ForUser(ctx, u) // the email in the assignee's language
	url, err := issueURL(ctx, issue)
	if err != nil {
		return err
	}
	return mailer.Send(ctx, mailers.Assigned{
		Name: u.Name, Email: u.Email, Ref: issue.Ref(issue.Project.Key), Title: issue.Title, By: actor.Name, URL: url,
	})
}

// NotifyComment emails the people following an issue about a new
// comment: its author, its assignee, and those who commented before;
// not the comment's author, and not users who turned the emails off, are
// disabled, or can no longer see the project.
type NotifyComment struct {
	CommentID int64 `json:"comment_id"`
}

// Handle sends the emails, one per recipient, each in their language.
func (j NotifyComment) Handle(ctx context.Context) error {
	comment, err := db.Query[models.Comment](ctx).With(models.CommentRels.Author).Find(j.CommentID)
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	issue, err := db.Query[models.Issue](ctx).With(models.IssueRels.Project).Find(comment.IssueID)
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	ids := []int64{issue.AuthorID}
	if issue.AssigneeID != nil {
		ids = append(ids, *issue.AssigneeID)
	}
	earlier, err := db.Pluck(db.Query[models.Comment](ctx).
		Where(models.CommentCols.IssueID.Eq(issue.ID), models.CommentCols.ID.Lt(comment.ID)), models.CommentCols.AuthorID)
	if err != nil {
		return err
	}
	ids = slices.DeleteFunc(append(ids, earlier...), func(id int64) bool { return id == comment.AuthorID })
	if len(ids) == 0 {
		return nil
	}
	users, err := db.Query[models.User](ctx).
		Where(models.UserCols.ID.In(slices.Compact(slices.Sorted(slices.Values(ids)))...), models.UserCols.Notify.Eq(true)).Get()
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.DisabledAt != nil || !canSee(ctx, u, issue.Project.Key) {
			continue
		}
		ctx := i18n.ForUser(ctx, &u)
		url, err := issueURL(ctx, issue)
		if err != nil {
			return err
		}
		// A retry after a failure part-way sends the earlier emails again:
		// at least once, as queue jobs run.
		if err := mailer.Send(ctx, mailers.Commented{
			Name: u.Name, Email: u.Email, Ref: issue.Ref(issue.Project.Key), Title: issue.Title,
			By: comment.Author.Name, Body: excerpt(comment.Body, 500), URL: url,
		}); err != nil {
			return err
		}
	}
	return nil
}

// canSee reports whether the user may still see the project's issues.
func canSee(ctx context.Context, u models.User, projectKey string) bool {
	g, err := rbac.Of(ctx, u.AuthID()) // a job has no logged-in user: ask for theirs
	return err == nil && g.CanIn(access.Project(projectKey), access.ViewIssues)
}

// issueURL is the absolute URL of the issue's page, for emails.
func issueURL(ctx context.Context, issue models.Issue) (string, error) {
	return mailer.URL(ctx, fmt.Sprintf("/p/%s/issues/%d", issue.Project.Key, issue.Number))
}

// excerpt is s cut to at most n bytes, at a character boundary, with an
// ellipsis if it was cut.
func excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
