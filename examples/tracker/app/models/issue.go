// SPDX-License-Identifier: Apache-2.0

package models

import (
	"context"
	"errors"
	"strconv"
	"time"

	"anetos.dev/anetos/db"
)

// The states of an issue.
const (
	Open   = "open"
	Closed = "closed"
)

// Priorities are an issue's priorities, from least to most pressing.
var Priorities = []string{"low", "normal", "high", "urgent"}

// Issue is a bug, a task or an idea in a project, numbered in it
// (WEB-12). Deleting one moves it to the trash.
type Issue struct {
	db.Model
	db.SoftDeletes
	ProjectID  int64      `db:"project_id" json:"-"`
	Number     int        `db:"number" json:"number"`
	Title      string     `db:"title" json:"title"`
	Body       string     `db:"body" json:"body"`
	Status     string     `db:"status" json:"status"`     // Open or Closed
	Priority   string     `db:"priority" json:"priority"` // one of Priorities
	AuthorID   int64      `db:"author_id" json:"-"`
	AssigneeID *int64     `db:"assignee_id" json:"-"`
	ClosedAt   *time.Time `db:"closed_at" json:"closed_at"`

	Project     *Project     `rel:"belongs_to" json:"-"`
	Author      *User        `rel:"belongs_to" json:"author,omitzero"`
	Assignee    *User        `rel:"belongs_to" json:"assignee,omitzero"`
	Labels      []Label      `rel:"many_to_many" json:"labels,omitzero"` // issue_label
	Comments    []Comment    `rel:"has_many" json:"-"`
	Attachments []Attachment `rel:"has_many" json:"-"`
}

// BeforeCreate numbers a new issue after its project's newest one
// (db.BeforeCreateHook). The project's row stays locked until the
// transaction ends, so issues opened at once get different numbers.
func (i *Issue) BeforeCreate(ctx context.Context) error {
	if i.Number != 0 {
		return nil
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		project := db.Query[Project](ctx).Where(ProjectCols.ID.Eq(i.ProjectID))
		if _, err := project.Update(ProjectCols.LastNumber.SetRaw("last_number + 1")); err != nil {
			return err
		}
		n, err := db.Pluck(project, ProjectCols.LastNumber)
		if err != nil {
			return err
		}
		if len(n) == 0 {
			return errors.New("issue: no project with the ID")
		}
		i.Number = n[0]
		return nil
	})
}

// Ref is the issue's name in its project: WEB-12.
func (i Issue) Ref(projectKey string) string { return projectKey + "-" + strconv.Itoa(i.Number) }

// IsOpen reports whether the issue is open.
func (i Issue) IsOpen() bool { return i.Status == Open }

// Label tags issues of a project: bug, design.
type Label struct {
	db.Model
	ProjectID int64  `db:"project_id" json:"-"`
	Name      string `db:"name" json:"name"`
	Color     string `db:"color" json:"color"` // #rrggbb
}

// Comment is a reply on an issue.
type Comment struct {
	db.Model
	IssueID  int64  `db:"issue_id" json:"-"`
	AuthorID int64  `db:"author_id" json:"-"`
	Body     string `db:"body" json:"body"`

	Author *User `rel:"belongs_to" json:"author,omitzero"`
}

// Attachment is a file attached to an issue, stored on the app's disk
// under a name of its own (Path); Name is the uploaded file's.
type Attachment struct {
	db.Model
	IssueID     int64  `db:"issue_id" json:"-"`
	UploaderID  int64  `db:"uploader_id" json:"-"`
	Name        string `db:"name" json:"name"`
	Path        string `db:"path" json:"-"`
	Size        int64  `db:"size" json:"size"`
	ContentType string `db:"content_type" json:"content_type"`

	Uploader *User `rel:"belongs_to" json:"uploader,omitzero"`
}
