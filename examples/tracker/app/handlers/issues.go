// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/jobs"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/views"
)

// Issues serves a project's issues.
type Issues struct{}

// IssueFields are the issue form's fields.
type IssueFields struct {
	Title    string  `json:"title" validate:"required|max:200"`
	Body     string  `json:"body" validate:"max:20000"`
	Priority string  `json:"priority" validate:"required|in:low,normal,high,urgent"`
	Assignee int64   `json:"assignee_id"` // 0: nobody
	Labels   []int64 `json:"labels" validate:"max:20"`
}

// NewIssueInput opens an issue in /p/{project}.
type NewIssueInput struct {
	Project string `path:"project"`
	IssueFields
}

// UpdateIssueInput edits /p/{project}/issues/{number}.
type UpdateIssueInput struct {
	IssuePath
	IssueFields
}

// New shows the form for a new issue.
func (Issues) New(c *web.Ctx, in ProjectPath) (web.Responder, error) {
	project, err := loadProject(c, in.Project, access.CreateIssues)
	if err != nil {
		return nil, err
	}
	form, err := issueForm(c, project, models.Issue{Priority: "normal"})
	if err != nil {
		return nil, err
	}
	return web.View(views.IssueFormPage(form)), nil
}

func issueForm(ctx context.Context, project models.Project, issue models.Issue) (views.IssueForm, error) {
	form := views.IssueForm{Project: project, Issue: issue}
	var err error
	if form.Labels, err = db.Query[models.Label](ctx).Where(models.LabelCols.ProjectID.Eq(project.ID)).OrderBy(models.LabelCols.Name.Asc()).Get(); err != nil {
		return form, err
	}
	form.Assignees, err = assignable(ctx, project)
	return form, err
}

// checkInput checks what the form refers to: the assignee is someone
// who may be assigned issues in the project, and the labels are the
// project's.
func checkInput(ctx context.Context, project models.Project, in IssueFields) (*int64, error) {
	var assignee *int64
	if in.Assignee != 0 {
		users, err := assignable(ctx, project)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(users, func(u models.User) bool { return u.ID == in.Assignee }) {
			return nil, validate.Fail("assignee_id", i18n.T(ctx, "issues.errors.assignee"))
		}
		assignee = &in.Assignee
	}
	if len(in.Labels) > 0 {
		n, err := db.Query[models.Label](ctx).Where(models.LabelCols.ProjectID.Eq(project.ID), models.LabelCols.ID.In(in.Labels...)).Count()
		if err != nil {
			return nil, err
		}
		if n != int64(len(in.Labels)) {
			return nil, validate.Fail("labels", i18n.T(ctx, "issues.errors.labels"))
		}
	}
	return assignee, nil
}

// Create opens an issue.
func (Issues) Create(c *web.Ctx, in NewIssueInput) (web.Responder, error) {
	project, err := loadProject(c, in.Project, access.CreateIssues)
	if err != nil {
		return nil, err
	}
	issue, err := openIssue(c, project, in.IssueFields)
	if err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "issues.status.created", "ref", issue.Ref(project.Key)))
	return web.RedirectRoute("issues.show", project.Key, issue.Number), nil
}

// openIssue opens an issue in the project, by the signed-in user,
// numbered after the project's newest one. Its assignee is told by
// email, once the issue is committed.
func openIssue(ctx context.Context, project models.Project, in IssueFields) (models.Issue, error) {
	u, err := currentUser(ctx)
	if err != nil {
		return models.Issue{}, err
	}
	assignee, err := checkInput(ctx, project, in)
	if err != nil {
		return models.Issue{}, err
	}
	issue := models.Issue{
		ProjectID: project.ID, Title: strings.TrimSpace(in.Title), Body: in.Body,
		Status: models.Open, Priority: in.Priority, AuthorID: u.ID, AssigneeID: assignee,
	}
	err = db.Tx(ctx, func(ctx context.Context) error {
		if err := db.Create(ctx, &issue); err != nil { // numbered by Issue.BeforeCreate
			return err
		}
		if err := db.Sync(ctx, &issue, models.IssueRels.Labels, anySlice(in.Labels)...); err != nil {
			return err
		}
		return notifyAssignee(ctx, issue, u.ID)
	})
	issue.Author = u
	return issue, err
}

