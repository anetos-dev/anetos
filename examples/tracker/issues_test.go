// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/tracker/app/jobs"
	"anetos.dev/anetos/examples/tracker/app/mailers"
	"anetos.dev/anetos/examples/tracker/app/models"
)

func TestOpenIssue(t *testing.T) {
	w := newWorld(t)
	app := w.as(w.member)
	app.Get(w.path("issues/new")).AssertOK().AssertSee("Max", "Olivia", "bug", "idea").AssertDontSee("Vera") // viewers aren't assignable

	app.PostForm(w.path("issues"), form("title", "", "priority", "normal")).
		AssertRedirect(w.path("issues/new")).
		AssertValidationErrors("title")
	app.PostForm(w.path("issues"), form("title", "x", "priority", "whenever")).AssertValidationErrors("priority")
	app.PostForm(w.path("issues"), form("title", "x", "priority", "normal", "assignee_id", id(w.viewer.ID))).AssertValidationErrors("assignee_id")
	app.PostForm(w.path("issues"), form("title", "x", "priority", "normal", "labels", "999")).AssertValidationErrors("labels")

	app.PostForm(w.path("issues"), form(
		"title", " Login fails ", "body", "Since Tuesday.", "priority", "high",
		"assignee_id", id(w.owner.ID), "labels", id(w.bug.ID), "labels", id(w.idea.ID),
	)).
		AssertRedirect(w.path("issues/1")).
		AssertSessionHas("status", "WEB-1 opened.").
		Follow().
		AssertSee("Login fails", "WEB-1", "Since Tuesday.", "High", "Olivia", "bug", "idea", "Max opened the issue")

	// Numbers follow the project's newest issue, also for issues made
	// elsewhere (factories, the API).
	w.issue()
	app.PostForm(w.path("issues"), form("title", "Third", "priority", "low")).AssertRedirect(w.path("issues/3"))
	other := anetostest.Create(w.app, projectWithKey("API"))
	i := anetostest.Create(w.app, issueIn(other, w.owner))
	if i.Number != 1 {
		t.Errorf("first issue of another project: number %d", i.Number)
	}

	// The assignee is told by email, in a queue job after the commit;
	// not someone who assigns themselves.
	sent := anetostest.Mailables[mailers.Assigned](w.app)
	if len(sent) != 1 || sent[0].Email != w.owner.Email || sent[0].Ref != "WEB-1" || sent[0].By != "Max" ||
		sent[0].URL != "http://example.test/p/WEB/issues/1" {
		t.Errorf("assignment emails: %+v", sent)
	}
	app.PostForm(w.path("issues"), form("title", "Mine", "priority", "low", "assignee_id", id(w.member.ID))).AssertRedirect(w.path("issues/4"))
	if n := len(anetostest.Mailables[mailers.Assigned](w.app)); n != 1 {
		t.Errorf("self-assignment emailed: %d emails", n)
	}
}

func TestIssueList(t *testing.T) {
	w := newSearchWorld(t)
	w.issue(func(i *models.Issue) { i.Title, i.Priority = "Crash on save", "urgent" })
	w.issue(func(i *models.Issue) { i.Title, i.AssigneeID = "Slow search", &w.member.ID })
	w.issue(func(i *models.Issue) { i.Title, i.Status = "Old bug", models.Closed })
	if err := addLabel(w, 1, w.bug); err != nil {
		t.Fatal(err)
	}
	app := w.as(w.member)
	app.Get(w.path("")).AssertSee("Crash on save", "Slow search").AssertDontSee("Old bug")
	app.Get(w.path("?status=closed")).AssertSee("Old bug").AssertDontSee("Crash on save")
	app.Get(w.path("?status=all")).AssertSee("Old bug", "Crash on save")
	app.Get(w.path("?label=bug")).AssertSee("Crash on save").AssertDontSee("Slow search")
	app.Get(w.path("?assignee=me")).AssertSee("Slow search").AssertDontSee("Crash on save")
	app.Get(w.path("?assignee=none")).AssertSee("Crash on save").AssertDontSee("Slow search")
	app.Get(w.path("?assignee=" + id(w.member.ID))).AssertSee("Slow search").AssertDontSee("Crash on save")
	app.Get(w.path("?q=crash")).AssertSee("Crash on save").AssertDontSee("Slow search")
	app.Get(w.path("?status=bogus")).AssertStatus(http.StatusUnprocessableEntity)

	// Orders: newest first by default, oldest, most pressing.
	page := app.Get(w.path("?sort=oldest")).Text()
	if strings.Index(page, "Crash on save") > strings.Index(page, "Slow search") {
		t.Error("oldest: Crash on save isn't first")
	}
	page = app.Get(w.path("")).Text()
	if strings.Index(page, "Slow search") > strings.Index(page, "Crash on save") {
		t.Error("newest: Slow search isn't first")
	}
	w.issue(func(i *models.Issue) { i.Title = "Later" })
	page = app.Get(w.path("?sort=priority")).Text()
	if strings.Index(page, "Crash on save") > strings.Index(page, "Later") {
		t.Error("priority: the urgent issue isn't first")
	}

	// 25 a page; the filters stay in the pages' links.
	for range 30 {
		w.issue()
	}
	app.Get(w.path("?label=")).AssertSee("Page 1 of 2", `href="?label=&amp;page=2"`)
}

