// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

func init() {
	extra = append(extra,
		test{"SchemaBuilder", testSchemaBuilder},
		test{"SchemaAlter", testSchemaAlter},
		test{"MigrationRunner", testMigrationRunner},
		test{"MigrationFailure", testMigrationFailure},
		test{"MigrationCommands", testMigrationCommands},
		test{"MigrationLock", testMigrationLock},
	)
}

var migrationTables = []string{"st_migrations", "st_m_notes", "st_m_items", "st_m_owners", "st_m_sql", "st_m_renamed", "st_m_extra"}

// Item exercises every column type of the schema builder.
type Item struct {
	ID        int64             `db:"id,pk"`
	OwnerID   *int64            `db:"owner_id"`
	Code      string            `db:"code"`
	Ref       string            `db:"ref"`
	Name      string            `db:"name"`
	Body      string            `db:"body"`
	Qty       int32             `db:"qty"`
	Big       int64             `db:"big"`
	Small     int16             `db:"small"`
	Active    bool              `db:"active"`
	Ratio     float64           `db:"ratio"`
	Price     string            `db:"price"`
	Day       time.Time         `db:"day"`
	Meta      map[string]string `db:"meta,json"`
	Blob      []byte            `db:"blob"`
	CreatedAt time.Time         `db:"created_at,readonly"`
}

// TableName implements db.Tabler.
func (Item) TableName() string { return "st_m_items" }

// Owner owns items.
type Owner struct {
	ID   int64  `db:"id,pk"`
	Name string `db:"name"`
}

// TableName implements db.Tabler.
func (Owner) TableName() string { return "st_m_owners" }

func dropMigrationTables(t *testing.T, ctx context.Context) {
	t.Helper()
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	for _, name := range migrationTables {
		check(t, s.DropIfExists(name))
	}
}

func createOwnersAndItems(s *migrate.Schema) error {
	if err := s.Create("st_m_owners", func(t *migrate.Table) {
		t.ID()
		t.String("name", 50).Unique()
	}); err != nil {
		return err
	}
	return s.Create("st_m_items", func(t *migrate.Table) {
		t.ID()
		t.ForeignID("owner_id").Nullable().References("st_m_owners").NullOnDelete()
		t.UUID("code")
		t.String("ref", 20).Default("none")
		t.String("name", 100)
		t.Text("body")
		t.Integer("qty").Default(1)
		t.BigInteger("big")
		t.SmallInteger("small")
		t.Boolean("active").Default(true)
		t.Float("ratio")
		t.Decimal("price", 10, 2)
		t.Date("day")
		t.JSON("meta").Nullable()
		t.Binary("blob").Nullable()
		t.Timestamp("created_at").UseCurrent()
		t.Index("owner_id", "created_at")
		t.Unique("code")
	})
}

