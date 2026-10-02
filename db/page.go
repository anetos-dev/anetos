// SPDX-License-Identifier: Apache-2.0

package db

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
)

// Page is one page of results from [Q.Paginate]. Its JSON form follows
// Laravel's paginator: data, current_page, per_page, total, last_page.
type Page[T any] struct {
	Data        []T   `json:"data"`         // the page's rows; empty, not nil, past the end
	CurrentPage int   `json:"current_page"` // 1-based
	PerPage     int   `json:"per_page"`     // rows per page
	Total       int64 `json:"total"`        // rows on all pages
	LastPage    int   `json:"last_page"`    // number of the last page; 1 when there are no rows
}

// HasMore reports whether there are pages after this one.
func (p Page[T]) HasMore() bool { return p.CurrentPage < p.LastPage }

// HasPrev reports whether there are pages before this one.
func (p Page[T]) HasPrev() bool { return p.CurrentPage > 1 }

// DefaultPerPage is used when Paginate or CursorPaginate get perPage < 1.
const DefaultPerPage = 15

// Paginate returns page (1-based) of the results, perPage rows each, with
// the total count. Pages past the end are empty. Limit and Offset set on
// the query are replaced.
//
// perPage often comes from the request; bound it (for example with
// validate:"max:100") so a client can't ask for a million rows.
func (q *Q[T]) Paginate(page, perPage int) (Page[T], error) {
	page = max(page, 1)
	if perPage < 1 {
		perPage = DefaultPerPage
	}
	base := q.clone()
	base.hasLimit, base.limit, base.offset = false, 0, 0
	total, err := base.Count()
	if err != nil {
		return Page[T]{}, err
	}
	p := Page[T]{CurrentPage: page, PerPage: perPage, Total: total,
		LastPage: max(1, int((total+int64(perPage)-1)/int64(perPage)))}
	if int64(page-1)*int64(perPage) >= total {
		p.Data = []T{}
		return p, nil
	}
	p.Data, err = base.Limit(perPage).Offset((page - 1) * perPage).Get()
	return p, err
}

// CursorPage is one page of results from [Q.CursorPaginate].
type CursorPage[T any] struct {
	Data       []T    `json:"data"`        // the page's rows
	PerPage    int    `json:"per_page"`    // rows per page
	NextCursor string `json:"next_cursor"` // "" on the last page
	PrevCursor string `json:"prev_cursor"` // "" on the first page
}

// ErrInvalidCursor is returned by CursorPaginate for a cursor it didn't
// produce. It reports status 400 to the web package.
var ErrInvalidCursor error = invalidCursor{}

type invalidCursor struct{}

func (invalidCursor) Error() string   { return "db: invalid pagination cursor" }
func (invalidCursor) HTTPStatus() int { return http.StatusBadRequest }

type cursorData struct {
	Values []json.RawMessage `json:"v"`
	Prev   bool              `json:"p,omitempty"`
}

