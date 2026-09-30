// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Model gives a struct an auto-increment ID and timestamps:
//
//	type Post struct {
//		db.Model
//		Title string `db:"title" json:"title"`
//	}
type Model struct {
	ID int64 `db:"id,pk" json:"id"`
	Timestamps
}

// Timestamps adds created_at and updated_at columns, set automatically by
// Create, Update and mass updates.
type Timestamps struct {
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// SoftDeletes makes Delete set deleted_at instead of removing the row, and
// hides deleted rows from queries unless WithTrashed or OnlyTrashed is used.
type SoftDeletes struct {
	DeletedAt *time.Time `db:"deleted_at" json:"deleted_at,omitempty"`
}

// Trashed reports whether the row has been soft-deleted.
func (s SoftDeletes) Trashed() bool { return s.DeletedAt != nil }

// Tabler lets a model choose its table name. Without it, the table is the
// snake_case plural of the type name (Post → posts, Category →
// categories).
type Tabler interface {
	TableName() string
}

// column is one mapped struct field.
type column struct {
	name     string
	index    []int
	typ      reflect.Type
	pk       bool
	json     bool // stored as JSON text
	readonly bool // never written (generated or database-defaulted columns)
}

// meta is what the db package knows about a struct type.
type meta struct {
	typ       reflect.Type
	table     string
	cols      []column
	byName    map[string]int // column name → index in cols
	pk        int            // index in cols, -1 if none
	autoPK    bool           // integer primary key, generated when zero
	createdAt int            // -1 if none
	updatedAt int
	deletedAt int // -1 unless SoftDeletes is embedded
}

var metas sync.Map // reflect.Type → *meta or error

var (
	timeType        = reflect.TypeFor[time.Time]()
	timestampsType  = reflect.TypeFor[Timestamps]()
	softDeletesType = reflect.TypeFor[SoftDeletes]()
	scannerType     = reflect.TypeFor[sql.Scanner]()
	valuerType      = reflect.TypeFor[driver.Valuer]()
)

// metaOf returns the metadata of struct type t, computed once.
func metaOf(t reflect.Type) (*meta, error) {
	if v, ok := metas.Load(t); ok {
		if err, isErr := v.(error); isErr {
			return nil, err
		}
		return v.(*meta), nil
	}
	m, err := buildMeta(t)
	var v any = m
	if err != nil {
		v = err
	}
	v, _ = metas.LoadOrStore(t, v)
	if err, isErr := v.(error); isErr {
		return nil, err
	}
	return v.(*meta), nil
}

func buildMeta(t reflect.Type) (*meta, error) {
	if t.Kind() != reflect.Struct || isValueType(t) {
		return nil, fmt.Errorf("db: %s is not a model struct", t)
	}
	m := &meta{typ: t, pk: -1, createdAt: -1, updatedAt: -1, deletedAt: -1, byName: map[string]int{}}
	if tb, ok := reflect.TypeAssert[Tabler](reflect.New(t)); ok {
		m.table = tb.TableName()
	} else {
		m.table = plural(snake(t.Name()))
	}
	if m.table == "" {
		return nil, fmt.Errorf("db: %s has no table name; add a TableName method", t)
	}
	if err := m.addFields(t, nil, map[reflect.Type]bool{}); err != nil {
		return nil, err
	}
	for _, p := range []*int{&m.createdAt, &m.updatedAt, &m.deletedAt} {
		if *p == -2 {
			*p = -1 // the embedded struct's columns were overridden
		}
	}
	if m.pk < 0 {
		if i, ok := m.byName["id"]; ok {
			m.pk = i
			m.cols[i].pk = true
		}
	}
	if m.pk >= 0 {
		switch m.cols[m.pk].typ.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			m.autoPK = true
		}
	}
	return m, nil
}

func (m *meta) addFields(t reflect.Type, index []int, seen map[reflect.Type]bool) error {
	if seen[t] {
		return fmt.Errorf("db: %s embeds itself", m.typ)
	}
	seen[t] = true
	defer delete(seen, t)
	for i := range t.NumField() {
		sf := t.Field(i)
		idx := append(slices.Clone(index), i)
		tag, hasTag := sf.Tag.Lookup("db")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		ft := sf.Type
		base := ft
		if base.Kind() == reflect.Pointer {
			base = base.Elem()
		}
		if sf.Anonymous && name == "" && base.Kind() == reflect.Struct && !isValueType(base) {
			if ft.Kind() == reflect.Pointer && !sf.IsExported() {
				continue // can't allocate it while scanning
			}
			switch base {
			case timestampsType:
				m.createdAt, m.updatedAt = -2, -2 // resolved below
			case softDeletesType:
				m.deletedAt = -2
			}
			if err := m.addFields(base, idx, seen); err != nil {
				return err
			}
			continue
		}
		if !sf.IsExported() {
			continue
		}
		if !hasTag && !isColumnType(ft) {
			continue // a relation or other non-column field
		}
		if name == "" {
			name = snake(sf.Name)
		}
		if _, dup := m.byName[name]; dup {
			return fmt.Errorf("db: %s: two fields map to column %q", m.typ, name)
		}
		c := column{name: name, index: idx, typ: ft}
		for o := range strings.SplitSeq(opts, ",") {
			switch strings.TrimSpace(o) {
			case "":
			case "pk":
				if m.pk >= 0 {
					return fmt.Errorf("db: %s: more than one pk field (composite keys aren't supported by model methods; use raw SQL)", m.typ)
				}
				c.pk = true
			case "json":
				c.json = true
			case "readonly":
				c.readonly = true
			default:
				return fmt.Errorf("db: %s.%s: unknown db tag option %q (known: pk, json, readonly)", m.typ, sf.Name, o)
			}
		}
		m.byName[name] = len(m.cols)
		if c.pk {
			m.pk = len(m.cols)
		}
		m.cols = append(m.cols, c)
		switch {
		case m.createdAt == -2 && name == "created_at":
			m.createdAt = len(m.cols) - 1
		case m.updatedAt == -2 && name == "updated_at":
			m.updatedAt = len(m.cols) - 1
		case m.deletedAt == -2 && name == "deleted_at":
			m.deletedAt = len(m.cols) - 1
		}
	}
	return nil
}

