// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
)

// maxSelected is how many records a bulk action takes at once.
const maxSelected = 1000

// listPage is a resource's list, or its trash.
type listPage struct {
	Trash    bool
	Headers  []header
	Rows     []listRow
	Total    int64
	Current  int
	Last     int
	Prev     string
	Next     string
	Search   string
	CanQuery bool // the list has a search box
	Filters  []filterView
	Sort     string
	Action   string // the URL of bulk actions
	Bulk     []buttonView
	NewURL   string
	TrashURL string
	ListURL  string
	Empty    string
}

type header struct {
	Title  string
	URL    string // a link sorting by the column; "" if it can't sort
	Sorted string // "asc", "desc" or ""
}

type listRow struct {
	Key   string
	URL   string
	Cells []template.HTML
	// Buttons are the trash's restore and delete.
	Buttons []buttonView
}

type filterView struct {
	Name    string
	Title   string
	Choices []choiceView
}

// buttonView is a form button: an action, a bulk action, delete.
type buttonView struct {
	Name    string
	Title   string
	URL     string
	Confirm string
	Danger  bool
}

// parseKey reads a record's key as the model's key type.
func (r *res[T, F]) parseKey(raw string) (any, bool) {
	switch r.keyType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		return n, err == nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		return n, err == nil
	}
	return raw, raw != ""
}

// listed applies the list's search, filters and order to q.
func (r *res[T, F]) listed(c *web.Ctx, q *db.Q[T], lp *listPage) (*db.Q[T], error) {
	if s := strings.TrimSpace(c.Query("q")); s != "" && len(r.Search) > 0 {
		d, err := db.From(c)
		if err != nil {
			return nil, err
		}
		lp.Search = s
		pattern := "%" + likeEscaper.Replace(s) + "%"
		var ors []db.Expr
		for _, col := range r.Search {
			ors = append(ors, db.SQL("LOWER("+d.Dialect().QuoteIdent(col)+") LIKE LOWER(?) ESCAPE '!'", pattern))
		}
		q = q.Where(db.Or(ors...))
	}
	lp.CanQuery = len(r.Search) > 0
	for _, f := range r.Filters {
		v := c.Query(f.Name)
		fv := filterView{Name: f.Name, Title: f.Label}
		if fv.Title == "" {
			fv.Title = humanize(f.Name)
		}
		for _, ch := range f.Choices {
			fv.Choices = append(fv.Choices, choiceView{ch.Value, ch.Label, ch.Value == v})
		}
		if v != "" && slices.ContainsFunc(f.Choices, func(ch Choice) bool { return ch.Value == v }) {
			q = f.Apply(q, v)
		}
		lp.Filters = append(lp.Filters, fv)
	}
	sort := r.Sort
	if s := c.Query("sort"); s != "" && r.sortable(strings.TrimPrefix(s, "-")) {
		sort = s
	}
	lp.Sort = sort
	if col, desc := strings.TrimPrefix(sort, "-"), strings.HasPrefix(sort, "-"); col != "" {
		o := db.C(col).Asc()
		if desc {
			o = db.C(col).Desc()
		}
		q = q.OrderBy(o)
		if r.fields.pk != "" && col != r.fields.pk {
			q = q.OrderBy(db.C(r.fields.pk).Asc())
		}
	}
	return q, nil
}

// likeEscaper escapes LIKE's wildcards, with ! as the escape character
// (the same in every database, unlike \).
var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

func (r *res[T, F]) sortable(col string) bool {
	return slices.ContainsFunc(r.Columns, func(c Column[T]) bool { return c.Sortable && c.Column == col })
}

// headers returns the list's headings, with links sorting by them.
func (r *res[T, F]) headers(c *web.Ctx, sort string) []header {
	out := make([]header, len(r.Columns))
	for i, col := range r.Columns {
		h := header{Title: col.Label}
		if col.Sortable && col.Column != "" {
			next := col.Column
			switch sort {
			case col.Column:
				h.Sorted, next = "asc", "-"+col.Column
			case "-" + col.Column:
				h.Sorted = "desc"
			}
			v := c.Request().URL.Query()
			v.Set("sort", next)
			v.Del("page")
			h.URL = "?" + v.Encode()
		}
		out[i] = h
	}
	return out
}

// cells renders a record's columns.
func (r *res[T, F]) cells(c *web.Ctx, row T, cols []Column[T]) []template.HTML {
	rv := reflect.ValueOf(row)
	out := make([]template.HTML, len(cols))
	for i, col := range cols {
		var v any
		if col.Value != nil {
			v = col.Value(row)
		} else {
			v = r.fields.value(rv, col.Column)
		}
		out[i] = cell(c, v)
	}
	return out
}