// CursorPaginate returns perPage rows after (or before) cursor, which is
// "" for the first page or a NextCursor/PrevCursor from a previous page.
// Unlike Paginate it stays fast deep into large tables and doesn't skip or
// repeat rows when rows are added, but it can't jump to a page number.
//
// The query's OrderBy terms must be plain columns (not OrderRaw) whose
// values aren't NULL; the primary key is added as a tie-breaker, and used
// alone when there is no OrderBy.
func (q *Q[T]) CursorPaginate(cursor string, perPage int) (CursorPage[T], error) {
	if q.err != nil {
		return CursorPage[T]{}, q.err
	}
	if perPage < 1 {
		perPage = DefaultPerPage
	}
	if q.search != nil || q.similar != nil {
		return CursorPage[T]{}, errors.New("db: CursorPaginate can't page through Search, Similar or Hybrid results, which are ordered by relevance; use Paginate")
	}
	orders, cols, err := q.cursorOrders()
	if err != nil {
		return CursorPage[T]{}, err
	}
	base := q.clone()
	base.orders = orders
	base.hasLimit, base.limit, base.offset = false, 0, 0

	var cur cursorData
	if cursor != "" {
		if cur, err = decodeCursor(cursor, len(orders)); err != nil {
			return CursorPage[T]{}, err
		}
		values := make([]any, len(orders))
		for i, c := range cols {
			p := reflect.New(c.typ)
			if err := json.Unmarshal(cur.Values[i], p.Interface()); err != nil {
				return CursorPage[T]{}, ErrInvalidCursor
			}
			values[i] = p.Elem().Interface()
		}
		base = base.Where(keyset(orders, values, cur.Prev))
		if cur.Prev {
			for i := range base.orders {
				base.orders[i].desc = !base.orders[i].desc
			}
		}
	}

	rows, err := base.Limit(perPage + 1).Get()
	if err != nil {
		return CursorPage[T]{}, err
	}
	more := len(rows) > perPage
	rows = rows[:min(len(rows), perPage)]
	if cur.Prev {
		slices.Reverse(rows)
	}
	page := CursorPage[T]{Data: rows, PerPage: perPage}
	if len(rows) == 0 {
		return page, nil
	}
	hasNext := more || cur.Prev
	hasPrev := cursor != "" && (!cur.Prev || more)
	if hasNext {
		if page.NextCursor, err = encodeCursor(rows[len(rows)-1], cols, false); err != nil {
			return page, err
		}
	}
	if hasPrev {
		if page.PrevCursor, err = encodeCursor(rows[0], cols, true); err != nil {
			return page, err
		}
	}
	return page, nil
}

// cursorOrders returns the query's orders plus the primary key, and the
// model columns they refer to.
func (q *Q[T]) cursorOrders() ([]Order, []column, error) {
	orders := slices.Clone(q.orders)
	var cols []column
	hasPK := false
	for _, o := range orders {
		if o.raw != nil {
			return nil, nil, fmt.Errorf("db: CursorPaginate needs column orderings, not OrderRaw")
		}
		name := o.col
		if table, col, ok := strings.Cut(name, "."); ok {
			if table != q.m.table {
				return nil, nil, fmt.Errorf("db: CursorPaginate can't order by %s: not a column of %s", name, q.m.table)
			}
			name = col
		}
		i, ok := q.m.byName[name]
		if !ok {
			return nil, nil, fmt.Errorf("db: CursorPaginate can't order by %s: not a column of %s", o.col, q.m.typ)
		}
		cols = append(cols, q.m.cols[i])
		hasPK = hasPK || i == q.m.pk
	}
	if !hasPK {
		if q.m.pk < 0 {
			return nil, nil, fmt.Errorf("db: CursorPaginate needs a primary key or an OrderBy on unique columns")
		}
		pk := q.m.cols[q.m.pk]
		orders = append(orders, Order{col: q.qualified(pk.name)})
		cols = append(cols, pk)
	}
	return orders, cols, nil
}

// keyset is the condition for rows after values in the given order (or
// before them when prev is set): (a > x) OR (a = x AND b > y) …
func keyset(orders []Order, values []any, prev bool) Expr {
	ors := make([]Expr, len(orders))
	for i, o := range orders {
		ands := make([]Expr, 0, i+1)
		for j := range i {
			ands = append(ands, cmpExpr{orders[j].col, "=", values[j]})
		}
		op := ">"
		if o.desc != prev {
			op = "<"
		}
		ands = append(ands, cmpExpr{o.col, op, values[i]})
		ors[i] = And(ands...)
	}
	return Or(ors...)
}

func encodeCursor[T any](row T, cols []column, prev bool) (string, error) {
	v := reflect.ValueOf(row)
	cur := cursorData{Prev: prev}
	for _, c := range cols {
		f := fieldOf(v, c.index)
		var x any
		if f.IsValid() {
			x = f.Interface()
		}
		b, err := json.Marshal(x)
		if err != nil {
			return "", fmt.Errorf("db: encode cursor: %w", err)
		}
		cur.Values = append(cur.Values, b)
	}
	b, err := json.Marshal(cur)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeCursor(s string, n int) (cursorData, error) {
	var cur cursorData
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &cur) != nil || len(cur.Values) != n {
		return cursorData{}, ErrInvalidCursor
	}
	return cur, nil
}