// isValueType reports whether a struct type is a single value (time,
// sql.Null*, or anything that scans itself) rather than a set of columns.
func isValueType(t reflect.Type) bool {
	return t == timeType || reflect.PointerTo(t).Implements(scannerType) || t.Implements(valuerType)
}

// isColumnType reports whether an untagged field is a column: anything but
// structs, pointers to structs and slices of structs, which are left for
// relations. []byte and value types are columns.
func isColumnType(t reflect.Type) bool {
	if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 {
		t = t.Elem()
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() != reflect.Struct || isValueType(t)
}

// insertable returns the columns written by INSERT: all but read-only ones,
// and the primary key when it is generated and zero.
func (m *meta) insertable(v reflect.Value) []int {
	out := make([]int, 0, len(m.cols))
	for i, c := range m.cols {
		if c.readonly {
			continue
		}
		if i == m.pk && m.autoPK && isZeroField(v, c.index) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// updatable returns the columns written by a model UPDATE. deleted_at is
// left alone: only Delete and Restore change it, so saving a stale copy of
// a row can't undelete it.
func (m *meta) updatable() []int {
	out := make([]int, 0, len(m.cols))
	for i, c := range m.cols {
		if c.readonly || i == m.pk || i == m.createdAt || i == m.deletedAt {
			continue
		}
		out = append(out, i)
	}
	return out
}

// fieldOf returns the field at index, or an invalid Value if it is behind
// a nil embedded pointer.
func fieldOf(v reflect.Value, index []int) reflect.Value {
	f, err := v.FieldByIndexErr(index)
	if err != nil {
		return reflect.Value{}
	}
	return f
}

// isZeroField reports whether the field at index is zero or behind a nil
// embedded pointer.
func isZeroField(v reflect.Value, index []int) bool {
	f := fieldOf(v, index)
	return !f.IsValid() || f.IsZero()
}

// fieldAlloc returns the field at index, allocating nil embedded pointers
// on the way.
func fieldAlloc(v reflect.Value, index []int) reflect.Value {
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v
}

// snake converts a Go name to snake_case: AuthorID → author_id,
// HTTPStatus → http_status, UserIDs → user_ids.
func snake(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			// A plural "s" after an acronym (IDs, URLs) stays with it.
			pluralAcronym := nextLower && runes[i+1] == 's' && (i+2 == len(runes) || unicode.IsUpper(runes[i+2]))
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower && !pluralAcronym) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// plural returns the English plural of a snake_case table name, changing
// only its last word: category → categories, box → boxes, person →
// people. Use a TableName method for anything it gets wrong.
func plural(s string) string {
	head, word := "", s
	if i := strings.LastIndexByte(s, '_'); i >= 0 {
		head, word = s[:i+1], s[i+1:]
	}
	if p, ok := irregular[word]; ok {
		return head + p
	}
	switch {
	case word == "" || strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") &&
		!strings.HasSuffix(word, "us") && !strings.HasSuffix(word, "is"):
		return s // already plural (or ends in s like "news")
	case strings.HasSuffix(word, "y") && len(word) > 1 && !strings.ContainsRune("aeiou", rune(word[len(word)-2])):
		return head + word[:len(word)-1] + "ies"
	case strings.HasSuffix(word, "is") && len(word) > 3: // axis → axes, analysis → analyses
		return head + word[:len(word)-2] + "es"
	case strings.HasSuffix(word, "ss"), strings.HasSuffix(word, "x"), strings.HasSuffix(word, "z"),
		strings.HasSuffix(word, "ch"), strings.HasSuffix(word, "sh"), strings.HasSuffix(word, "us"):
		return head + word + "es"
	}
	return head + word + "s"
}

var irregular = map[string]string{
	"person": "people", "man": "men", "woman": "women", "child": "children",
	"mouse": "mice", "goose": "geese", "foot": "feet", "tooth": "teeth",
	"datum": "data", "medium": "media", "criterion": "criteria",
	"analysis": "analyses", "index": "indices", "status": "statuses",
	"quiz": "quizzes", "leaf": "leaves", "life": "lives", "knife": "knives",
	"wife": "wives", "half": "halves", "info": "info", "equipment": "equipment",
	"news": "news", "series": "series", "species": "species", "sheep": "sheep",
	"fish": "fish", "deer": "deer", "data": "data", "metadata": "metadata",
	"alias": "aliases", "canvas": "canvases", "gas": "gases", "lens": "lenses",
	"hero": "heroes", "potato": "potatoes", "tomato": "tomatoes", "echo": "echoes",
	"matrix": "matrices", "vertex": "vertices", "appendix": "appendices",
	"axis": "axes", "this": "this",
}