func testSchemaBuilder(t *testing.T, ctx context.Context) {
	dropMigrationTables(t, ctx)
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	check(t, createOwnersAndItems(s))
	if ok, err := s.HasTable("st_m_items"); err != nil || !ok {
		t.Fatalf("HasTable = %v, %v", ok, err)
	}
	if ok, _ := s.HasColumn("st_m_items", "price"); !ok {
		t.Error("HasColumn(price) = false")
	}
	if ok, _ := s.HasColumn("st_m_items", "nope"); ok {
		t.Error("HasColumn(nope) = true")
	}

	owner := Owner{Name: "Ada"}
	check(t, db.Create(ctx, &owner))
	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	item := Item{OwnerID: &owner.ID, Code: "123e4567-e89b-12d3-a456-426614174000", Name: "Widget", Body: "b",
		Qty: 3, Big: 1 << 40, Small: 7, Active: true, Ratio: 0.25, Price: "19.99", Day: day,
		Meta: map[string]string{"k": "v"}, Blob: []byte{0, 1, 2}, Ref: "r1"}
	check(t, db.Create(ctx, &item))
	got, err := db.Find[Item](ctx, item.ID)
	check(t, err)
	if got.Code != item.Code || got.Name != "Widget" || got.Qty != 3 || got.Big != 1<<40 || got.Small != 7 ||
		!got.Active || got.Ratio != 0.25 || got.Price != "19.99" || got.Day.Format(time.DateOnly) != "2026-03-15" ||
		got.Meta["k"] != "v" || !bytes.Equal(got.Blob, []byte{0, 1, 2}) || got.OwnerID == nil || *got.OwnerID != owner.ID {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, item)
	}
	if got.CreatedAt.IsZero() || time.Since(got.CreatedAt).Abs() > time.Hour {
		t.Errorf("created_at default = %v", got.CreatedAt)
	}

	// Defaults apply to rows inserted without the column.
	_, err = db.Exec(ctx, "INSERT INTO st_m_items (code, name, body, big, small, ratio, price, day) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		"223e4567-e89b-12d3-a456-426614174000", "Plain", "", 1, 1, 0, "1.00", day)
	check(t, err)
	plain, err := db.Query[Item](ctx).Where(db.C("name").Eq("Plain")).First()
	check(t, err)
	if plain.Qty != 1 || !plain.Active || plain.Ref != "none" || plain.OwnerID != nil {
		t.Errorf("defaults: %+v", plain)
	}

	// Unique index.
	dup := item
	dup.ID = 0
	if err := db.Create(ctx, &dup); err == nil {
		t.Error("duplicate code accepted")
	}
	// ON DELETE SET NULL.
	check(t, db.Delete(ctx, &owner))
	orphan, err := db.Find[Item](ctx, item.ID)
	check(t, err)
	if orphan.OwnerID != nil {
		t.Errorf("owner_id after the owner was deleted = %v", *orphan.OwnerID)
	}
	check(t, s.Drop("st_m_items"))
	check(t, s.DropIfExists("st_m_items"))
	if ok, _ := s.HasTable("st_m_items"); ok {
		t.Error("table still there after Drop")
	}
}

func testSchemaAlter(t *testing.T, ctx context.Context) {
	dropMigrationTables(t, ctx)
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	check(t, s.Create("st_m_notes", func(t *migrate.Table) {
		t.ID()
		t.String("title", 50)
		t.String("legacy", 10).Nullable()
		t.Integer("rank").Default(0)
	}))
	_, err = db.Exec(ctx, "INSERT INTO st_m_notes (title) VALUES (?)", "first")
	check(t, err)
	check(t, s.Alter("st_m_notes", func(t *migrate.Table) {
		t.String("subtitle", 50).Nullable()
		t.Integer("stars").Default(5)
		t.RenameColumn("title", "heading")
		t.Index("rank")
	}))
	check(t, s.Alter("st_m_notes", func(t *migrate.Table) { t.DropColumn("legacy") }))
	for col, want := range map[string]bool{"heading": true, "title": false, "subtitle": true, "legacy": false, "stars": true} {
		if ok, err := s.HasColumn("st_m_notes", col); err != nil || ok != want {
			t.Errorf("HasColumn(%s) = %v, %v", col, ok, err)
		}
	}
	stars, err := db.RawFirst[int64](ctx, "SELECT stars FROM st_m_notes")
	check(t, err)
	if stars != 5 {
		t.Errorf("new column's default for an existing row = %d", stars)
	}
	check(t, s.Alter("st_m_notes", func(t *migrate.Table) { t.DropIndex("rank") }))
	if s.Dialect() != "sqlite" {
		check(t, s.Alter("st_m_notes", func(t *migrate.Table) { t.String("heading", 200).Nullable().Change() }))
		_, err = db.Exec(ctx, "INSERT INTO st_m_notes (heading) VALUES (?)", strings.Repeat("x", 150))
		check(t, err)
		_, err = db.Exec(ctx, "INSERT INTO st_m_notes (rank) VALUES (?)", 1)
		check(t, err) // heading is nullable now
	}
	check(t, s.Rename("st_m_notes", "st_m_renamed"))
	if ok, _ := s.HasTable("st_m_renamed"); !ok {
		t.Error("renamed table missing")
	}
	check(t, s.Exec("CREATE TABLE st_m_extra (x INTEGER); INSERT INTO st_m_extra (x) VALUES (1); INSERT INTO st_m_extra (x) VALUES (2);"))
	if n, _ := db.RawFirst[int64](ctx, "SELECT COUNT(*) FROM st_m_extra"); n != 2 {
		t.Errorf("multi-statement Exec inserted %d", n)
	}
}

