// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"errors"
	"fmt"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"anetos.dev/anetos/internal/naming"
)

// FindRoot returns the directory of the go.mod file at or above dir.
func FindRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod in this directory or above it; run anetos in a project (anetos new creates one)")
		}
		dir = parent
	}
}

type makeData struct {
	Type, Words, Path, Table, ID string
	API                          bool // the project is an API project (IsAPI)
}

// IsAPI reports whether the project at root is an API project, as
// anetos new --stack=api writes one: it has routes/api.go and no
// routes/web.go. A web project that adds routes/api.go stays a web
// project, and an API project with templ views (for emails) stays an API
// project.
func IsAPI(root string) bool {
	if _, err := os.Stat(filepath.Join(root, "routes", "api.go")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(root, "routes", "web.go"))
	return errors.Is(err, fs.ErrNotExist)
}

// RefuseAPI returns an error when the project at root is an API project
// ([IsAPI]) and cmd, a generator of HTML pages, can't run there; why
// says what to do instead or when it's coming. Otherwise it returns nil.
func RefuseAPI(root, cmd, why string) error {
	if !IsAPI(root) {
		return nil
	}
	return fmt.Errorf("%s writes HTML pages, and this is an API project (routes/api.go, no routes/web.go); %s", cmd, why)
}

// typeName checks and normalizes a name given on the command line into an
// exported Go identifier: "blog-post" and "blog_post" become "BlogPost".
func typeName(name string) (string, error) {
	id := identifier(name)
	if !nameRe.MatchString(name) || !token.IsIdentifier(id) {
		return "", fmt.Errorf("%q is not a usable name; use letters and digits, like Post or BlogPost", name)
	}
	return id, nil
}

func (d makeData) write(root, tmpl, rel string) (string, error) {
	out, err := render("templates/make/"+tmpl, "", d)
	if err != nil {
		return "", err
	}
	if err := writeNew(filepath.Join(root, filepath.FromSlash(rel)), out, 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// MakeHandler writes app/handlers/<name>.go with a handler type, which
// answers JSON in an API project.
func MakeHandler(root, name string) (string, error) {
	t, err := typeName(name)
	if err != nil {
		return "", err
	}
	snake := naming.Snake(t)
	d := makeData{Type: t, Words: strings.ReplaceAll(snake, "_", " "), Path: strings.ReplaceAll(snake, "_", "-"), API: IsAPI(root)}
	return d.write(root, "handler.go.tmpl", "app/handlers/"+snake+".go")
}

// MakeModel writes app/models/<name>.go with a model embedding db.Model.
func MakeModel(root, name string) (string, error) {
	t, err := typeName(name)
	if err != nil {
		return "", err
	}
	snake := naming.Snake(t)
	d := makeData{Type: t, Table: naming.Plural(snake)}
	return d.write(root, "model.go.tmpl", "app/models/"+snake+".go")
}

// ModelTable returns the table a model named name maps to.
func ModelTable(name string) string {
	t, _ := typeName(name)
	return naming.Plural(naming.Snake(t))
}

// MakeAgent writes app/agents/<name>.go with an ai.Agent and a tool.
func MakeAgent(root, name string) (string, error) {
	t, err := typeName(name)
	if err != nil {
		return "", err
	}
	snake := naming.Snake(t)
	d := makeData{Type: t, Path: strings.ReplaceAll(snake, "_", "-"), ID: strings.ToLower(t[:1]) + t[1:]}
	return d.write(root, "agent.go.tmpl", "app/agents/"+snake+".go")
}

// MakeMiddleware writes app/middleware/<name>.go with a middleware
// function.
func MakeMiddleware(root, name string) (string, error) {
	t, err := typeName(name)
	if err != nil {
		return "", err
	}
	return makeData{Type: t, API: IsAPI(root)}.write(root, "middleware.go.tmpl", "app/middleware/"+naming.Snake(t)+".go")
}

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	createRe = regexp.MustCompile(`^create_([a-z0-9_]+?)(?:_table)?$`)
	alterRe  = regexp.MustCompile(`^.*_(?:to|from|in|on)_([a-z0-9_]+?)(?:_table)?$`) // the last to/from/in/on
)

// MakeMigration writes database/migrations/<timestamp>_<name>.go. A name
// like create_posts_table creates a table; add_x_to_posts_table alters
// one; anything else gets empty functions.
func MakeMigration(root, name string, now time.Time) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "database", "migrations")); err != nil {
		return "", errors.New("no database/migrations directory; anetos new creates it, with the All set new migrations add themselves to")
	}
	t, err := typeName(name)
	if err != nil {
		return "", err
	}
	snake := naming.Snake(t)
	// IDs sort in creation order, also for migrations made within a second.
	ts := now.UTC().Truncate(time.Second)
	if last, ok := latestMigration(filepath.Join(root, "database", "migrations")); ok && !ts.After(last) {
		ts = last.Add(time.Second)
	}
	d := makeData{ID: ts.Format(idLayout) + "_" + snake}
	tmpl := "migration_blank.go.tmpl"
	if m := createRe.FindStringSubmatch(snake); m != nil {
		d.Table, tmpl = m[1], "migration_create.go.tmpl"
	} else if m := alterRe.FindStringSubmatch(snake); m != nil {
		d.Table, tmpl = m[1], "migration_alter.go.tmpl"
	}
	return d.write(root, tmpl, "database/migrations/"+d.ID+".go")
}

const idLayout = "2006_01_02_150405"

// latestMigration returns the newest timestamp among the migration files
// in dir.
func latestMigration(dir string) (time.Time, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	var last time.Time
	for _, e := range entries {
		if len(e.Name()) < len(idLayout) {
			continue
		}
		if t, err := time.Parse(idLayout, e.Name()[:len(idLayout)]); err == nil && t.After(last) {
			last = t
		}
	}
	return last, !last.IsZero()
}
