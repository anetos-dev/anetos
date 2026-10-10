// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/views"
)

// SearchInput is the search box: words to find in the issues of every
// project the user sees.
type SearchInput struct {
	Q string `query:"q" validate:"max:200"`
}

// Search finds issues by their title and text (the full-text index of
// the issues table), best matches first, in the projects the user sees.
func Search(c *web.Ctx, in SearchInput) (web.Responder, error) {
	results, err := searchIssues(c, in.Q, 50)
	if err != nil {
		return nil, err
	}
	return web.Render(views.SearchPage(in.Q, results)), nil
}

// searchIssues returns up to limit issues matching q in the projects the
// logged-in user sees.
func searchIssues(ctx context.Context, q string, limit int) ([]views.SearchResult, error) {
	if q == "" {
		return nil, nil
	}
	keys, all, err := access.VisibleKeys(ctx)
	if err != nil || !all && len(keys) == 0 {
		return nil, err
	}
	query := db.Query[models.Issue](ctx).With(models.IssueRels.Project).Search(q).Limit(limit)
	if !all {
		query = query.WhereHas(models.IssueRels.Project, models.ProjectCols.Key.In(keys...))
	}
	issues, err := query.Get()
	if err != nil {
		return nil, err
	}
	out := make([]views.SearchResult, len(issues))
	for i, issue := range issues {
		out[i] = views.SearchResult{Project: *issue.Project, Issue: issue}
	}
	return out, nil
}

// assignedTo returns the open issues assigned to the user, most
// pressing first, for the dashboard.
func assignedTo(ctx context.Context, userID int64) ([]views.AssignedIssue, error) {
	keys, all, err := access.VisibleKeys(ctx)
	if err != nil || !all && len(keys) == 0 {
		return nil, err
	}
	q := db.Query[models.Issue](ctx).
		Where(models.IssueCols.AssigneeID.Eq(&userID), models.IssueCols.Status.Eq(models.Open)).
		With(models.IssueRels.Project).
		OrderBy(db.OrderRaw(priorityOrder), models.IssueCols.UpdatedAt.Desc()).
		Limit(50)
	if !all { // not the projects they left
		q = q.WhereHas(models.IssueRels.Project, models.ProjectCols.Key.In(keys...))
	}
	issues, err := q.Get()
	if err != nil {
		return nil, err
	}
	out := make([]views.AssignedIssue, len(issues))
	for i, issue := range issues {
		out[i] = views.AssignedIssue{ProjectKey: issue.Project.Key, Issue: issue}
	}
	return out, nil
}

// priorityOrder sorts issues from urgent to low.
const priorityOrder = "CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END"