func migrationSet(extraSteps bool) *migrate.Set {
	set := migrate.NewSet("app")
	set.AddFunc("2026_01_01_000001_create_owners", func(s *migrate.Schema) error {
		return s.Create("st_m_owners", func(t *migrate.Table) {
			t.ID()
			t.String("name", 50)
		})
	}, func(s *migrate.Schema) error { return s.Drop("st_m_owners") })
	set.AddFunc("2026_01_01_000002_create_notes", func(s *migrate.Schema) error {
		return s.Create("st_m_notes", func(t *migrate.Table) {
			t.ID()
			t.ForeignID("owner_id").References("st_m_owners").CascadeOnDelete()
			t.Text("body")
			t.Timestamps()
		})
	}, func(s *migrate.Schema) error { return s.Drop("st_m_notes") })
	if extraSteps {
		set.AddFunc("2026_01_02_000001_add_rank", func(s *migrate.Schema) error {
			return s.Alter("st_m_owners", func(t *migrate.Table) { t.Integer("rank").Default(0) })
		}, func(s *migrate.Schema) error {
			return s.Alter("st_m_owners", func(t *migrate.Table) { t.DropColumn("rank") })
		})
	}
	return set
}

func sqlSet(t *testing.T) *migrate.Set {
	set := migrate.NewSet("reports")
	check(t, set.AddFS(fstest.MapFS{
		"sql/2026_01_01_000003_create_sql.up.sql":   {Data: []byte("CREATE TABLE st_m_sql (id INTEGER PRIMARY KEY, label VARCHAR(20));\nINSERT INTO st_m_sql VALUES (1, 'a;b');")},
		"sql/2026_01_01_000003_create_sql.down.sql": {Data: []byte("DROP TABLE st_m_sql;")},
	}, "sql"))
	return set
}

func runner(t *testing.T, ctx context.Context, env anetos.Environment, sets ...*migrate.Set) *migrate.Runner {
	t.Helper()
	r, err := migrate.NewRunner(d(ctx), sets, migrate.WithTable("st_migrations"), migrate.WithEnvironment(env),
		migrate.WithSeeders(migrate.Seeder{Name: "owners", Run: func(ctx context.Context) error {
			_, err := db.Exec(ctx, "INSERT INTO st_m_owners (name) VALUES (?), (?)", "Ada", "Bob")
			return err
		}}))
	check(t, err)
	return r
}

func ids(results []migrate.Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.ID
	}
	return out
}

func testMigrationRunner(t *testing.T, ctx context.Context) {
	dropMigrationTables(t, ctx)
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	r := runner(t, ctx, anetos.Testing, migrationSet(false), sqlSet(t))
	done, err := r.Up(ctx)
	check(t, err)
	want := []string{"2026_01_01_000001_create_owners", "2026_01_01_000002_create_notes", "2026_01_01_000003_create_sql"}
	if !slices.Equal(ids(done), want) {
		t.Fatalf("Up = %v", ids(done))
	}
	if label, _ := db.RawFirst[string](ctx, "SELECT label FROM st_m_sql"); label != "a;b" {
		t.Errorf("SQL migration: label = %q", label)
	}
	again, err := r.Up(ctx)
	check(t, err)
	if len(again) != 0 {
		t.Errorf("second Up applied %v", ids(again))
	}

	r2 := runner(t, ctx, anetos.Testing, migrationSet(true), sqlSet(t))
	done, err = r2.Up(ctx)
	check(t, err)
	if !slices.Equal(ids(done), []string{"2026_01_02_000001_add_rank"}) {
		t.Errorf("batch 2 = %v", ids(done))
	}
	status, err := r2.Status(ctx)
	check(t, err)
	if len(status) != 4 || status[3].Batch != 2 || status[0].Batch != 1 || !status[0].Applied || status[2].Source != "reports" {
		t.Errorf("status = %+v", status)
	}

	undone, err := r2.Rollback(ctx, 1)
	check(t, err)
	if !slices.Equal(ids(undone), []string{"2026_01_02_000001_add_rank"}) {
		t.Errorf("rollback = %v", ids(undone))
	}
	if ok, _ := s.HasColumn("st_m_owners", "rank"); ok {
		t.Error("rank still there after rollback")
	}

	// A migration applied but no longer registered shows as missing and
	// blocks rolling back past it.
	_, err = r2.Up(ctx)
	check(t, err)
	status, err = r.Status(ctx) // r doesn't know add_rank
	check(t, err)
	if last := status[len(status)-1]; !last.Missing || last.ID != "2026_01_02_000001_add_rank" {
		t.Errorf("missing migration: %+v", last)
	}
	if _, err := r.Rollback(ctx, 1); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("rollback of an unregistered migration: %v", err)
	}

	undone, err = r2.Reset(ctx)
	check(t, err)
	if len(undone) != 4 || undone[0].ID != "2026_01_02_000001_add_rank" || undone[3].ID != "2026_01_01_000001_create_owners" {
		t.Errorf("reset = %v", ids(undone))
	}
	if ok, _ := s.HasTable("st_m_owners"); ok {
		t.Error("tables left after reset")
	}

	// Fresh drops everything (also tables no migration knows) and migrates.
	_, err = db.Exec(ctx, "CREATE TABLE st_m_extra (x INTEGER)")
	check(t, err)
	if _, err := runner(t, ctx, anetos.Production, migrationSet(true)).Fresh(ctx); !errors.Is(err, migrate.ErrNotAllowed) {
		t.Errorf("Fresh in production: %v", err)
	}
	done, err = r2.Fresh(ctx)
	check(t, err)
	if len(done) != 4 {
		t.Errorf("fresh applied %v", ids(done))
	}
	if ok, _ := s.HasTable("st_m_extra"); ok {
		t.Error("Fresh left an unknown table")
	}
	check(t, r2.Seed(ctx))
	if n, _ := db.RawFirst[int64](ctx, "SELECT COUNT(*) FROM st_m_owners"); n != 2 {
		t.Errorf("seeded %d owners", n)
	}
	if err := r2.Seed(ctx, "nope"); err == nil {
		t.Error("unknown seeder accepted")
	}
	// Fresh dropped every table, including the suite's own; recreate them.
	recreate(t, ctx)
}

