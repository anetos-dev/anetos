// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// scanPlan maps the columns of a result set to fields of a struct type.
type scanPlan struct {
	value     bool         // T is a single value: scan the only column into it
	valueTime bool         // … and it is a time.Time
	dest      []scanTarget // one per result column
	names     []string     // result column names, for errors
}

type scanTarget struct {
	index []int // nil: discard the column
	json  bool
	time  bool // time.Time or *time.Time
}

type scanKey struct {
	typ  reflect.Type
	cols string
}

var scanPlans sync.Map // scanKey → *scanPlan

func planFor(t reflect.Type, cols []string) (*scanPlan, error) {
	key := scanKey{t, strings.Join(cols, "\x00")}
	if p, ok := scanPlans.Load(key); ok {
		return p.(*scanPlan), nil
	}
	p := &scanPlan{names: cols}
	if t.Kind() != reflect.Struct || isValueType(t) {
		if len(cols) != 1 {
			return nil, fmt.Errorf("db: scanning into %s needs exactly one column, got %d (%s)", t, len(cols), strings.Join(cols, ", "))
		}
		p.value = true
		p.valueTime = t == timeType || t == timePtrType
	} else {
		m, err := metaOf(t)
		if err != nil {
			return nil, err
		}
		p.dest = make([]scanTarget, len(cols))
		for i, name := range cols {
			j, ok := m.byName[name]
			if !ok {
				j, ok = m.byName[strings.ToLower(name)]
			}
			if ok {
				c := m.cols[j]
				p.dest[i] = scanTarget{index: c.index, json: c.json,
					time: c.typ == timeType || c.typ == timePtrType}
			}
		}
	}
	actual, _ := scanPlans.LoadOrStore(key, p)
	return actual.(*scanPlan), nil
}

// rowScanner scans rows of one result set into values of type T.
type rowScanner struct {
	plan     *scanPlan
	dest     []any
	discard  any
	fixTimes bool         // see needsTimeFix
	extra    map[int]*any // result columns that aren't fields, read by the caller
	times    []timeScanner
	jsons    []jsonScanner // reused from row to row, as dest is
}

func newRowScanner(d *DB, t reflect.Type, rows *sql.Rows) (*rowScanner, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	p, err := planFor(t, cols)
	if err != nil {
		return nil, err
	}
	s := &rowScanner{plan: p, dest: make([]any, len(cols)), fixTimes: needsTimeFix(d.dialect)}
	if s.fixTimes && (p.value && p.valueTime || slices.ContainsFunc(p.dest, func(t scanTarget) bool { return t.time })) {
		s.times = make([]timeScanner, len(cols))
	}
	if slices.ContainsFunc(p.dest, func(t scanTarget) bool { return t.json }) {
		s.jsons = make([]jsonScanner, len(cols))
	}
	return s, nil
}

// scan reads the current row into v, a settable value of the scanned type.
func (s *rowScanner) scan(rows *sql.Rows, v reflect.Value) error {
	if s.plan.value {
		s.dest[0] = v.Addr().Interface()
		if s.fixTimes && s.plan.valueTime {
			s.times[0].v = v
			s.dest[0] = &s.times[0]
		}
	} else {
		for i, t := range s.plan.dest {
			switch {
			case t.index == nil:
				s.dest[i] = &s.discard
				if p, ok := s.extra[i]; ok {
					s.dest[i] = p
				}
			case t.json:
				s.jsons[i].dst = fieldAlloc(v, t.index).Addr().Interface()
				s.dest[i] = &s.jsons[i]
			case t.time && s.fixTimes:
				s.times[i].v = fieldAlloc(v, t.index)
				s.dest[i] = &s.times[i]
			default:
				s.dest[i] = fieldAlloc(v, t.index).Addr().Interface()
			}
		}
	}
	if err := rows.Scan(s.dest...); err != nil {
		return s.explain(err)
	}
	return nil
}

// explain adds a hint to the error database/sql gives for NULL in a
// non-pointer field.
func (s *rowScanner) explain(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "converting NULL") {
		return fmt.Errorf("db: %w (a nullable column needs a pointer field such as *string, or sql.Null[T])", err)
	}
	return fmt.Errorf("db: scan: %w", err)
}

// jsonScanner decodes a JSON column into a field.
type jsonScanner struct{ dst any }

func (j *jsonScanner) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return nil
	case []byte:
		return json.Unmarshal(v, j.dst)
	case string:
		return json.Unmarshal([]byte(v), j.dst)
	}
	return fmt.Errorf("db: JSON column holds %T", src)
}

// needsTimeFix reports whether times read from d need timeScanner: SQLite
// returns text for expressions like MAX(created_at), and the PostgreSQL
// driver returns times in the local time zone. (The MySQL driver is
// configured to return UTC.)
func needsTimeFix(d Dialect) bool {
	n := d.Name()
	return n == "sqlite" || n == "postgres"
}

// timeScanner reads a time.Time or *time.Time field from a time value or
// text, in UTC, so times read back look the same on every database.
type timeScanner struct{ v reflect.Value }

// textTimeLayouts are the formats SQLite drivers write.
var textTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

func parseTextTime(s string) (time.Time, error) {
	for _, layout := range textTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("db: can't read %q as a time", s)
}

func (ts *timeScanner) Scan(src any) error {
	var t time.Time
	switch x := src.(type) {
	case nil:
		ts.v.SetZero()
		return nil
	case time.Time:
		t = x
	case string:
		var err error
		if t, err = parseTextTime(x); err != nil {
			return err
		}
	case []byte:
		var err error
		if t, err = parseTextTime(string(x)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("db: can't read %T as a time", src)
	}
	t = t.UTC()
	switch dst := ts.v.Addr().Interface().(type) { // no boxing of t
	case *time.Time:
		*dst = t
	case **time.Time:
		p := new(time.Time) // not &t, which would put every t on the heap
		*p = t
		*dst = p
	default:
		return fmt.Errorf("db: can't read a time into %s", ts.v.Type())
	}
	return nil
}

// fieldArg returns the query argument for column c of struct value v.
func fieldArg(v reflect.Value, c column) (any, error) {
	f := fieldOf(v, c.index)
	if !f.IsValid() {
		return nil, nil // behind a nil embedded pointer: NULL
	}
	if c.json {
		switch f.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
			if f.IsNil() {
				return nil, nil
			}
		}
		b, err := json.Marshal(f.Interface())
		if err != nil {
			return nil, fmt.Errorf("db: encode JSON column %s: %w", c.name, err)
		}
		return string(b), nil
	}
	return f.Interface(), nil
}

// collect runs a query and scans every row into a []T.
func collect[T any](d *DB, rows *sql.Rows, err error) ([]T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	s, err := newRowScanner(d, reflect.TypeFor[T](), rows)
	if err != nil {
		return nil, err
	}
	var out []T
	for rows.Next() {
		var zero T
		out = append(out, zero)
		if err := s.scan(rows, reflect.ValueOf(&out[len(out)-1]).Elem()); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []T{} // encodes as [] in JSON
	}
	return out, errors.Join(rows.Close())
}
