// SPDX-License-Identifier: Apache-2.0

package views

import (
	"time"

	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/tracker/app/models"
)

// ProjectRow is a project in the list, with its issue counts.
type ProjectRow struct {
	Project    models.Project
	Open, Done int64
}

// Filter is how a project's issue list is filtered and sorted: the
// query parameters of /p/{project}.
type Filter struct {
	Status   string // "open" (default), "closed" or "all"
	Label    string // a label's name
	Assignee string // "me", "none", or a user's ID
	Q        string // search words
	Sort     string // "newest" (default), "oldest", "updated" or "priority"
}

// IssueList is a project's page: its issues, filtered.
type IssueList struct {
	Project   models.Project
	Page      db.Page[models.Issue]
	Filter    Filter
	Labels    []models.Label
	Assignees []models.User
	CanCreate bool // may open issues
	CanManage bool // may change the project's settings
}

// Member is a user with their role in a project.
type Member struct {
	User models.User
	Role string
}

// ProjectSettings is a project's settings page.
type ProjectSettings struct {
	Project models.Project
	Labels  []models.Label
	Members []Member
	Roles   []string // the roles owners can give
	MeID    int64
}

// IssueForm opens or edits an issue.
type IssueForm struct {
	Project   models.Project
	Issue     models.Issue // ID 0: a new one
	Labels    []models.Label
	Assignees []models.User
}

// Activity is a change in an issue's history, from the audit log.
type Activity struct {
	At      time.Time
	Who     string
	Field   string // "status", "assignee_id", "title", "priority", or "" for the issue's creation
	Old     string
	New     string
	Deleted bool
}

// IssuePage shows an issue with its comments, files and history.
type IssuePage struct {
	Project     models.Project
	Issue       models.Issue
	Comments    []models.Comment
	Attachments []models.Attachment
	Activity    []Activity
	Assignees   []models.User
	CanEdit     bool // edit, close, assign
	CanComment  bool
	CanAttach   bool
	CanDelete   bool // move to the trash: owners
}

// SearchResult is an issue found by the search, with its project.
type SearchResult struct {
	Project models.Project
	Issue   models.Issue
}

// AssignedIssue is an open issue assigned to the user, on the dashboard.
type AssignedIssue struct {
	ProjectKey string
	Issue      models.Issue
}