type failing struct{ table string }

func (f failing) Up(s *migrate.Schema) error {
	if err := s.Create(f.table, func(t *migrate.Table) { t.ID() }); err != nil {
		return err
	}
	return errors.New("boom")
}

func (f failing) Down(s *migrate.Schema) error { return s.Drop(f.table) }

func testMigrationFailure(t *testing.T, ctx context.Context) {
	dropMigrationTables(t, ctx)
	set := migrationSet(false)
	set.Add("2026_01_03_000001_fails", failing{"st_m_extra"})
	set.AddFunc("2026_01_03_000002_never", func(*migrate.Schema) error { t.Error("ran after a failure"); return nil }, nil)
	r := runner(t, ctx, anetos.Testing, set)
	done, err := r.Up(ctx)
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "2026_01_03_000001_fails") {
		t.Errorf("err = %v", err)
	}
	if len(done) != 2 {
		t.Errorf("applied before the failure: %v", ids(done))
	}
	status, err := r.Status(ctx)
	check(t, err)
	if status[2].Applied {
		t.Error("failed migration recorded as applied")
	}
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	exists, _ := s.HasTable("st_m_extra")
	if transactional := s.Dialect() != "mysql"; transactional && exists {
		t.Error("the failed migration's table survived (DDL should have rolled back)")
	}
}