func TestEditIssue(t *testing.T) {
	w := newWorld(t)
	w.issue(func(i *models.Issue) { i.Title = "Typo" })
	app := w.as(w.member)
	app.Get(w.path("issues/1/edit")).AssertOK().AssertSee(`value="Typo"`, `name="_method" value="PUT"`)
	app.PutForm(w.path("issues/1"), form("title", "Typo on the pricing page", "priority", "urgent",
		"assignee_id", id(w.owner.ID), "labels", id(w.bug.ID))).
		AssertRedirect(w.path("issues/1"))
	anetostest.AssertDatabaseHas[models.Issue](w.app, models.IssueCols.Title.Eq("Typo on the pricing page"), models.IssueCols.Priority.Eq("urgent"))
	if n := len(anetostest.Mailables[mailers.Assigned](w.app)); n != 1 {
		t.Errorf("reassignment: %d emails", n)
	}
	// Saving again without a new assignee sends nothing.
	app.PutForm(w.path("issues/1"), form("title", "Typo on the pricing page", "priority", "urgent", "assignee_id", id(w.owner.ID))).AssertRedirect(w.path("issues/1"))
	if n := len(anetostest.Mailables[mailers.Assigned](w.app)); n != 1 {
		t.Errorf("same assignee: %d emails", n)
	}

	// The history, from the audit log.
	app.Get(w.path("issues/1")).AssertSee(
		"The system opened the issue", // made by a factory, outside a request
		"Max changed the title from “Typo” to “Typo on the pricing page”",
		"Max changed the priority from Normal to Urgent",
		"Max assigned it to Olivia",
	).AssertDontSee(">bug<", "idea") // labels taken off the second time
}

func TestCloseAndReopen(t *testing.T) {
	w := newWorld(t)
	w.issue()
	app := w.as(w.member)
	app.Get(w.path("issues/1")).AssertSee("Close issue")
	app.PostForm(w.path("issues/1/status"), form("status", "closed")).AssertRedirect(w.path("issues/1"))
	issue, err := db.Query[models.Issue](w.app.Context()).First()
	if err != nil || issue.Status != models.Closed || issue.ClosedAt == nil {
		t.Fatalf("closed: %+v, %v", issue, err)
	}

	// With htmx: the new header comes back.
	app.WithHeader("HX-Request", "true")
	app.PostForm(w.path("issues/1/status"), form("status", "open")).
		AssertOK().
		AssertSee(`id="issue-header"`, "Close issue").
		AssertDontSee("<html")
	app.WithHeader("HX-Request", "")
	app.Get(w.path("issues/1")).AssertSee("Max closed the issue", "Max reopened the issue")
	w.as(w.viewer).PostForm(w.path("issues/1/status"), form("status", "closed")).AssertForbidden()
}