// notifyAssignee queues the email to the issue's assignee, unless they
// assigned it to themselves; the job runs once the transaction commits.
func notifyAssignee(ctx context.Context, issue models.Issue, actorID int64) error {
	if issue.AssigneeID == nil || *issue.AssigneeID == actorID {
		return nil
	}
	return queue.Dispatch(ctx, jobs.NotifyAssigned{IssueID: issue.ID, AssigneeID: *issue.AssigneeID, ActorID: actorID}, queue.AfterCommit())
}

func anySlice[T any](s []T) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// Show is an issue's page: the issue, its comments and files, and what
// changed (the audit log).
func (Issues) Show(c *web.Ctx, in IssuePath) (web.Responder, error) {
	project, issue, err := loadIssue(c, in, access.ViewIssues)
	if err != nil {
		return nil, err
	}
	page := views.IssuePage{
		Project: project, Issue: issue,
		CanEdit:    !project.Archived() && access.Can(c, project.Key, access.EditIssues),
		CanComment: !project.Archived() && access.Can(c, project.Key, access.Comment),
		CanAttach:  !project.Archived() && access.Can(c, project.Key, access.CreateIssues),
		CanDelete:  !project.Archived() && access.Can(c, project.Key, access.ManageProjects),
	}
	if page.Comments, err = db.Query[models.Comment](c).Where(models.CommentCols.IssueID.Eq(issue.ID)).
		With(models.CommentRels.Author).OrderBy(models.CommentCols.ID.Asc()).Get(); err != nil {
		return nil, err
	}
	if page.Attachments, err = db.Query[models.Attachment](c).Where(models.AttachmentCols.IssueID.Eq(issue.ID)).
		With(models.AttachmentRels.Uploader).OrderBy(models.AttachmentCols.ID.Asc()).Get(); err != nil {
		return nil, err
	}
	if page.Activity, err = activity(c, issue); err != nil {
		return nil, err
	}
	if page.CanEdit {
		if page.Assignees, err = assignable(c, project); err != nil {
			return nil, err
		}
	}
	return web.View(views.IssuePageView(page)), nil
}

// tracked are the fields whose changes the issue's page shows.
var tracked = []string{"status", "assignee_id", "title", "priority"}

// activity reads the issue's history from the audit log, oldest first:
// its creation, then each change of a tracked field.
func activity(ctx context.Context, issue models.Issue) ([]views.Activity, error) {
	subject, err := audit.SubjectOf(&issue)
	if err != nil {
		return nil, err
	}
	events, _, err := audit.History(ctx, subject, 100, "")
	if err != nil {
		return nil, err
	}
	var out []views.Activity
	users := map[string]string{} // user IDs to names, for actors and assignees
	for _, e := range slices.Backward(events) {
		if e.Entry == nil {
			continue // bulk writes: none here
		}
		who := e.Actor()
		base := views.Activity{At: e.At()} // Who "": the system (a command, a task)
		if who.Type == "user" {
			base.Who = who.ID
			users[who.ID] = ""
		}
		switch e.Action() {
		case audit.Created:
			out = append(out, base)
		case audit.Deleted:
			base.Deleted = true
			out = append(out, base)
		case audit.Updated:
			for _, f := range tracked {
				old, changed := e.Entry.Changes.Old[f]
				if !changed {
					continue
				}
				a := base
				a.Field, a.Old, a.New = f, text(old), text(e.Entry.Changes.New[f])
				if f == "assignee_id" {
					users[a.Old], users[a.New] = "", ""
				}
				out = append(out, a)
			}
		}
	}
	if err := userNames(ctx, users); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Who = name(users, out[i].Who)
		if out[i].Field == "assignee_id" {
			out[i].Old, out[i].New = name(users, out[i].Old), name(users, out[i].New)
		}
	}
	return out, nil
}