func testMigrationCommands(t *testing.T, ctx context.Context) {
	dropMigrationTables(t, ctx)
	var out bytes.Buffer
	run := func(env anetos.Environment, args ...string) error {
		t.Helper()
		out.Reset()
		handled, err := runner(t, ctx, env, migrationSet(true)).Command(ctx, args, &out)
		if !handled {
			t.Fatalf("%v not handled", args)
		}
		return err
	}
	check(t, run(anetos.Production, "migrate"))
	if !strings.Contains(out.String(), "Migrated:") || strings.Count(out.String(), "\n") != 3 {
		t.Errorf("migrate output:\n%s", out.String())
	}
	check(t, run(anetos.Production, "migrate"))
	if !strings.Contains(out.String(), "Nothing to migrate.") {
		t.Errorf("second migrate:\n%s", out.String())
	}
	check(t, run(anetos.Production, "migrate:status"))
	if !strings.Contains(out.String(), "STATUS") || strings.Count(out.String(), "Ran") != 3 {
		t.Errorf("status:\n%s", out.String())
	}
	if err := run(anetos.Production, "migrate:rollback"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("rollback in production without --force: %v", err)
	}
	check(t, run(anetos.Production, "migrate:rollback", "--force", "--step=1"))
	if !strings.Contains(out.String(), "Rolled back:") {
		t.Errorf("rollback output:\n%s", out.String())
	}
	if err := run(anetos.Production, "migrate:fresh"); !errors.Is(err, migrate.ErrNotAllowed) {
		t.Errorf("fresh in production: %v", err)
	}
	check(t, run(anetos.Development, "migrate:fresh", "--seed"))
	if !strings.Contains(out.String(), "Seeded:      owners") {
		t.Errorf("fresh --seed:\n%s", out.String())
	}
	check(t, run(anetos.Development, "db:seed", "--seeder=owners"))
	if err := run(anetos.Development, "migrate", "extra"); err == nil {
		t.Error("stray argument accepted")
	}
	if handled, _ := runner(t, ctx, anetos.Testing).Command(ctx, []string{"serve"}, &out); handled {
		t.Error("serve handled")
	}
	check(t, run(anetos.Development, "migrate:reset"))
	check(t, run(anetos.Development, "migrate", "--seed"))
	if !strings.Contains(out.String(), "Seeded:      owners") {
		t.Errorf("migrate --seed:\n%s", out.String())
	}
	if err := run(anetos.Development, "migrate:status", "--step=2"); err == nil {
		t.Error("a flag migrate:status doesn't take was accepted")
	}
	if err := run(anetos.Development, "migrate", "-h"); err != nil {
		t.Errorf("-h: %v", err)
	}
	failing := migrationSet(true)
	failing.AddFunc("2026_09_09_000000_fails", func(*migrate.Schema) error { return errors.New("boom") }, nil)
	out.Reset()
	if _, err := runner(t, ctx, anetos.Development, failing).Command(ctx, []string{"migrate"}, &out); err == nil || strings.Contains(out.String(), "Nothing to migrate") {
		t.Errorf("failed migrate: %v\n%s", err, out.String())
	}
	recreate(t, ctx)
}

func testMigrationLock(t *testing.T, ctx context.Context) {
	dropMigrationTables(t, ctx)
	if name := d(ctx).Dialect().Name(); name != "sqlite" {
		one := db.New(d(ctx).SQL(), d(ctx).Dialect())
		single, err := migrate.NewRunner(one, []*migrate.Set{migrationSet(true)}, migrate.WithTable("st_migrations"))
		check(t, err)
		d(ctx).SQL().SetMaxOpenConns(1)
		_, err = single.Up(ctx)
		d(ctx).SQL().SetMaxOpenConns(10)
		if err == nil || !strings.Contains(err.Error(), "at least 2 connections") {
			t.Errorf("pool of one: %v", err)
		}
	}
	var wg sync.WaitGroup
	results := make([][]migrate.Result, 4)
	errs := make([]error, 4)
	for i := range 4 {
		wg.Go(func() {
			// Separate runners, as in separate processes.
			results[i], errs[i] = runner(t, ctx, anetos.Testing, migrationSet(true)).Up(ctx)
		})
	}
	wg.Wait()
	total := 0
	for i := range 4 {
		if errs[i] != nil && d(ctx).Dialect().Name() != "sqlite" {
			t.Errorf("runner %d: %v", i, errs[i])
		}
		total += len(results[i])
	}
	if d(ctx).Dialect().Name() != "sqlite" && total != 3 {
		t.Errorf("migrations applied %d times in total, want 3", total)
	}
	n, err := db.RawFirst[int64](ctx, "SELECT COUNT(*) FROM st_migrations")
	check(t, err)
	if n != 3 {
		t.Errorf("%d migration records, want 3", n)
	}
}

func init() {
	extra = append(extra,
		test{"MigrationEdgeCases", testMigrationEdgeCases},
		test{"SQLiteRebuild", testSQLiteRebuild},
	)
}

