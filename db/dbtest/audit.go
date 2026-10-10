// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/validate"
)

func init() {
	extra = append(extra,
		test{"Audit", testAudit},
		test{"AuditBulk", testAuditBulk},
		test{"WatchedWrites", testWatchedWrites},
		test{"UniqueLiveAndPrune", testUniqueLiveAndPrune},
	)
}

// stAudited is a tracked model: soft deletes, a JSON column, a column
// left out (score), one redacted by option (notes) and one by name
// (api_token).
type stAudited struct {
	db.Model
	db.SoftDeletes
	Code     string            `db:"code"`
	Title    string            `db:"title"`
	Views    int               `db:"views"`
	Score    int               `db:"score"`
	Notes    string            `db:"notes"`
	APIToken string            `db:"api_token"`
	Meta     map[string]string `db:"meta,json"`
}

// TableName implements db.Tabler.
func (stAudited) TableName() string { return "st_audited" }

var (
	auditViews = db.Col[int]("views")
	auditScore = db.Col[int]("score")
)

// auditSetup creates the log's tables and st_audited, and an app whose
// log tracks stAudited (once per database: watchers stay).
func auditSetup(t *testing.T, ctx context.Context, env config.Map) (context.Context, *anetos.App) {
	t.Helper()
	set := migrate.NewSet("st_audit")
	set.AddFunc("2026_10_06_000000_create_st_audited", func(s *migrate.Schema) error {
		return s.Create("st_audited", func(t *migrate.Table) {
			t.ID()
			t.String("code", 50).Unique()
			t.String("title", 255)
			t.Integer("views").Default(0)
			t.Integer("score").Default(0)
			t.Text("notes")
			t.String("api_token", 100)
			t.JSON("meta").Nullable()
			t.Timestamps()
			t.SoftDeletes()
		})
	}, func(s *migrate.Schema) error { return s.Drop("st_audited") })
	r, err := migrate.NewRunner(d(ctx), []*migrate.Set{audit.Migrations(), set}, migrate.WithTable("st_audit_migrations"))
	check(t, err)
	_, err = r.Up(ctx)
	check(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		for _, table := range []string{"audit_bulk_items", "audit_bulk", "audit_log", "st_audited", "st_audit_migrations"} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table)
		}
	})
	src := config.Map{"APP_ENV": "testing"}
	maps.Copy(src, env)
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	anetos.Provide(app, d(ctx))
	trail, err := audit.New(app)
	check(t, err)
	if !auditTracked[d(ctx)] {
		check(t, audit.Track[stAudited](trail, audit.Except("score"), audit.Redact("notes")))
		auditTracked[d(ctx)] = true
	} else if err := audit.Track[stAudited](trail); err == nil {
		// Watchers stay with the database: the first app's tracking
		// (with AUDIT_BULK_MAX_VALUES=3) goes on, and a second is refused.
		t.Error("a table was tracked twice")
	}
	return app.Context(ctx), app
}

// auditEnv are the settings of the audit tests' apps.
var auditEnv = config.Map{"AUDIT_BULK_MAX_VALUES": "3", "AUDIT_RETENTION_DAYS": "30"}

var auditTracked = map[*db.DB]bool{}

func entries(t *testing.T, ctx context.Context) []audit.Entry {
	t.Helper()
	es, err := db.Query[audit.Entry](ctx).OrderBy(db.C("id").Asc()).Get()
	check(t, err)
	return es
}

