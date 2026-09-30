// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode"
)

// Migration changes the database schema in Up and undoes the change in
// Down.
type Migration interface {
	// Up applies the change.
	Up(s *Schema) error
	// Down undoes it; return ErrIrreversible if it can't be undone.
	Down(s *Schema) error
}

// WithoutTransaction can be implemented by a migration that must not run
// in a transaction, such as PostgreSQL's CREATE INDEX CONCURRENTLY. Wrap a
// migration made by [Func] with [NoTransaction] instead; SQL files start
// with a "-- anetos:no-transaction" line.
type WithoutTransaction interface {
	// WithoutTransaction marks the migration; it is never called.
	WithoutTransaction()
}

// Func returns a migration made of two functions. down may be nil for a
// migration that can't be rolled back.
func Func(up, down func(s *Schema) error) Migration { return funcMigration{up, down} }

// NoTransaction returns m marked to run outside a transaction.
func NoTransaction(m Migration) Migration { return noTx{m} }

type noTx struct{ Migration }

func (noTx) WithoutTransaction() {}

// ErrIrreversible is returned by Down for a migration that can't be undone.
var ErrIrreversible = errors.New("migrate: this migration can't be rolled back")

// Set is a named group of migrations: the app's, or a plugin's. Status
// shows which set each migration belongs to. Build a set in a package of
// its own, with one file per migration adding itself:
//
//	// database/migrations/migrations.go
//	var All = migrate.NewSet("app")
//
//	// database/migrations/2026_10_01_120000_create_posts.go
//	func init() { All.Add("2026_10_01_120000_create_posts", createPosts{}) }
//
// Migrations run in order of their IDs across all sets, so IDs should
// start with a timestamp.
type Set struct {
	source  string
	entries []entry
	ids     map[string]bool
}

type entry struct {
	source string
	id     string
	m      Migration
	noTx   bool
}

// NewSet returns an empty set named source ("app", or a plugin's name).
func NewSet(source string) *Set {
	if !validID(source) || len(source) > 100 {
		panic(fmt.Sprintf("migrate: invalid set name %q", source))
	}
	return &Set{source: source, ids: map[string]bool{}}
}

// Source returns the set's name.
func (s *Set) Source() string { return s.source }

// Add adds a migration. It panics if id is empty, contains spaces, is
// longer than 255 bytes, or was already added: those are programming
// errors found at startup.
func (s *Set) Add(id string, m Migration) {
	if !validID(id) || len(id) > 255 {
		panic(fmt.Sprintf("migrate: invalid migration ID %q (use letters, digits, _ . -)", id))
	}
	if s.ids[id] {
		panic(fmt.Sprintf("migrate: migration %q added twice to %s", id, s.source))
	}
	if m == nil {
		panic("migrate: nil migration " + id)
	}
	s.ids[id] = true
	_, noTx := m.(WithoutTransaction)
	s.entries = append(s.entries, entry{source: s.source, id: id, m: m, noTx: noTx})
}

// Migration returns the migration added under id, or nil.
func (s *Set) Migration(id string) Migration {
	for _, e := range s.entries {
		if e.id == id {
			return e.m
		}
	}
	return nil
}

// AddFunc adds a migration written as two functions. down may be nil for
// a migration that can't be rolled back.
func (s *Set) AddFunc(id string, up, down func(s *Schema) error) {
	s.Add(id, Func(up, down))
}

type funcMigration struct{ up, down func(*Schema) error }

func (f funcMigration) Up(s *Schema) error { return f.up(s) }
func (f funcMigration) Down(s *Schema) error {
	if f.down == nil {
		return ErrIrreversible
	}
	return f.down(s)
}

// AddFS adds SQL migrations from dir in fsys, usually an embed.FS: each
// ID.up.sql file is a migration, and ID.down.sql (optional) undoes it.
// Statements are separated by semicolons and sent as written (see
// [Schema.Exec]). A first line of "-- anetos:no-transaction" runs the
// migration outside a transaction; "-- anetos:no-split" sends the whole
// file as one statement.
//
//	//go:embed sql/*.sql
//	var files embed.FS
//	func init() { must(All.AddFS(files, "sql")) }
func (s *Set) AddFS(fsys fs.FS, dir string) error {
	ups, err := fs.Glob(fsys, path.Join(dir, "*.up.sql"))
	if err != nil {
		return err
	}
	for _, up := range ups {
		id := strings.TrimSuffix(path.Base(up), ".up.sql")
		if !validID(id) || len(id) > 255 {
			return fmt.Errorf("migrate: %s: invalid migration ID %q", up, id)
		}
		if s.ids[id] {
			return fmt.Errorf("migrate: %s: migration %q is already in %s", up, id, s.source)
		}
		upSQL, err := fs.ReadFile(fsys, up)
		if err != nil {
			return err
		}
		var downSQL []byte
		if b, err := fs.ReadFile(fsys, path.Join(dir, id+".down.sql")); err == nil {
			downSQL = b
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		m := sqlMigration{up: string(upSQL), down: string(downSQL)}
		if directive(m.up, "no-transaction") {
			s.Add(id, NoTransaction(m))
		} else {
			s.Add(id, m)
		}
	}
	downs, _ := fs.Glob(fsys, path.Join(dir, "*.down.sql"))
	for _, down := range downs {
		id := strings.TrimSuffix(path.Base(down), ".down.sql")
		if !s.ids[id] {
			return fmt.Errorf("migrate: %s has no matching .up.sql", down)
		}
	}
	return nil
}

type sqlMigration struct{ up, down string }

func (m sqlMigration) Up(s *Schema) error { return s.execFile(m.up) }
func (m sqlMigration) Down(s *Schema) error {
	if strings.TrimSpace(m.down) == "" {
		return ErrIrreversible
	}
	return s.execFile(m.down)
}

// directive reports whether sql starts with "-- anetos:name" lines.
func directive(sql, name string) bool {
	for line := range strings.SplitSeq(sql, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-- anetos:") {
			return false
		}
		if strings.TrimSpace(strings.TrimPrefix(line, "-- anetos:")) == name {
			return true
		}
	}
	return false
}

func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
		if r > unicode.MaxASCII || !ok {
			return false
		}
	}
	return true
}

// Seeder fills the database with data: reference data for every
// environment, or sample data for development.
//
//	migrate.Seeder{Name: "users", Run: func(ctx context.Context) error {
//		return db.CreateMany(ctx, []models.User{…})
//	}}
type Seeder struct {
	Name string                          // for db:seed --seeder=name and the output
	Run  func(ctx context.Context) error // fills the tables, in a transaction
}