func testMigrationEdgeCases(t *testing.T, ctx context.Context) {
	dropMigrationTables(t, ctx)
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	dialect := s.Dialect()

	// Triggers and procedure bodies stay whole.
	check(t, s.Create("st_m_notes", func(t *migrate.Table) {
		t.ID()
		t.String("body", 50)
		t.Integer("edits").Default(0)
	}))
	switch dialect {
	case "sqlite":
		check(t, s.Exec(`CREATE TRIGGER st_m_notes_edits AFTER UPDATE OF body ON st_m_notes
BEGIN
	UPDATE st_m_notes SET edits = edits + 1 WHERE id = NEW.id;
END;
SELECT 1;`))
	case "mysql":
		check(t, s.Exec(`CREATE TRIGGER st_m_notes_edits BEFORE UPDATE ON st_m_notes FOR EACH ROW
BEGIN
	IF NEW.body <> OLD.body THEN
		SET NEW.edits = OLD.edits + 1;
	END IF;
END;
SELECT 1;`))
	case "postgres":
		check(t, s.Exec(`CREATE OR REPLACE FUNCTION st_m_count_edits() RETURNS trigger AS $$
BEGIN
	NEW.edits := OLD.edits + 1; -- a semicolon; inside the body
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER st_m_notes_edits BEFORE UPDATE ON st_m_notes FOR EACH ROW EXECUTE FUNCTION st_m_count_edits();`))
	}
	_, err = db.Exec(ctx, "INSERT INTO st_m_notes (body) VALUES (?)", "a")
	check(t, err)
	_, err = db.Exec(ctx, "UPDATE st_m_notes SET body = ?", "b")
	check(t, err)
	if n, _ := db.RawFirst[int64](ctx, "SELECT edits FROM st_m_notes"); n != 1 {
		t.Errorf("trigger ran %d times", n)
	}

	// Raw SQL without arguments is sent as written: ? is not a placeholder.
	if dialect == "postgres" {
		check(t, s.Exec(`CREATE TABLE st_m_extra (doc JSONB); INSERT INTO st_m_extra VALUES ('{"k": 1}')`))
		if n, _ := db.RawFirst[int64](ctx, `SELECT COUNT(*) FROM st_m_extra WHERE doc ? 'k'`); n != 1 {
			t.Errorf("jsonb ? matched %d", n)
		}
		// PostgreSQL's USING converts a column Change can't cast alone.
		check(t, s.Alter("st_m_notes", func(t *migrate.Table) { t.String("code", 10).Default("42") }))
		check(t, s.Alter("st_m_notes", func(t *migrate.Table) {
			t.Integer("code").Default(0).Change().Using("code::integer")
		}))
	}

	// Long index names stay distinct and droppable.
	check(t, s.Create("st_m_items", func(t *migrate.Table) {
		t.ID()
		t.BigInteger("organization_member_identifier")
		t.String("notification_channel_preference_a", 10)
		t.String("notification_channel_preference_b", 10)
		t.Index("organization_member_identifier", "notification_channel_preference_a")
		t.Index("organization_member_identifier", "notification_channel_preference_b")
	}))
	check(t, s.Alter("st_m_items", func(t *migrate.Table) {
		t.DropIndex("organization_member_identifier", "notification_channel_preference_a")
		t.IndexNamed("st_m_items_short", "notification_channel_preference_a")
	}))
	check(t, s.Alter("st_m_items", func(t *migrate.Table) { t.DropIndexNamed("st_m_items_short") }))

	// Portability checks fail on every database, not only some.
	if err := s.Alter("st_m_items", func(t *migrate.Table) { t.Integer("required_later") }); err == nil {
		t.Error("NOT NULL column without a default added to an existing table")
	}
	if err := s.Create("st_m_extra2", func(t *migrate.Table) { t.String("x", 1).Default(nil) }); err == nil {
		t.Error("NOT NULL DEFAULT NULL accepted")
	}
	if err := s.Create("other.table", func(t *migrate.Table) { t.ID() }); err == nil {
		t.Error("schema-qualified name accepted")
	}

	// Decimals keep every digit.
	check(t, s.Create("st_m_owners", func(t *migrate.Table) {
		t.ID()
		t.Decimal("amount", 20, 2)
	}))
	_, err = db.Exec(ctx, "INSERT INTO st_m_owners (amount) VALUES (?)", "123456789012345678.90")
	check(t, err)
	if got, _ := db.RawFirst[string](ctx, "SELECT amount FROM st_m_owners"); got != "123456789012345678.90" {
		t.Errorf("decimal = %s", got)
	}

	// A migration marked NoTransaction runs outside one.
	set := migrate.NewSet("app")
	ran := false
	set.Add("2026_01_05_000001_no_tx", migrate.NoTransaction(migrate.Func(func(s *migrate.Schema) error {
		ran = !db.InTx(s.Context())
		if s.Dialect() == "postgres" {
			return s.Exec("CREATE INDEX CONCURRENTLY st_m_notes_body_idx ON st_m_notes (body)")
		}
		return nil
	}, nil)))
	check(t, set.AddFS(fstest.MapFS{"sql/2026_01_05_000002_sql_no_tx.up.sql": {Data: []byte("-- anetos:no-transaction\nCREATE TABLE st_m_sql (x INTEGER);")}}, "sql"))
	_, err = runner(t, ctx, anetos.Testing, set).Up(ctx)
	check(t, err)
	if !ran {
		t.Error("NoTransaction migration ran in a transaction")
	}

	// PostgreSQL: Fresh drops enum types and leaves extension objects.
	if dialect == "postgres" {
		check(t, s.Exec("DROP TYPE IF EXISTS st_m_mood; CREATE TYPE st_m_mood AS ENUM ('ok', 'sad')"))
		extension := s.Exec("CREATE EXTENSION IF NOT EXISTS pg_buffercache") == nil
		for range 2 {
			fresh := migrate.NewSet("app")
			fresh.AddFunc("2026_01_06_000001_mood", func(s *migrate.Schema) error {
				return s.Exec("CREATE TYPE st_m_mood AS ENUM ('ok', 'sad')")
			}, nil)
			_, err := runner(t, ctx, anetos.Testing, fresh).Fresh(ctx)
			check(t, err)
		}
		if extension {
			check(t, s.Exec("DROP EXTENSION pg_buffercache"))
		}
		recreate(t, ctx)
	}
}