// text is a logged value as text: "" for null. The log keeps values as
// JSON, so numbers come back as float64.
func text(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// userNames fills in the names of the users whose IDs are the keys.
func userNames(ctx context.Context, users map[string]string) error {
	var ids []int64
	for id := range users {
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			ids = append(ids, n)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	found, err := db.Query[models.User](ctx).Where(models.UserCols.ID.In(ids...)).Get()
	if err != nil {
		return err
	}
	for _, u := range found {
		users[u.AuthID()] = u.Name
	}
	return nil
}

// name is the user's name, or the ID if the user is gone ("" stays "").
func name(users map[string]string, id string) string {
	if n := users[id]; n != "" {
		return n
	}
	return id
}

// Edit shows the form to edit an issue.
func (Issues) Edit(c *web.Ctx, in IssuePath) (web.Responder, error) {
	project, issue, err := loadIssue(c, in, access.EditIssues)
	if err != nil {
		return nil, err
	}
	form, err := issueForm(c, project, issue)
	if err != nil {
		return nil, err
	}
	return web.View(views.IssueFormPage(form)), nil
}

// Update saves an issue's title, text, priority, assignee and labels. A
// new assignee is told by email.
func (Issues) Update(c *web.Ctx, in UpdateIssueInput) (web.Responder, error) {
	project, issue, err := loadIssue(c, in.IssuePath, access.EditIssues)
	if err != nil {
		return nil, err
	}
	u, err := currentUser(c)
	if err != nil {
		return nil, err
	}
	assignee, err := checkInput(c, project, in.IssueFields)
	if err != nil {
		return nil, err
	}
	reassigned := !sameID(issue.AssigneeID, assignee)
	issue.Title, issue.Body, issue.Priority, issue.AssigneeID = strings.TrimSpace(in.Title), in.Body, in.Priority, assignee
	err = db.Tx(c, func(ctx context.Context) error {
		if err := db.Update(ctx, &issue); err != nil {
			return err
		}
		if err := db.Sync(ctx, &issue, models.IssueRels.Labels, anySlice(in.Labels)...); err != nil {
			return err
		}
		if reassigned {
			return notifyAssignee(ctx, issue, u.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "issues.status.saved"))
	return web.RedirectRoute("issues.show", project.Key, issue.Number), nil
}

func sameID(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// StatusInput closes or reopens an issue.
type StatusInput struct {
	Project string `path:"project"`
	Number  int    `path:"number"`
	Status  string `json:"status" validate:"required|in:open,closed"`
}

// SetStatus closes or reopens an issue. With htmx, the issue's header is
// sent back to replace the old one; otherwise the browser goes back to
// the issue.
func (Issues) SetStatus(c *web.Ctx, in StatusInput) (web.Responder, error) {
	project, issue, err := loadIssue(c, IssuePath{in.Project, in.Number}, access.EditIssues)
	if err != nil {
		return nil, err
	}
	if issue.Status != in.Status {
		issue.Status = in.Status
		issue.ClosedAt = nil
		if in.Status == models.Closed {
			now := anetos.Now(c)
			issue.ClosedAt = &now
		}
		if err := db.Update(c, &issue); err != nil {
			return nil, err
		}
	}
	if c.IsHTMX() {
		return web.View(views.IssueHeader(project, issue, true)), nil
	}
	return web.RedirectRoute("issues.show", project.Key, issue.Number), nil
}

// Delete moves an issue to the trash (owners only); administrators can
// restore it in the admin.
func (Issues) Delete(c *web.Ctx, in IssuePath) (web.Responder, error) {
	project, issue, err := loadIssue(c, in, access.ManageProjects)
	if err != nil {
		return nil, err
	}
	if err := db.Delete(c, &issue); err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "issues.status.deleted", "ref", issue.Ref(project.Key)))
	return web.RedirectRoute("projects.show", project.Key), nil
}

// CommentInput is a new comment.
type CommentInput struct {
	Project string `path:"project"`
	Number  int    `path:"number"`
	Body    string `json:"body" validate:"required|max:10000"`
}

// Comment adds a comment, and queues the emails to the issue's author,
// assignee and earlier commenters. With htmx, the new comment is sent
// back to add to the list.
func (Issues) Comment(c *web.Ctx, in CommentInput) (web.Responder, error) {
	project, issue, err := loadIssue(c, IssuePath{in.Project, in.Number}, access.Comment)
	if err != nil {
		return nil, err
	}
	u, err := currentUser(c)
	if err != nil {
		return nil, err
	}
	comment := models.Comment{IssueID: issue.ID, AuthorID: u.ID, Body: strings.TrimSpace(in.Body)}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := db.Create(ctx, &comment); err != nil {
			return err
		}
		return queue.Dispatch(ctx, jobs.NotifyComment{CommentID: comment.ID}, queue.AfterCommit())
	})
	if err != nil {
		return nil, err
	}
	if c.IsHTMX() {
		comment.Author = u
		return web.View(views.CommentItem(comment)), nil
	}
	return web.RedirectRoute("issues.show", project.Key, issue.Number), nil
}
