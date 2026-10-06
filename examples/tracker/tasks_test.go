// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/mailers"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/app/tasks"
)

func TestDigest(t *testing.T) {
	w := newWorld(t)
	w.issue(func(i *models.Issue) { i.Title, i.AssigneeID = "Normal one", &w.member.ID })
	w.issue(func(i *models.Issue) { i.Title, i.AssigneeID, i.Priority = "Urgent one", &w.member.ID, "urgent" })
	w.issue(func(i *models.Issue) { i.Title, i.AssigneeID, i.Status = "Closed one", &w.member.ID, models.Closed })
	w.issue(func(i *models.Issue) { i.Title, i.AssigneeID = "Owner's", &w.owner.ID })
	w.issue(func(i *models.Issue) { i.Title = "Nobody's" })
	// The owner turned the emails off.
	if _, err := db.Query[models.User](w.app.Context()).Where(models.UserCols.ID.Eq(w.owner.ID)).Update(models.UserCols.Notify.Set(false)); err != nil {
		t.Fatal(err)
	}

	// The task is scheduled on weekday mornings; run it now.
	if err := tasks.SendDigests(w.app.Context()); err != nil {
		t.Fatal(err)
	}
	sent := anetostest.Mailables[mailers.Digest](w.app)
	if len(sent) != 1 || sent[0].Email != w.member.Email || len(sent[0].Issues) != 2 ||
		sent[0].Issues[0].Title != "Urgent one" || sent[0].Issues[1].Ref != "WEB-1" {
		t.Fatalf("digests: %+v", sent)
	}
	anetostest.AssertMailQueued(w.app, func(m mailers.Digest) bool { return m.Email == w.member.Email })

	// Not for projects the user left.
	if err := removeRoles(w, w.member); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SendDigests(w.app.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(anetostest.Mailables[mailers.Digest](w.app)); n != 1 {
		t.Errorf("after leaving: %d digests", n)
	}
}

func TestSchedule(t *testing.T) {
	w := newWorld(t)
	var out, errOut bytes.Buffer
	if code := w.app.ExecuteArgs(w.app.Context(), []string{"schedule:list"}, &out, &errOut); code != 0 ||
		!strings.Contains(out.String(), "send-digests") || !strings.Contains(out.String(), "0 8 * * 1-5") {
		t.Errorf("schedule:list: %d\n%s%s", code, &out, &errOut)
	}
}

// removeRoles takes u's roles in the project away.
func removeRoles(w *world, u models.User) error {
	return rbac.Sync(w.app.Context(), u.AuthID(), access.Project(w.project.Key))
}