func testSQLiteRebuild(t *testing.T, ctx context.Context) {
	if d(ctx).Dialect().Name() != "sqlite" {
		t.Skip("SQLite only")
	}
	dropMigrationTables(t, ctx)
	set := migrationSet(false)
	// The documented way to change a column on SQLite: new table, copy,
	// drop, rename. Foreign keys are off during SQLite migrations, so the
	// drop doesn't cascade to st_m_notes.
	set.AddFunc("2026_01_04_000001_rebuild_owners", func(s *migrate.Schema) error {
		if err := s.Create("st_m_owners_new", func(t *migrate.Table) {
			t.ID()
			t.String("name", 200) // was 50
		}); err != nil {
			return err
		}
		if err := s.Exec("INSERT INTO st_m_owners_new (id, name) SELECT id, name FROM st_m_owners"); err != nil {
			return err
		}
		if err := s.Drop("st_m_owners"); err != nil {
			return err
		}
		return s.Rename("st_m_owners_new", "st_m_owners")
	}, nil)
	first := migrate.NewSet("app")
	first.Add("2026_01_01_000001_create_owners", set.Migration("2026_01_01_000001_create_owners"))
	first.Add("2026_01_01_000002_create_notes", set.Migration("2026_01_01_000002_create_notes"))
	_, err := runner(t, ctx, anetos.Testing, first).Up(ctx)
	check(t, err)
	_, err = db.Exec(ctx, "INSERT INTO st_m_owners (name) VALUES ('Ada')")
	check(t, err)
	_, err = db.Exec(ctx, "INSERT INTO st_m_notes (owner_id, body) VALUES (1, 'a'), (1, 'b')")
	check(t, err)
	_, err = runner(t, ctx, anetos.Testing, set).Up(ctx)
	check(t, err)
	if n, _ := db.RawFirst[int64](ctx, "SELECT COUNT(*) FROM st_m_notes"); n != 2 {
		t.Errorf("rebuild lost child rows: %d left", n)
	}
	// A migration that breaks a foreign key is rolled back.
	broken := migrate.NewSet("app")
	broken.AddFunc("2026_01_04_000002_break", func(s *migrate.Schema) error {
		return s.Exec("DELETE FROM st_m_owners")
	}, nil)
	_, err = runner(t, ctx, anetos.Testing, broken).Up(ctx)
	if err == nil || !strings.Contains(err.Error(), "foreign keys would be broken") {
		t.Errorf("broken foreign keys: %v", err)
	}
	if n, _ := db.RawFirst[int64](ctx, "SELECT COUNT(*) FROM st_m_owners"); n != 1 {
		t.Errorf("owners after the failed migration: %d", n)
	}
}