func (r *res[T, F]) perPage() int {
	if r.PerPage > 0 {
		return r.PerPage
	}
	return r.p.cfg.PerPage
}

func (r *res[T, F]) list(c *web.Ctx, trash bool) error {
	n, _ := strconv.Atoi(c.Query("page"))
	lp := listPage{Trash: trash, ListURL: r.url("")}
	q := r.query(c)
	if trash {
		q = q.OnlyTrashed()
	}
	q, err := r.listed(c, q, &lp)
	if err != nil {
		return err
	}
	pg, err := q.Paginate(max(n, 1), r.perPage())
	if err != nil {
		return err
	}
	lp.Headers = r.headers(c, lp.Sort)
	lp.Total, lp.Current, lp.Last = pg.Total, pg.CurrentPage, pg.LastPage
	if pg.CurrentPage > 1 {
		lp.Prev = web.PageURL(c, pg.CurrentPage-1)
	}
	if pg.CurrentPage < pg.LastPage {
		lp.Next = web.PageURL(c, pg.CurrentPage+1)
	}
	for _, row := range pg.Data {
		k := r.keyText(row)
		lr := listRow{Key: k, URL: r.url("/" + k), Cells: r.cells(c, row, r.Columns)}
		if trash {
			lr.URL = ""
			if r.can(c, "delete") {
				lr.Buttons = []buttonView{
					{Name: "restore", Title: "Restore", URL: r.url("/" + k + "/restore")},
					{Name: "force-delete", Title: "Delete forever", URL: r.url("/" + k + "/force-delete"),
						Confirm: "Delete " + r.label(row) + " forever? This can't be undone.", Danger: true},
				}
			}
		}
		lp.Rows = append(lp.Rows, lr)
	}
	title := r.PluralLabel
	crumbs := r.crumbs()
	if trash {
		title = r.PluralLabel + ": trash"
		crumbs = r.crumbs(navItem{Title: "Trash"})
		lp.Empty = "The trash is empty."
	} else {
		crumbs = crumbs[:1]
		lp.Empty = "No " + strings.ToLower(r.PluralLabel) + " yet."
		if lp.Search != "" || c.Request().URL.RawQuery != "" {
			lp.Empty = "Nothing matches."
		}
		lp.Action = r.url("/bulk")
		for _, a := range r.BulkActions {
			if r.can(c, a.Permission) {
				lp.Bulk = append(lp.Bulk, buttonView{Name: a.Name, Title: a.Label, Confirm: a.Confirm, Danger: a.Danger})
			}
		}
		if r.in.delete && r.can(c, "delete") {
			lp.Bulk = append(lp.Bulk, buttonView{Name: "delete", Title: "Delete", Confirm: "Delete the selected " + strings.ToLower(r.PluralLabel) + "?", Danger: true})
		}
		if r.in.create && r.can(c, "create") {
			lp.NewURL = r.url("/new")
		}
		if r.in.soft && r.can(c, "delete") {
			lp.TrashURL = r.url("/trash")
		}
	}
	return r.p.render(c, "list", page{Title: title, Crumbs: crumbs, Data: lp})
}

func (r *res[T, F]) index(c *web.Ctx) error { return r.list(c, false) }

func (r *res[T, F]) trash(c *web.Ctx) error { return r.list(c, true) }

// showPage is a record's page.
type showPage struct {
	Lines    []line
	Sections []section
	EditURL  string
	Delete   *buttonView
	Actions  []buttonView
}

type line struct {
	Title string
	Value template.HTML
}

func (r *res[T, F]) show(c *web.Ctx) error {
	row, err := r.find(c, false)
	if err != nil {
		return err
	}
	cols := r.Details
	if len(cols) == 0 {
		cols = r.Columns
	}
	sp := showPage{}
	for i, v := range r.cells(c, row, cols) {
		sp.Lines = append(sp.Lines, line{cols[i].Label, v})
	}
	k := r.keyText(row)
	if r.in.editable && r.can(c, "update") && r.allowed(c, row, "update") {
		sp.EditURL = r.url("/" + k + "/edit")
	}
	if r.in.delete && r.can(c, "delete") && r.allowed(c, row, "delete") {
		msg := "Delete " + r.label(row) + "?"
		if !r.in.soft {
			msg += " This can't be undone."
		}
		sp.Delete = &buttonView{Name: "delete", Title: "Delete", URL: r.url("/" + k + "/delete"), Confirm: msg, Danger: true}
	}
	for _, a := range r.Actions {
		if (a.When == nil || a.When(row)) && r.can(c, a.Permission) && r.allowed(c, row, "action:"+a.Name) {
			sp.Actions = append(sp.Actions, buttonView{Name: a.Name, Title: a.Label, URL: r.url("/" + k + "/actions/" + a.Name), Confirm: a.Confirm, Danger: a.Danger})
		}
	}
	if r.hooks.sections != nil {
		if sp.Sections, err = r.hooks.sections(c, row); err != nil {
			return err
		}
	}
	if h, err := r.p.history(c, r.table, &row); err != nil {
		return err
	} else if h != nil {
		sp.Sections = append(sp.Sections, *h)
	}
	label := r.label(row)
	return r.p.render(c, "show", page{Title: label, Crumbs: r.crumbs(navItem{Title: label}), Data: sp})
}

