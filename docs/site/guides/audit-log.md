---
title: Keep an audit log
since: v0.3.0
group: "Accounts and security"
weight: 308
---

# Keep an audit log

Record who created, changed, deleted and restored the rows of your
models, field by field, with package `audit`: for data governance, for
support, and for "who changed this?".

## Before you start

- [Connect to a database](database.md) and run your
  [migrations](migrations.md) with `migrate.New`.
- To record *who*, set up [authentication](authentication.md): changes
  are attributed to the logged-in user. Without it, they are attributed
  to the system.

The code here comes from [`examples/audit`](../../../examples/audit), a
JSON API of documents.

## Steps

### 1. Add the log's tables

Add `audit.Migrations()` to your migration sets. It creates three tables:
`audit_log` (one entry per change of one row, or per event you record),
`audit_bulk` (one entry per write to many rows) and `audit_bulk_items`
(the key of every row a bulk write touched).

```go
// illustrative
sets := []*migrate.Set{Migrations, auth.Migrations(), audit.Migrations()}
if _, err := migrate.New(app, sets); err != nil {
	return nil, err
}
```

### 2. Track your models

Create the app's log after `db.Connect`, and name the models it tracks:

```go
trail, err := audit.New(app) // after db.Connect
if err != nil {
	return nil, err
}
if err := audit.Track[Document](trail, audit.Except("view_count")); err != nil {
	return nil, err
}
// Documents deleted more than 30 days ago go for good: db:prune-trashed.
if err := db.Prunable[Document](app, 30*24*time.Hour); err != nil {
	return nil, err
}
```

(Copied from [`examples/audit/main.go`](../../../examples/audit/main.go), region `setup`.)

From now on, every `db.Create`, `db.Update`, `db.Save`, `db.Delete`,
`db.ForceDelete` and `db.Restore` of a `Document` writes an entry. The
model needs nothing special:

```go
// Document is what the log tracks. Deleting one soft-deletes it; its
// slug is unique among the documents that aren't deleted.
type Document struct {
	db.Model
	db.SoftDeletes
	OwnerID    int64  `db:"owner_id" json:"owner_id"`
	Slug       string `db:"slug" json:"slug"`
	Title      string `db:"title" json:"title"`
	Body       string `db:"body" json:"body"`
	Status     string `db:"status" json:"status"`    // draft, published, archived
	ShareToken string `db:"share_token" json:"-"`    // redacted in the log: its name says token
	Views      int    `db:"view_count" json:"views"` // left out of the log: audit.Except
}
```

(Copied from [`examples/audit/models.go`](../../../examples/audit/models.go), region `model`.)

Choose what each model's entries keep with options of `Track`:

| Option | Effect |
|---|---|
| `audit.Except("view_count")` | The column is left out entirely: an update that changes only it isn't logged. Use it for counters and caches. |
| `audit.Redact("diagnosis")` | The log records that the column changed, as `"[redacted]"`, not its values. |
| `audit.Reveal("token_count")` | Columns whose names say they hold a secret (`password`, `secret`, `token`, `credential`, an API, private or session key, a recovery code, an OTP) are redacted by default; this shows one. |

### 3. Change rows as usual

Nothing changes in your handlers. An update records the fields that
changed, from what to what, and who changed them:

```go
// UpdateDocument changes a document. The log records the fields that
// changed, from what to what, and who changed them.
func (Handlers) UpdateDocument(c *web.Ctx, in DocumentChanges) (web.Responder, error) {
	d, err := db.Find[Document](c, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		d.Title = *in.Title
	}
	if in.Body != nil {
		d.Body = *in.Body
	}
	if in.Status != nil {
		d.Status = *in.Status
	}
	if err := db.Update(c, &d); err != nil {
		return nil, err
	}
	return web.JSON(http.StatusOK, d), nil
}
```

(Copied from [`examples/audit/main.go`](../../../examples/audit/main.go), region `update`.)

The entry for that update holds:

```json
{
  "action": "updated",
  "actor_type": "user", "actor_id": "7",
  "subject_type": "documents", "subject_id": "42",
  "changes": {"old": {"title": "Plan"}, "new": {"title": "The plan"}},
  "via_kind": "request", "via_name": "PATCH /api/documents/42",
  "request_id": "b6f1…"
}
```