func TestDeleteIssue(t *testing.T) {
	w := newWorld(t)
	issue := w.issue()
	w.as(w.member).Get(w.path("issues/1")).AssertDontSee("Move to trash")
	w.as(w.member).DeleteForm(w.path("issues/1"), nil).AssertForbidden()
	w.as(w.owner).Get(w.path("issues/1")).AssertSee("Move to trash")
	w.as(w.owner).DeleteForm(w.path("issues/1"), nil).
		AssertRedirect(w.path("")).
		AssertSessionHas("status", "WEB-1 moved to the trash.")
	anetostest.AssertSoftDeleted[models.Issue](w.app, models.IssueCols.ID.Eq(issue.ID))
	w.as(w.owner).Get(w.path("issues/1")).AssertNotFound()
}

func TestComments(t *testing.T) {
	w := newWorld(t)
	w.issue(func(i *models.Issue) { i.AssigneeID = &w.member.ID }) // by Olivia, for Max

	// A viewer comments: the author and the assignee are told.
	viewer := w.as(w.viewer)
	viewer.Get(w.path("issues/1")).AssertSee("Add a comment")
	viewer.PostForm(w.path("issues/1/comments"), form("body", "")).AssertValidationErrors("body")
	viewer.PostForm(w.path("issues/1/comments"), form("body", "Same here on Firefox.")).
		AssertRedirect(w.path("issues/1")).
		Follow().
		AssertSee("Vera", "Same here on Firefox.")
	anetostest.AssertDispatched(w.app, func(j jobs.NotifyComment) bool { return true })
	sent := anetostest.Mailables[mailers.Commented](w.app)
	if got := recipients(sent); got != w.owner.Email+","+w.member.Email {
		t.Errorf("first comment told %s", got)
	}

	// With htmx, the comment comes back alone. The earlier commenter is
	// told now, the comment's author isn't; nor someone who turned the
	// emails off.
	if _, err := db.Query[models.User](w.app.Context()).Where(models.UserCols.ID.Eq(w.owner.ID)).Update(models.UserCols.Notify.Set(false)); err != nil {
		t.Fatal(err)
	}
	member := w.as(w.member)
	member.WithHeader("HX-Request", "true")
	member.PostForm(w.path("issues/1/comments"), form("body", "Fixed in <b>main</b>.")).
		AssertOK().
		AssertSee(`id="comment-`, "Max", "Fixed in &lt;b&gt;main&lt;/b&gt;.").
		AssertDontSee("<html")
	member.WithHeader("HX-Request", "")
	sent = anetostest.Mailables[mailers.Commented](w.app)
	if got := recipients(sent[2:]); got != w.viewer.Email || !strings.Contains(sent[2].Body, "Fixed in <b>main</b>.") {
		t.Errorf("second comment told %s: %+v", got, sent[2:])
	}
	w.as(w.outsider).PostForm(w.path("issues/1/comments"), form("body", "Hi")).AssertNotFound()
}

func recipients(sent []mailers.Commented) string {
	var to []string
	for _, m := range sent {
		to = append(to, m.Email)
	}
	return strings.Join(to, ",")
}

func TestAttachments(t *testing.T) {
	w := newWorld(t)
	w.issue()
	member := w.as(w.member)
	member.Get(w.path("issues/1")).AssertSee("No files.", `enctype="multipart/form-data"`)
	member.PostMultipart(w.path("issues/1/files"), nil, anetostest.Upload{Field: "file", Filename: "trace.txt", Content: []byte("panic: oops")}).
		AssertRedirect(w.path("issues/1")).
		AssertSessionHas("status", "trace.txt attached.")
	a, err := db.Query[models.Attachment](w.app.Context()).First()
	if err != nil || a.Name != "trace.txt" || a.Size != 11 || !strings.HasPrefix(a.Path, "attachments/WEB/") {
		t.Fatalf("attachment: %+v, %v", a, err)
	}
	w.app.Disk().AssertContent(a.Path, "panic: oops")
	member.Get(w.path("issues/1")).AssertSee("trace.txt", "11 B")

	// Anyone in the project downloads it; no one else, not even through
	// another project's URL.
	res := w.as(w.viewer).Get(w.path("files/"+id(a.ID))).AssertOK().AssertHeader("Content-Disposition", `attachment; filename=trace.txt`)
	if res.Text() != "panic: oops" {
		t.Errorf("download: %q", res.Text())
	}
	w.as(w.outsider).Get(w.path("files/" + id(a.ID))).AssertNotFound()
	other := anetostest.Create(w.app, projectWithKey("API"))
	assign(t, w, w.outsider, other, "owner")
	w.as(w.outsider).Get("/p/API/files/" + id(a.ID)).AssertNotFound()

	// The uploader or an editor deletes it, from the disk too.
	w.as(w.viewer).DeleteForm(w.path("files/"+id(a.ID)), nil).AssertForbidden()
	w.as(w.member).DeleteForm(w.path("files/"+id(a.ID)), nil).AssertRedirect(w.path("issues/1"))
	w.app.Disk().AssertMissing(a.Path)
}

