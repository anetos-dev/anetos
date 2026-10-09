// SPDX-License-Identifier: Apache-2.0

// Package views holds the templ components: the pages, their parts and
// the emails. Their text is in locales/.
package views

import (
	"context"
	"net/url"
	"strconv"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/views/ui"
)

// DigestLine is an issue in the digest email.
type DigestLine struct {
	Ref, Title, Priority string
	URL                  string
}

// filterURL is the URL of the project's page with the filter f, with
// the parameter key set to value ("" removes it); the page goes back to
// the first.
func filterURL(ctx context.Context, project models.Project, f Filter, key, value string) string {
	base, err := web.URL(ctx, "projects.show", project.Key)
	if err != nil {
		return ""
	}
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("status", f.Status)
	set("label", f.Label)
	set("assignee", f.Assignee)
	set("q", f.Q)
	set("sort", f.Sort)
	if value == "" {
		q.Del(key)
	} else {
		q.Set(key, value)
	}
	if q.Get("status") == models.Open {
		q.Del("status") // the default
	}
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

// issueURL is the issue's page.
func issueURL(ctx context.Context, projectKey string, number int) (string, error) {
	return web.URL(ctx, "issues.show", projectKey, number)
}

// issuePages is the pagination of a project's issues: "Page 2 of 5"
// when there is more than one.
func issuePages(ctx context.Context, page db.Page[models.Issue]) ui.Pages {
	status := ""
	if page.LastPage > 1 {
		status = i18n.T(ctx, "pages.of", "page", page.CurrentPage, "last", page.LastPage)
	}
	return ui.PagesOf(ctx, page, i18n.T(ctx, "pages.label"), status, i18n.T(ctx, "pages.prev"), i18n.T(ctx, "pages.next"))
}

// priorityOptions are the priority select's options, lowest first.
func priorityOptions(ctx context.Context) []ui.Option {
	options := make([]ui.Option, 0, len(models.Priorities))
	for _, p := range models.Priorities {
		options = append(options, ui.Option{Value: p, Label: i18n.T(ctx, "issues.priorities."+p)})
	}
	return options
}

// assigneeOptions are the assignee select's options: nobody, then the
// users who can be assigned.
func assigneeOptions(ctx context.Context, users []models.User) []ui.Option {
	options := []ui.Option{{Value: "", Label: i18n.T(ctx, "issues.nobody")}}
	for _, u := range users {
		options = append(options, ui.Option{Value: idString(u.ID), Label: u.Name})
	}
	return options
}

// idString is an ID as text, for form values.
func idString(id int64) string { return strconv.FormatInt(id, 10) }

// assigneeValue is the assignee's ID as a form value, "" for nobody.
func assigneeValue(id *int64) string {
	if id == nil {
		return ""
	}
	return idString(*id)
}

// hasLabel reports whether the issue has the label.
func hasLabel(issue models.Issue, id int64) bool {
	for _, l := range issue.Labels {
		if l.ID == id {
			return true
		}
	}
	return false
}

// size is a file size for people: 2.4 MB.
func size(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 1, 64) + " KB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}

// issueFormTitle is the title of the issue form.
func issueFormTitle(ctx context.Context, f IssueForm) string {
	if f.Issue.ID == 0 {
		return i18n.T(ctx, "issues.new")
	}
	return i18n.T(ctx, "issues.edit_title", "ref", f.Issue.Ref(f.Project.Key))
}

// activityText describes a change in the issue's history.
func activityText(ctx context.Context, a Activity) string {
	if a.Who == "" {
		a.Who = i18n.T(ctx, "activity.system")
	}
	switch {
	case a.Deleted:
		return i18n.T(ctx, "activity.deleted", "who", a.Who)
	case a.Field == "":
		return i18n.T(ctx, "activity.created", "who", a.Who)
	case a.Field == "status" && a.New == models.Closed:
		return i18n.T(ctx, "activity.closed", "who", a.Who)
	case a.Field == "status":
		return i18n.T(ctx, "activity.reopened", "who", a.Who)
	case a.Field == "assignee_id" && a.New == "":
		return i18n.T(ctx, "activity.unassigned", "who", a.Who, "old", a.Old)
	case a.Field == "assignee_id":
		return i18n.T(ctx, "activity.assigned", "who", a.Who, "new", a.New)
	case a.Field == "priority":
		return i18n.T(ctx, "activity.priority", "who", a.Who,
			"old", i18n.T(ctx, "issues.priorities."+a.Old), "new", i18n.T(ctx, "issues.priorities."+a.New))
	default: // title
		return i18n.T(ctx, "activity.renamed", "who", a.Who, "old", a.Old, "new", a.New)
	}
}