func (r *res[T, F]) delete(c *web.Ctx) error {
	row, err := r.find(c, false)
	if err != nil {
		return err
	}
	if err := r.check(c, row, "delete"); err != nil {
		return refused(c, err, r.url("/"+r.keyText(row)))
	}
	if err := r.deleteRow(c, &row, !r.in.soft); err != nil {
		return err
	}
	msg := r.label(row) + " deleted."
	if r.in.soft {
		msg = r.label(row) + " moved to the trash."
	}
	return done(c, msg, r.url(""))
}

// deleteRow deletes row: soft, or for good (and then the removed hook, in
// the same transaction).
func (r *res[T, F]) deleteRow(ctx context.Context, row *T, forGood bool) error {
	if !forGood {
		return db.Delete(ctx, row)
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		if err := db.ForceDelete(ctx, row); err != nil {
			return err
		}
		if r.hooks.removed != nil {
			return r.hooks.removed(ctx, *row)
		}
		return nil
	})
}

func (r *res[T, F]) restore(c *web.Ctx) error {
	row, err := r.find(c, true)
	if err != nil {
		return err
	}
	if err := r.check(c, row, "delete"); err != nil {
		return refused(c, err, r.url("/trash"))
	}
	if err := db.Restore(c, &row); err != nil {
		return err
	}
	return done(c, r.label(row)+" restored.", r.url("/trash"))
}

func (r *res[T, F]) forceDelete(c *web.Ctx) error {
	row, err := r.find(c, true)
	if err != nil {
		return err
	}
	if err := r.check(c, row, "delete"); err != nil {
		return refused(c, err, r.url("/trash"))
	}
	if err := r.deleteRow(c, &row, true); err != nil {
		return err
	}
	return done(c, r.label(row)+" deleted forever.", r.url("/trash"))
}

// action runs a record's action.
func (r *res[T, F]) action(c *web.Ctx) error {
	i := slices.IndexFunc(r.Actions, func(a Action[T]) bool { return a.Name == c.Param("action") })
	if i < 0 {
		return web.Error(http.StatusNotFound, "")
	}
	a := r.Actions[i]
	if err := r.authorize(c, a.Permission); err != nil {
		return err
	}
	row, err := r.find(c, false)
	if err != nil {
		return err
	}
	to := r.url("/" + r.keyText(row))
	if a.When != nil && !a.When(row) {
		return failed(c, a.Label+" can't be done to "+r.label(row)+".", to)
	}
	if err := r.check(c, row, "action:"+a.Name); err != nil {
		return refused(c, err, to)
	}
	if (a.Danger || a.sensitive) && r.p.mustConfirm(c) {
		return r.p.askConfirm(c, r.p.back(c, to))
	}
	if err := a.Run(c, &row); err != nil {
		return refused(c, err, to)
	}
	if a.then != nil {
		to = a.then(c, row)
	}
	msg := a.Done
	if msg == "" {
		msg = a.Label + ": done."
	}
	return done(c, msg, to)
}