The actions are `created` (with the new values), `updated` (the changed
columns' old and new values; an update that changes nothing but
timestamps isn't logged), `deleted` (soft-deleted: the row is still
there), `restored`, and `force_deleted` (gone from the database, by
`ForceDelete` or by `Delete` on a model without soft deletes: the entry
keeps the values the row had).

### 4. Write many rows at once

A query's `Update`, `Delete`, `ForceDelete` or `Restore`, `CreateMany`
and `Upsert` write one **bulk entry** each, however many rows they touch:

```go
// Archive archives the user's published documents in one statement: one
// bulk entry in the log, with every document's key.
func (Handlers) Archive(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	n, err := db.Query[Document](c).
		Where(colOwnerID.Eq(u.ID), colStatus.Eq("published")).
		Update(colStatus.Set("archived"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]int64{"archived": n})
}
```

(Copied from [`examples/audit/main.go`](../../../examples/audit/main.go), region `archive`.)

A bulk entry (`audit.BulkOp`) keeps the condition and its arguments, the
assignments, the number of rows, the values the rows had before (up to
`AUDIT_BULK_MAX_VALUES` rows, 10,000 by default, with a flag saying
whether it kept them all), and the key of **every** row in
`audit_bulk_items`. Deleting a thousand posts reads as one event, and
"who deleted post 42?" still has an answer.

If the condition names a column the log hides or redacts
(`Where(apiToken.Eq(t))`), its arguments are stored as `"[redacted]"`.
Values you write into the SQL itself (`WhereRaw("token = 'abc'")`) are
stored as written: pass them as arguments.

On a tracked model, `Upsert` needs a value in every conflict column: a
NULL, or a zero generated key, never conflicts, and the log couldn't find
the row again, so it is refused.

### 5. Show a row's history

`audit.History` returns a row's events, newest first: its own entries
and the bulk writes that touched it.

```go
// History returns what happened to a document, newest first: its own
// entries and the bulk writes that touched it.
func (Handlers) History(c *web.Ctx, in HistoryQuery) (web.Responder, error) {
	d, err := db.Query[Document](c).WithTrashed().Find(in.ID)
	if err != nil {
		return nil, err
	}
	subject, err := audit.SubjectOf(&d)
	if err != nil {
		return nil, err
	}
	events, next, err := audit.History(c, subject, 50, in.Cursor)
	if err != nil {
		return nil, err
	}
	out := make([]Event, len(events))
	for i, e := range events {
		out[i] = Event{At: e.At(), Actor: e.Actor().String(), Action: e.Action()}
		if e.Entry != nil {
			out[i].Via = e.Entry.ViaName
			out[i].Changes = &e.Entry.Changes
		} else {
			out[i].Via = e.Bulk.ViaName
			out[i].Rows = e.Bulk.RowCount
		}
	}
	return web.JSON(http.StatusOK, HistoryPage{out, next}), nil
}
```

(Copied from [`examples/audit/main.go`](../../../examples/audit/main.go), region `history`.)

`History` also returns a cursor: pass it back for the next (older) page;
it is `""` after the last one. For anything else, query the tables like
any model: `db.Query[audit.Entry]`, `db.Query[audit.BulkOp]`.

> **Warning:** the log is as sensitive as the data it describes. Check
> permissions before showing it, as you would for the rows themselves.

### 6. Record your own events

Not everything worth recording is a write: an export, a download, a
failed permission check. `audit.Record` adds an entry with the same
actor and operation, and your details as JSON:

```go
// Export returns the user's documents, and records that they did: an
// event of the app's own, not a write.
func (Handlers) Export(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	docs, err := db.Query[Document](c).Where(colOwnerID.Eq(u.ID)).Get()
	if err != nil {
		return err
	}
	err = audit.Record(c, "documents.exported", audit.Subject{Type: "users", ID: u.AuthID()},
		map[string]any{"documents": len(docs)})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, docs)
}
```

(Copied from [`examples/audit/main.go`](../../../examples/audit/main.go), region `export`.)