func TestSearch(t *testing.T) {
	w := newSearchWorld(t)
	w.issue(func(i *models.Issue) { i.Title, i.Body = "Checkout fails", "The payment form spins forever." })
	w.issue(func(i *models.Issue) { i.Title = "Typo in footer" })
	other := anetostest.Create(w.app, projectWithKey("API"))
	anetostest.Create(w.app, issueIn(other, w.owner, func(i *models.Issue) { i.Title = "Payment webhook retries" }))

	w.as(w.member).Get("/search?q=payment").AssertOK().
		AssertSee("Checkout fails", "WEB-1").
		AssertDontSee("Payment webhook", "Typo in footer") // not a member of API
	assign(t, w, w.member, other, "viewer")
	w.as(w.member).Get("/search?q=payment").AssertSee("Checkout fails", "Payment webhook retries")
	w.as(w.member).Get("/search?q=zebra").AssertSee("No issues match “zebra”.")
	w.as(w.outsider).Get("/search?q=payment").AssertDontSee("Checkout fails")
}

func TestDashboard(t *testing.T) {
	w := newWorld(t)
	w.issue(func(i *models.Issue) { i.Title, i.AssigneeID, i.Priority = "Fix the build", &w.member.ID, "urgent" })
	w.issue(func(i *models.Issue) { i.Title, i.AssigneeID, i.Status = "Done already", &w.member.ID, models.Closed })
	w.as(w.member).Get("/dashboard").AssertOK().AssertSee("Assigned to you", "Fix the build", "WEB-1", "Urgent").AssertDontSee("Done already")
	w.as(w.viewer).Get("/dashboard").AssertSee("Nothing assigned to you is open.")
}

func TestNotifySetting(t *testing.T) {
	w := newWorld(t)
	app := w.as(w.owner)
	app.Get("/settings").AssertOK().AssertSee(`name="notify" value="1" checked`)
	app.PostForm("/settings/preferences", form("locale", "", "time_zone", "")).AssertRedirect("/settings")
	app.Get("/settings").AssertDontSee(`name="notify" value="1" checked`)
	app.PostForm("/settings/preferences", form("notify", "1")).AssertRedirect("/settings")
	anetostest.AssertDatabaseHas[models.User](w.app, models.UserCols.ID.Eq(w.owner.ID), models.UserCols.Notify.Eq(true))
}

// The emails go only to users who still see the project, aren't
// disabled, and want them: checked when the job runs, which may be
// later than the change.
func TestNotifyChecksWhenSending(t *testing.T) {
	w := newWorld(t)
	issue := w.issue(func(i *models.Issue) { i.AssigneeID = &w.member.ID })
	if err := removeRoles(w, w.member); err != nil {
		t.Fatal(err)
	}
	job := jobs.NotifyAssigned{IssueID: issue.ID, AssigneeID: w.member.ID, ActorID: w.owner.ID}
	if err := job.Handle(w.app.Context()); err != nil {
		t.Fatal(err)
	}
	anetostest.AssertMailNotSent(w.app, func(m mailers.Assigned) bool { return true })

	// A disabled author isn't told about comments.
	if _, err := db.Query[models.User](w.app.Context()).Where(models.UserCols.ID.Eq(w.owner.ID)).Update(models.UserCols.DisabledAt.Set(&issue.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	w.as(w.viewer).PostForm(w.path("issues/1/comments"), form("body", "Ping")).AssertRedirect(w.path("issues/1"))
	anetostest.AssertMailNotSent(w.app, func(m mailers.Commented) bool { return m.Email == w.owner.Email })
}