// bulk runs a bulk action, or deletes, on the selected records.
func (r *res[T, F]) bulk(c *web.Ctx) error {
	req := c.Request()
	if err := req.ParseForm(); err != nil {
		return web.Error(http.StatusBadRequest, "")
	}
	back := r.url("")
	if ref, err := url.Parse(req.PostForm.Get("_back")); err == nil && ref.Path == back && ref.Host == "" && ref.Scheme == "" {
		back = ref.String() // the list as it was: its page, filters and order
	}
	raw := req.PostForm["ids"]
	if len(raw) == 0 {
		return failed(c, "Select some "+strings.ToLower(r.PluralLabel)+" first.", back)
	}
	if len(raw) > maxSelected {
		return failed(c, fmt.Sprintf("Select at most %d %s.", maxSelected, strings.ToLower(r.PluralLabel)), back)
	}
	keys := make([]any, 0, len(raw))
	for _, s := range raw {
		k, ok := r.parseKey(s)
		if !ok {
			return web.Error(http.StatusBadRequest, "")
		}
		keys = append(keys, k)
	}
	name := req.PostForm.Get("action")
	if r.p.mustConfirm(c) {
		perm := ""
		if name == "delete" && r.in.delete {
			perm = "delete"
		} else if i := slices.IndexFunc(r.BulkActions, func(a BulkAction[T]) bool { return a.Name == name && a.Danger }); i >= 0 {
			perm = r.BulkActions[i].Permission
			if perm == "" {
				perm = "update"
			}
		}
		if perm != "" {
			// Allowed first: no password asked for what one can't do.
			if err := r.authorize(c, perm); err != nil {
				return err
			}
			return r.p.askConfirm(c, back)
		}
	}
	q := r.query(c).WhereKeys(keys...)
	if r.hooks.guard != nil || name == "delete" && !r.in.soft && r.hooks.removed != nil {
		// Record by record: the guard on each, the removed hook.
		return r.bulkEach(c, q, name, len(keys), back)
	}
	var (
		n     int64
		err   error
		title string
	)
	if name == "delete" {
		if !r.in.delete {
			return web.Error(http.StatusNotFound, "")
		}
		if err := r.authorize(c, "delete"); err != nil {
			return err
		}
		title = "Deleted"
		n, err = q.Delete()
	} else {
		i := slices.IndexFunc(r.BulkActions, func(a BulkAction[T]) bool { return a.Name == name })
		if i < 0 {
			return web.Error(http.StatusNotFound, "")
		}
		a := r.BulkActions[i]
		if err := r.authorize(c, a.Permission); err != nil {
			return err
		}
		title = a.Label
		n, err = a.Run(c, q)
	}
	if err != nil {
		if msg, ok := userError(err); ok {
			return failed(c, msg, back)
		}
		return err
	}
	return done(c, fmt.Sprintf("%s: %d of %d.", title, n, len(keys)), back)
}

// bulkEach runs a bulk action, or deletes, on the selected records the
// guard allows, one by one for deleting.
func (r *res[T, F]) bulkEach(c *web.Ctx, q *db.Q[T], name string, selected int, back string) error {
	op, title := "delete", "Deleted"
	var bulk *BulkAction[T]
	if name == "delete" {
		if !r.in.delete {
			return web.Error(http.StatusNotFound, "")
		}
		if err := r.authorize(c, "delete"); err != nil {
			return err
		}
	} else {
		i := slices.IndexFunc(r.BulkActions, func(a BulkAction[T]) bool { return a.Name == name })
		if i < 0 {
			return web.Error(http.StatusNotFound, "")
		}
		bulk = &r.BulkActions[i]
		if err := r.authorize(c, bulk.Permission); err != nil {
			return err
		}
		op, title = "bulk:"+name, bulk.Label
	}
	rows, err := q.Get()
	if err != nil {
		return err
	}
	var ok []T
	var refusal error
	for _, row := range rows {
		if err := r.check(c, row, op); err != nil {
			if _, isUser := userError(err); !isUser {
				return err
			}
			refusal = err
			continue
		}
		ok = append(ok, row)
	}
	var n int64
	err = db.Tx(c, func(ctx context.Context) error {
		if bulk != nil {
			keys := make([]any, len(ok))
			for i := range ok {
				_, keys[i], _ = db.KeyOf(&ok[i])
			}
			if len(keys) == 0 {
				return nil
			}
			var err error
			n, err = bulk.Run(ctx, r.query(ctx).WhereKeys(keys...))
			return err
		}
		for i := range ok {
			if err := r.deleteRow(ctx, &ok[i], !r.in.soft); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return refused(c, err, back)
	}
	msg := fmt.Sprintf("%s: %d of %d.", title, n, selected)
	if refusal != nil {
		m, _ := userError(refusal)
		msg += " Some were left: " + m
	}
	return done(c, msg, back)
}

// userError returns the message of a validation error, for the user.
func userError(err error) (string, bool) {
	var ve *validate.Errors
	if !errors.As(err, &ve) || ve.Len() == 0 {
		return "", false
	}
	var msgs []string
	for _, k := range ve.Keys() {
		msgs = append(msgs, ve.Get(k))
	}
	return strings.Join(msgs, " "), true
}

// failed flashes an error and sends the browser to to.
func failed(c *web.Ctx, msg, to string) error {
	flash(c, "admin.error", msg)
	return c.Redirect(http.StatusSeeOther, to)
}