Code that records events only in apps that keep a log, such as a
package of its own, checks `audit.Enabled(ctx)` first: `Record` fails
without one. `audit.Tracked(ctx, table)` says whether the log tracks a
table.

### 7. Name who acts when it isn't a user

Changes are attributed, in order, to:

1. the actor you set with `audit.WithActor`;
2. the logged-in user, or the one a job acts as (`auth.WithUser`); while
   someone impersonates a user (`auth.Impersonate`, the admin's
   "Impersonate"), that someone, with the user in the entry's `ActingAs`
   (`"user:42"`; jobs dispatched meanwhile carry that someone alone);
3. in a queue job or an async event listener, the actor of the work that
   started it: a job a user's request dispatched is attributed to that
   user (dispatching loads the logged-in user, once per request, to know
   who; if that fails, the job's tracked writes fail rather than guess);
4. otherwise `system` (commands, scheduled tasks).

A webhook's handler names its sender:

```go
// illustrative
ctx := audit.WithActor(c, audit.Actor{Type: "service", ID: "stripe"})
```

If the logged-in user can't be loaded (the database is down), the write
fails: the log doesn't guess who did it.

### 8. Keep entries for as long as you must, and no longer

- **Retention:** set `AUDIT_RETENTION_DAYS` and run `audit:prune` daily
  (or `audit.Prune` in a [scheduled task](scheduling.md)). It deletes
  older entries in batches, and records that it did; with the default of
  0 it keeps everything.
- **Client IP addresses:** off by default. `AUDIT_IP=masked` keeps
  `203.0.113.0` for `203.0.113.77` (an IPv6 address to its /48);
  `AUDIT_IP=full` keeps it whole. An IP address is personal data under
  the GDPR, so decide, and say so in your privacy notice.
- **Erasure requests:** `audit:anonymize user 42` (or `audit.Anonymize`)
  replaces the user with `erased` in every entry, as the actor and as the
  user someone impersonated, and drops the IP addresses of the entries they
  made. Entries *about* the person's own rows keep their values:
  delete those with `db.Query[audit.Entry]` if the request covers them.

## How it works

`audit.Track` asks the `db` package to watch the model's table
(`DB.Watch`). Every write the `db` package makes to a watched table then
runs in a transaction (a savepoint, inside one you opened), and the log
writes its entry **in that transaction**, after the write:

- a committed change always has its entry, and a rolled-back one never
  does;
- an update first reads the row `FOR UPDATE`, so "before" is what the
  database held, not what your handler loaded earlier;
- a bulk write first selects the matching rows' keys `FOR UPDATE`, then
  writes in chunks of 1,000 keys and fails if a chunk changes fewer rows
  than it selected, so the entry lists exactly the rows written.

The cost falls only on tracked models: a transaction, one read and one
insert per single-row write; a read of the keys and one insert per
1,000 rows for a bulk write. A bulk update of only `Except`ed columns
isn't logged, but still pays for the read.

> **Warning:** the log sees what goes through the `db` package. Raw SQL
> (`db.Exec`), pivot writes (`db.Attach`, `db.Sync`) and the database's
> own `ON DELETE CASCADE` aren't recorded.

## Soft deletes that play well with the log

Two things help models with `db.SoftDeletes`:

**Unique among live rows.** A soft-deleted row keeps its values, so a
deleted user's email would block a new sign-up with a plain unique
index. `UniqueWithoutTrashed` makes the index ignore deleted rows, and the
`unique_without_trashed` validation rule checks the same:

```go
return s.Create("documents", func(t *migrate.Table) {
	t.ID()
	t.ForeignID("owner_id").References("users")
	t.String("slug", 100).UniqueWithoutTrashed() // PostgreSQL and SQLite
	t.String("title", 255)
	t.Text("body")
	t.String("status", 20).Default("draft")
	t.String("share_token", 64)
	t.Integer("view_count").Default(0)
	t.Timestamps()
	t.SoftDeletes()
})
```

(Copied from [`examples/audit/models.go`](../../../examples/audit/models.go), region `migration`.)

```go
// illustrative
Slug string `json:"slug" validate:"required|unique_without_trashed:documents,slug"`
```

MySQL and MariaDB have no partial indexes, so `UniqueWithoutTrashed` fails there
with the reason; use `Unique`, or a generated column that is `NULL` for
deleted rows with a unique index on it.

**Pruning.** `db.Prunable[Document](app, 30*24*time.Hour)` (step 2)
registers the model; `db:prune-trashed` (or `db.PruneAllTrashed` in a
scheduled task) then deletes for good the rows deleted more than 30 days
ago, 1,000 per transaction. On a tracked model, each batch is a bulk
`force_deleted` entry, with the rows' values, by `system` when the
command or a scheduled task runs it.

## Testing it

Entries are written in your tests too, in the test's transaction. Check
them through the API or with `db.Query[audit.Entry]`:

```go
app := anetostest.New(t, setup)
ada, token := newUser(t, app, "Ada")
api := as(app, token)

var doc Document
api.PostJSON("/api/documents", NewDocument{Slug: "plan", Title: "Plan"}).AssertCreated().JSON(&doc)
path := fmt.Sprintf("/api/documents/%d", doc.ID)
api.PatchJSON(path, map[string]string{"title": "The plan", "status": "published"}).AssertOK()
api.GetJSON(path).AssertOK() // a view: not in the log
api.PostJSON("/api/documents/archive", nil).AssertOK().AssertJSONPath("archived", float64(1))
api.Delete(path).AssertNoContent()
api.PostJSON(path+"/restore", nil).AssertOK()

var page HistoryPage
api.GetJSON(path + "/history").AssertOK().JSON(&page)
events := page.Events
var actions []string
for _, e := range events {
	actions = append(actions, e.Action)
	if e.Actor != "user:"+ada.AuthID() {
		t.Errorf("%s by %s", e.Action, e.Actor)
	}
}
want := []string{audit.Restored, audit.Deleted, audit.Updated, audit.Updated, audit.Created}
if strings.Join(actions, " ") != strings.Join(want, " ") {
	t.Fatalf("history: %v, want %v", actions, want)
}
```

(Copied from [`examples/audit/main_test.go`](../../../examples/audit/main_test.go), region `test-history`.)

Use `app.Travel` to test retention and pruning.

## Common problems

- **`audit: New needs the app's database`**: call `db.Connect` before
  `audit.New`.
- **`audit: … is tracked already`**: a table is tracked once per
  database; call `Track` once, at setup.
- **`audit: a key of N bytes is longer than the log keeps`**: entries
  keep keys of up to 255 bytes and actor IDs of up to 100.
- **`db: … is watched, so its model … needs a primary key`**: tracked
  models need one, to name the rows in entries.
- **No entry for an update**: only timestamps or `Except`ed columns
  changed, or the change was made with `db.Exec`.
- **`a watched bulk write changed N of the M rows it selected`**: the
  rows changed between the read and the write, which the lock should
  prevent; the write was rolled back. Retry it.
- **The log grows fast**: leave counters out with `Except`, and set
  `AUDIT_RETENTION_DAYS`.
- **A big bulk write fails on MySQL with `max_allowed_packet`**: its
  entry keeps the values of up to `AUDIT_BULK_MAX_VALUES` rows in one
  JSON value; lower the setting, or raise the server's limit.
- **A hook hangs once its model is tracked**: hooks now run inside the
  write's transaction, which holds the row's lock. A hook that writes
  outside it (`db.WithoutTx`) waits for that lock; on SQLite with one
  connection it waits forever. Do the work in the hook's context, or
  after the commit (`db.AfterCommit`).
- **`Upsert` fails on a `UniqueWithoutTrashed` index**: PostgreSQL and SQLite only
  take a partial index as the conflict target with its `WHERE`, which
  `Upsert` doesn't write. Use `Create` and `Update`, or a plain `Unique`
  index.

## Next steps

- [Roles and permissions](roles-and-permissions.md), to decide who may
  read the log.
- [Transactions](transactions.md): entries join yours.
- [Scheduling](scheduling.md), for `audit.Prune` and
  `db.PruneAllTrashed`.
- [Configuration reference](../reference/configuration.md#audit-log):
  `AUDIT_*`.