func testAudit(t *testing.T, ctx context.Context) {
	ctx, app := auditSetup(t, ctx, auditEnv)

	// Create: every tracked column's new value, secrets redacted.
	p := stAudited{Code: "a", Title: "First", Views: 1, Score: 9, Notes: "private", APIToken: "tok", Meta: map[string]string{"k": "v"}}
	check(t, db.Create(ctx, &p))
	es := entries(t, ctx)
	if len(es) != 1 {
		t.Fatalf("after Create: %d entries", len(es))
	}
	e := es[0]
	if e.Action != audit.Created || e.SubjectType != "st_audited" || e.SubjectID != fmt.Sprint(p.ID) || e.Actor() != audit.System || e.OccurredAt.IsZero() {
		t.Errorf("created entry: %+v", e)
	}
	if e.Changes.New["title"] != "First" || e.Changes.New["notes"] != audit.Redacted || e.Changes.New["api_token"] != audit.Redacted ||
		fmt.Sprint(e.Changes.New["views"]) != "1" || fmt.Sprint(e.Changes.New["meta"]) != "map[k:v]" || len(e.Changes.Old) != 0 {
		t.Errorf("created changes: %+v", e.Changes)
	}
	if _, ok := e.Changes.New["score"]; ok {
		t.Error("the excepted column score was recorded")
	}

	// Update: only what changed, from what to what.
	p.Title, p.Score = "Second", 10
	check(t, db.Update(ctx, &p))
	es = entries(t, ctx)
	if len(es) != 2 || es[1].Action != audit.Updated || es[1].Changes.Old["title"] != "First" || es[1].Changes.New["title"] != "Second" ||
		len(es[1].Changes.Fields()) != 1 {
		t.Fatalf("update entry: %+v", es[len(es)-1])
	}
	// Nothing tracked changed: no entry.
	p.Score = 11
	check(t, db.Update(ctx, &p))
	check(t, db.Update(ctx, &p))
	// A redacted column: changed, without its values.
	p.Notes = "still private"
	check(t, db.Update(ctx, &p))
	es = entries(t, ctx)
	if len(es) != 3 || es[2].Changes.Old["notes"] != audit.Redacted || es[2].Changes.New["notes"] != audit.Redacted {
		t.Fatalf("redacted update: %d entries, last %+v", len(es), es[len(es)-1].Changes)
	}

	// A stale copy, soft-deleted by someone else since it was loaded:
	// Update doesn't write deleted_at, so the entry doesn't claim a
	// restore.
	stale := p
	other := p
	check(t, db.Delete(ctx, &other))
	stale.Title = "Stale"
	check(t, db.Update(ctx, &stale))
	es = entries(t, ctx)
	if last := es[len(es)-1]; last.Action != audit.Updated || len(last.Changes.New) != 1 || last.Changes.New["title"] != "Stale" {
		t.Fatalf("update of a stale copy: %+v", last.Changes)
	}
	check(t, db.Restore(ctx, &other))
	stale.Title = "Second"
	check(t, db.Update(ctx, &stale))
	p = stale
	es = entries(t, ctx)
	if len(es) != 7 {
		t.Fatalf("%d entries before the rollback", len(es))
	}

	// Rolled back: neither the change nor its entry.
	errStop := errors.New("stop")
	err := db.Tx(ctx, func(ctx context.Context) error {
		p.Title = "Rolled back"
		if err := db.Update(ctx, &p); err != nil {
			return err
		}
		return errStop
	})
	if !errors.Is(err, errStop) || len(entries(t, ctx)) != 7 {
		t.Fatalf("rollback: %v, %d entries", err, len(entries(t, ctx)))
	}
	p.Title = "Second"

	// A named actor, in a unit of work.
	uctx, end := app.StartUnit(audit.WithActor(ctx, audit.Actor{Type: "service", ID: "stripe"}), anetos.Unit{Kind: "job", Name: "sync-prices"})
	p.Views = 2
	check(t, db.Update(uctx, &p))
	end()
	es = entries(t, ctx)
	if last := es[len(es)-1]; last.Actor() != (audit.Actor{Type: "service", ID: "stripe"}) || last.ViaKind != "job" || last.ViaName != "sync-prices" {
		t.Errorf("actor and unit: %+v", last)
	}

	// Soft delete, restore (twice: the second restores nothing), force
	// delete with the values the row had.
	check(t, db.Delete(ctx, &p))
	check(t, db.Restore(ctx, &p))
	check(t, db.Restore(ctx, &p))
	check(t, db.ForceDelete(ctx, &p))
	es = entries(t, ctx)
	var actions []string
	for _, e := range es[8:] {
		actions = append(actions, e.Action)
	}
	if !slices.Equal(actions, []string{audit.Deleted, audit.Restored, audit.ForceDeleted}) {
		t.Fatalf("actions %v", actions)
	}
	if last := es[len(es)-1]; last.Changes.Old["title"] != "Second" || last.Changes.Old["notes"] != audit.Redacted || len(last.Changes.New) != 0 {
		t.Errorf("force delete changes: %+v", last.Changes)
	}

	// The app's own events.
	subject, err := audit.SubjectOf(&p)
	check(t, err)
	check(t, audit.Record(ctx, "post.exported", subject, map[string]any{"format": "pdf"}))
	events, next, err := audit.History(ctx, subject, 100, "")
	check(t, err)
	if len(events) != len(es)+1 || events[0].Action() != "post.exported" || events[0].Entry.Properties["format"] != "pdf" ||
		events[len(events)-1].Action() != audit.Created || next != "" {
		t.Errorf("history: %d events (next %q), first %+v", len(events), next, events[0].Entry)
	}
	// Paging, with several events at one instant: every event once.
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i := range 3 {
		check(t, db.Create(ctx, &audit.Entry{OccurredAt: at, ActorType: "system", Action: fmt.Sprint("same.", i), SubjectType: subject.Type, SubjectID: subject.ID}))
	}
	same := audit.BulkOp{OccurredAt: at, ActorType: "system", Action: "same.bulk", SubjectType: subject.Type, RowCount: 1}
	check(t, db.Create(ctx, &same))
	check(t, db.Create(ctx, &audit.BulkItem{BulkID: same.ID, SubjectType: subject.Type, SubjectID: subject.ID}))
	all, _, err := audit.History(ctx, subject, 1000, "")
	check(t, err)
	var paged []string
	for cursor := ""; ; {
		page, next, err := audit.History(ctx, subject, 2, cursor)
		check(t, err)
		for _, ev := range page {
			paged = append(paged, fmt.Sprint(ev.Action(), ev.At().UnixMicro()))
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(paged) != len(all) || len(all) != len(es)+5 {
		t.Errorf("paging gave %d events of %d: %v", len(paged), len(all), paged)
	}
	if _, _, err := audit.History(ctx, subject, 2, "nonsense"); err == nil {
		t.Error("an invalid cursor was accepted")
	}

	// Erasure.
	n, err := audit.Anonymize(ctx, audit.Actor{Type: "service", ID: "stripe"})
	check(t, err)
	if n != 1 {
		t.Errorf("Anonymize changed %d entries", n)
	}
	if left, _ := db.Query[audit.Entry](ctx).Where(db.C("actor_id").Eq("stripe")).Count(); left != 0 {
		t.Errorf("%d entries still name the actor", left)
	}
}

func testAuditBulk(t *testing.T, ctx context.Context) {
	ctx, _ = auditSetup(t, ctx, auditEnv)
	bulks := func() []audit.BulkOp {
		bs, err := db.Query[audit.BulkOp](ctx).OrderBy(db.C("id").Asc()).Get()
		check(t, err)
		return bs
	}
	rows := make([]stAudited, 5)
	for i := range rows {
		rows[i] = stAudited{Code: fmt.Sprint("c", i), Title: fmt.Sprint("t", i), Views: i, Notes: "n", APIToken: "x"}
	}
	check(t, db.CreateMany(ctx, rows))
	bs := bulks()
	if len(bs) != 1 || bs[0].Action != audit.Created || bs[0].RowCount != 5 || len(bs[0].After) != 3 || bs[0].Complete {
		t.Fatalf("CreateMany: %+v", bs)
	}
	for _, r := range rows {
		if r.ID == 0 {
			t.Fatal("CreateMany on a tracked table didn't set the keys")
		}
	}
	if items, _ := db.Query[audit.BulkItem](ctx).Count(); items != 5 {
		t.Errorf("%d items", items)
	}
	if es := entries(t, ctx); len(es) != 0 {
		t.Errorf("bulk writes made %d single entries", len(es))
	}

	// A bulk update with an expression, on the rows with views >= 2.
	n, err := db.Query[stAudited](ctx).Where(auditViews.Gte(2)).Update(auditViews.SetRaw("views + ?", 10), auditScore.Set(1))
	check(t, err)
	bs = bulks()
	up := bs[len(bs)-1]
	if n != 3 || up.Action != audit.Updated || up.RowCount != 3 || up.Condition.SQL == "" || len(up.Before) != 3 || !up.Complete {
		t.Fatalf("bulk update (%d rows): %+v", n, up)
	}
	if _, ok := up.Assignments["score"]; ok {
		t.Error("the excepted column score is in the assignments")
	}
	if a, ok := up.Assignments["views"].(map[string]any); !ok || a["sql"] == "" {
		t.Errorf("the expression's assignment: %#v", up.Assignments["views"])
	}
	if _, ok := up.Before[0].Values["views"]; !ok || len(up.Before[0].Values) != 1 {
		t.Errorf("values before: %+v", up.Before[0])
	}

	// Soft delete, restore, force delete of the same rows; nothing
	// matching writes nothing.
	q := db.Query[stAudited](ctx).Where(auditViews.Gte(12))
	if n, err := q.Delete(); err != nil || n != 3 {
		t.Fatalf("Delete = %d, %v", n, err)
	}
	if n, err := q.Restore(); err != nil || n != 3 {
		t.Fatalf("Restore = %d, %v", n, err)
	}
	if n, err := q.ForceDelete(); err != nil || n != 3 {
		t.Fatalf("ForceDelete = %d, %v", n, err)
	}
	if n, err := q.Delete(); err != nil || n != 0 {
		t.Fatalf("Delete of nothing = %d, %v", n, err)
	}
	bs = bulks()
	var actions []string
	for _, b := range bs[2:] {
		actions = append(actions, b.Action)
	}
	if !slices.Equal(actions, []string{audit.Deleted, audit.Restored, audit.ForceDeleted}) {
		t.Fatalf("bulk actions %v", actions)
	}
	if fd := bs[len(bs)-1]; len(fd.Before) != 3 || !fd.Complete || fd.Before[0].Values["notes"] != audit.Redacted || fd.Before[0].Values["code"] == nil {
		t.Errorf("force delete values: %+v", fd.Before)
	}

	// Who deleted row 4? Its history has the bulk writes.
	gone := rows[4]
	subject, err := audit.SubjectOf(&gone)
	check(t, err)
	events, _, err := audit.History(ctx, subject, 10, "")
	check(t, err)
	if len(events) != 5 || events[0].Bulk == nil || events[0].Action() != audit.ForceDeleted {
		t.Errorf("history of a bulk-deleted row: %d events", len(events))
	}

	// Upsert: one row updated, one created; an untouched conflict isn't
	// recorded.
	ups := []stAudited{{Code: "c0", Title: "changed", Notes: "n", APIToken: "x"}, {Code: "c9", Title: "new", Notes: "n", APIToken: "x"}}
	check(t, db.Upsert(ctx, ups, []string{"code"}, "title"))
	bs = bulks()
	u := bs[len(bs)-1]
	if u.Action != audit.Upserted || u.RowCount != 2 || len(u.Before) != 1 || len(u.After) != 2 || u.Before[0].Values["title"] != "t0" {
		t.Errorf("upsert: %+v", u)
	}
	if err := db.Upsert(ctx, []stAudited{{Code: "z", Title: "z"}}, []string{"id"}, "title"); err == nil {
		t.Error("a watched upsert on generated keys was accepted")
	}
	check(t, db.Upsert(ctx, ups[:1], []string{"code"}))
	if got := bulks(); len(got) != len(bs) {
		t.Error("an upsert that changed nothing was recorded")
	}
	// Many rows at once, past SQLite's expression limits.
	many := make([]stAudited, 1200)
	for i := range many {
		many[i] = stAudited{Code: fmt.Sprint("m", i), Title: "m", Notes: "n", APIToken: "x"}
	}
	check(t, db.Upsert(ctx, many, []string{"code"}, "title"))
	if u := bulks()[len(bulks())-1]; u.RowCount != 1200 || len(u.After) != 3 || u.Complete {
		t.Errorf("1,200-row upsert: %+v", u.RowCount)
	}

	// A condition naming a redacted column keeps its arguments to itself.
	_, err = db.Query[stAudited](ctx).Where(db.C("api_token").Eq("x"), auditViews.Eq(0)).Update(auditViews.Set(1))
	check(t, err)
	if c := bulks()[len(bulks())-1].Condition; len(c.Args) == 0 || c.Args[0] != audit.Redacted {
		t.Errorf("condition: %+v", c)
	}

	// Retention: entries older than 30 days go, and the pruning is
	// recorded.
	longAgo := time.Now().UTC().AddDate(0, 0, -40).Truncate(time.Microsecond)
	old := audit.Entry{OccurredAt: longAgo, ActorType: "system", Action: "old"}
	check(t, db.Create(ctx, &old))
	oldBulk := audit.BulkOp{OccurredAt: longAgo, ActorType: "user", ActorID: "9", Action: "old", SubjectType: "st_audited", RowCount: 1}
	check(t, db.Create(ctx, &oldBulk))
	check(t, db.Create(ctx, &audit.BulkItem{BulkID: oldBulk.ID, SubjectType: "st_audited", SubjectID: "1"}))
	before, _ := db.Query[audit.BulkOp](ctx).Count()
	p, err := audit.Prune(ctx)
	check(t, err)
	if p.Entries != 1 || p.Bulk != 1 {
		t.Errorf("Prune: %+v", p)
	}
	if n, _ := db.Query[audit.BulkItem](ctx).Where(db.C("bulk_id").Eq(oldBulk.ID)).Count(); n != 0 {
		t.Error("the pruned bulk entry's items are left")
	}
	if n, _ := db.Query[audit.BulkOp](ctx).Count(); n != before-1 {
		t.Errorf("%d bulk entries left of %d", n, before)
	}
	es := entries(t, ctx)
	if len(es) != 1 || es[0].Action != "audit.pruned" {
		t.Errorf("after Prune: %+v", es)
	}

	// Anonymizing reaches bulk entries too.
	mine := audit.BulkOp{OccurredAt: time.Now().UTC().Truncate(time.Microsecond), ActorType: "user", ActorID: "77", Action: audit.Updated, SubjectType: "st_audited", IP: "203.0.113.0"}
	check(t, db.Create(ctx, &mine))
	anonymized, err := audit.Anonymize(ctx, audit.User("77"))
	check(t, err)
	got, err := db.Find[audit.BulkOp](ctx, mine.ID)
	check(t, err)
	if anonymized != 1 || got.ActorID != "erased" || got.IP != "" {
		t.Errorf("anonymized bulk entry: %d, %+v", anonymized, got)
	}

	// And entries of someone impersonating the person, exactly them (MySQL's
	// text collations ignore case and trailing spaces).
	var acted []int64
	for _, as := range []string{"user:abc", "user:ABC", "user:abc "} {
		e := audit.Entry{OccurredAt: time.Now().UTC().Truncate(time.Microsecond), ActorType: "user", ActorID: "1", ActingAs: as,
			Action: audit.Updated, SubjectType: "st_audited", SubjectID: "1", Changes: audit.Changes{}}
		check(t, db.Create(ctx, &e))
		acted = append(acted, e.ID)
	}
	anonymized, err = audit.Anonymize(ctx, audit.User("abc"))
	check(t, err)
	var as []string
	for _, id := range acted {
		e, err := db.Find[audit.Entry](ctx, id)
		check(t, err)
		as = append(as, e.ActingAs)
	}
	if anonymized != 1 || !slices.Equal(as, []string{"user:erased", "user:ABC", "user:abc "}) {
		t.Errorf("anonymized acting_as: %d, %q", anonymized, as)
	}
}

// recorder is a db.Watcher that records writes and can fail.
type recorder struct {
	writes []db.Write
	fail   error
}

func (r *recorder) Written(_ context.Context, w *db.Write) error {
	r.writes = append(r.writes, *w)
	return r.fail
}

// stWatched is a model of a table watched by a recorder.
type stWatched struct {
	ID    int64  `db:"id,pk"`
	Label string `db:"label"`
}

// TableName implements db.Tabler.
func (stWatched) TableName() string { return "st_watched" }

var watchedRecorders = map[*db.DB]*recorder{}

func testWatchedWrites(t *testing.T, ctx context.Context) {
	mustExec(t, ctx, "DROP TABLE IF EXISTS st_watched")
	switch d(ctx).Dialect().Name() {
	case "postgres":
		mustExec(t, ctx, "CREATE TABLE st_watched (id BIGSERIAL PRIMARY KEY, label TEXT NOT NULL)")
	case "mysql":
		mustExec(t, ctx, "CREATE TABLE st_watched (id BIGINT AUTO_INCREMENT PRIMARY KEY, label TEXT NOT NULL)")
	default:
		mustExec(t, ctx, "CREATE TABLE st_watched (id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT NOT NULL)")
	}
	t.Cleanup(func() { _, _ = db.Exec(context.WithoutCancel(ctx), "DROP TABLE IF EXISTS st_watched") })
	r := watchedRecorders[d(ctx)]
	if r == nil {
		r = &recorder{}
		check(t, d(ctx).Watch("st_watched", r, 1))
		watchedRecorders[d(ctx)] = r
	}
	r.writes, r.fail = nil, nil

	w := stWatched{Label: "a"}
	check(t, db.Create(ctx, &w))
	w.Label = "b"
	check(t, db.Update(ctx, &w))
	if len(r.writes) != 2 || r.writes[0].Op != db.OpCreate || r.writes[0].After["label"] != "a" ||
		r.writes[1].Op != db.OpUpdate || r.writes[1].Before["label"] != "a" || r.writes[1].After["label"] != "b" ||
		fmt.Sprint(r.writes[1].Key) != fmt.Sprint(w.ID) {
		t.Fatalf("writes: %+v", r.writes)
	}

	// A watcher's error rolls the write back.
	r.fail = errors.New("no")
	w.Label = "c"
	if err := db.Update(ctx, &w); !errors.Is(err, r.fail) {
		t.Fatalf("Update with a failing watcher = %v", err)
	}
	if got, _ := db.Find[stWatched](ctx, w.ID); got.Label != "b" {
		t.Errorf("the failed update was kept: %q", got.Label)
	}
	if _, err := db.Query[stWatched](ctx).Update(db.Col[string]("label").Set("d")); !errors.Is(err, r.fail) {
		t.Fatalf("bulk Update with a failing watcher = %v", err)
	}
	if got, _ := db.Find[stWatched](ctx, w.ID); got.Label != "b" {
		t.Errorf("the failed bulk update was kept: %q", got.Label)
	}
	r.fail = nil

	// A bulk write in chunks: more rows than one chunk, values of one row
	// (the watcher asked for 1).
	many := make([]stWatched, 1500)
	for i := range many {
		many[i].Label = "x"
	}
	check(t, db.CreateMany(ctx, many))
	r.writes = nil
	n, err := db.Query[stWatched](ctx).Where(db.Col[string]("label").Eq("x")).Update(db.Col[string]("label").Set("y"))
	check(t, err)
	if n != 1500 || len(r.writes) != 1 || len(r.writes[0].Bulk.Keys) != 1500 || len(r.writes[0].Bulk.Before) != 1 || r.writes[0].Bulk.Complete ||
		r.writes[0].Bulk.Set["label"] != "y" {
		t.Fatalf("chunked bulk update: %d rows, writes %d", n, len(r.writes))
	}
	if left, _ := db.Query[stWatched](ctx).Where(db.Col[string]("label").Eq("x")).Count(); left != 0 {
		t.Errorf("%d rows weren't updated", left)
	}
	// Plain Delete on a model without SoftDeletes: a force delete, with
	// the values.
	r.writes = nil
	check(t, db.Delete(ctx, &w))
	if len(r.writes) != 1 || r.writes[0].Op != db.OpForceDelete || r.writes[0].Before["label"] != "b" {
		t.Errorf("delete: %+v", r.writes)
	}
	if err := db.Delete(ctx, &w); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("deleting a deleted row = %v", err)
	}
}

// stTrashable is a model with soft deletes and an email unique among
// live rows.
type stTrashable struct {
	db.Model
	db.SoftDeletes
	Email string `db:"email"`
}

// TableName implements db.Tabler.
func (stTrashable) TableName() string { return "st_trashables" }

func testUniqueLiveAndPrune(t *testing.T, ctx context.Context) {
	set := migrate.NewSet("st_live")
	set.AddFunc("2026_10_06_000000_create_st_trashables", func(s *migrate.Schema) error {
		return s.Create("st_trashables", func(t *migrate.Table) {
			t.ID()
			t.String("email", 100).UniqueLive()
			t.Timestamps()
			t.SoftDeletes()
		})
	}, func(s *migrate.Schema) error { return s.Drop("st_trashables") })
	r, err := migrate.NewRunner(d(ctx), []*migrate.Set{set}, migrate.WithTable("st_live_migrations"))
	check(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		for _, table := range []string{"st_trashables", "st_live_migrations"} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table)
		}
	})
	_, err = r.Up(ctx)
	if d(ctx).Dialect().Name() == "mysql" {
		if err == nil {
			t.Fatal("UniqueLive on MySQL didn't fail")
		}
		return
	}
	check(t, err)

	a := stTrashable{Email: "a@example.com"}
	check(t, db.Create(ctx, &a))
	if err := db.Create(ctx, &stTrashable{Email: "a@example.com"}); err == nil {
		t.Fatal("two live rows with one email")
	}
	type form struct {
		Email string `json:"email" validate:"unique_live:st_trashables,email"`
	}
	if err := validate.Struct(ctx, &form{"a@example.com"}); err == nil {
		t.Error("unique_live passed a live duplicate")
	}
	check(t, db.Delete(ctx, &a))
	if err := validate.Struct(ctx, &form{"a@example.com"}); err != nil {
		t.Errorf("unique_live counted a deleted row: %v", err)
	}
	b := stTrashable{Email: "a@example.com"}
	check(t, db.Create(ctx, &b))

	// Pruning: a row deleted 40 days ago goes, one deleted now stays.
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing"}), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	check(t, db.PruneTrashed[stTrashable](app, 30*24*time.Hour))
	if err := db.PruneTrashed[stTrashable](app, time.Hour); err == nil {
		t.Error("registered twice")
	}
	if err := db.PruneTrashed[stAuthor](app, time.Hour); err == nil {
		t.Error("registered a model without SoftDeletes")
	}
	mustExec(t, ctx, "UPDATE st_trashables SET deleted_at = ? WHERE id = ?", time.Now().UTC().AddDate(0, 0, -40), a.ID)
	check(t, db.Delete(ctx, &b))
	done, err := db.PruneAllTrashed(app.Context(ctx))
	check(t, err)
	if len(done) != 1 || done[0].Rows != 1 || done[0].Table != "st_trashables" {
		t.Errorf("PruneAllTrashed = %+v", done)
	}
	if n, _ := db.Query[stTrashable](ctx).WithTrashed().Count(); n != 1 {
		t.Errorf("%d rows left", n)
	}
}
