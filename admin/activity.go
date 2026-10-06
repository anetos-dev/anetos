// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
)

// ViewActivity is the permission to see the audit log in the admin:
// [Activity]'s pages, records' history, [RecentActivity].
const ViewActivity rbac.Permission = "admin.activity.view"

// activityPage is how many entries a page of the activity shows.
const activityPage = 50

// Activity adds the activity pages: the audit log's entries and bulk
// writes, newest first, filtered by who, what, which kind of record,
// which record and when, with a page per entry. The pages of a resource
// whose model the log tracks then show a record's history. Permission:
// [ViewActivity]. The app must keep an audit log (audit.ForApp).
func Activity(p *Panel) error {
	if p.reg == nil {
		return errors.New("admin: Activity needs the app's roles and permissions (rbac.ForApp)")
	}
	p.activity = true
	return p.add(&activityRes{p: p, in: resInfo{Name: "activity", Title: "Activity", Singular: "Entry", custom: []string{"view"}}})
}

// activityRes is the activity pages.
type activityRes struct {
	p  *Panel
	in resInfo
}

func (r *activityRes) info() *resInfo { return &r.in }

// count isn't shown: counting the log is slow, and says little.
func (r *activityRes) count(context.Context) (int64, error) { return -1, nil }

func (r *activityRes) mount(g *web.Router) {
	g = g.With(rbac.Require(ViewActivity))
	g.Get("/", r.index).Name("admin.activity.index")
	g.Get("/{id}", r.entry).Name("admin.activity.show")
	g.Get("/bulk/{id}", r.bulkOp).Name("admin.activity.bulk")
}

func (r *activityRes) url(suffix string) string { return r.p.base + "/activity" + suffix }

// activityFilter is the list's filters, from the query.
type activityFilter struct {
	Kind, Who, Action, Type, ID, From, To string
	before                                int64
}

func filterOf(c *web.Ctx) activityFilter {
	q := c.Request().URL.Query()
	f := activityFilter{Kind: q.Get("kind"), Who: strings.TrimSpace(q.Get("who")), Action: strings.TrimSpace(q.Get("action")),
		Type: strings.TrimSpace(q.Get("type")), ID: strings.TrimSpace(q.Get("id")), From: q.Get("from"), To: q.Get("to")}
	if f.Kind != "bulk" {
		f.Kind = ""
	}
	f.before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	return f
}

// conds returns the filters' conditions. A bulk write matches a record's
// ID if it touched the record.
func (f activityFilter) conds(ctx context.Context, entries bool) []db.Expr {
	var out []db.Expr
	if f.Who != "" {
		typ, id, ok := strings.Cut(f.Who, ":")
		if !ok {
			typ, id = "user", f.Who
			if f.Who == "system" {
				typ, id = "system", ""
			}
		}
		out = append(out, db.C("actor_type").Eq(typ), db.C("actor_id").Eq(id))
	}
	if f.Action != "" {
		out = append(out, db.C("action").Eq(f.Action))
	}
	if f.Type != "" {
		out = append(out, db.C("subject_type").Eq(f.Type))
	}
	switch {
	case f.ID != "" && entries:
		out = append(out, db.C("subject_id").Eq(f.ID))
	case f.ID != "":
		sub := "SELECT bulk_id FROM audit_bulk_items WHERE subject_id = ?"
		args := []any{f.ID}
		if f.Type != "" {
			sub += " AND subject_type = ?"
			args = append(args, f.Type)
		}
		out = append(out, db.SQL("id IN ("+sub+")", args...))
	}
	loc := anetos.Location(ctx)
	if t, err := time.ParseInLocation(time.DateOnly, f.From, loc); err == nil {
		out = append(out, db.C("occurred_at").Gte(t.UTC()))
	}
	if t, err := time.ParseInLocation(time.DateOnly, f.To, loc); err == nil {
		out = append(out, db.C("occurred_at").Lt(t.AddDate(0, 0, 1).UTC()))
	}
	if f.before > 0 {
		out = append(out, db.C("id").Lt(f.before))
	}
	return out
}

// eventRow is a line of the activity.
type eventRow struct {
	ID       int64
	URL      string
	When     string
	Who      string
	WhoURL   string
	ActingAs string
	Action   string
	Record   string
	RecordTo string
	Rows     string // bulk writes: how many rows
	Via      string
}

// activityList is the activity page.
type activityList struct {
	Filter  activityFilter
	Rows    []eventRow
	Older   string
	Entries string
	Bulk    string
	Newest  string // the first page, while on a later one
}

func (r *activityRes) index(c *web.Ctx) error {
	f := filterOf(c)
	al := activityList{Filter: f}
	q := c.Request().URL.Query()
	q.Del("before")
	q.Del("kind")
	al.Entries = "?" + q.Encode()
	q.Set("kind", "bulk")
	al.Bulk = "?" + q.Encode()
	var last int64
	if f.Kind == "bulk" {
		ops, err := db.Query[audit.BulkOp](c).Where(f.conds(c, false)...).OrderBy(db.C("id").Desc()).Limit(activityPage + 1).Get()
		if err != nil {
			return err
		}
		more := len(ops) > activityPage
		ops = ops[:min(len(ops), activityPage)]
		rows := make([]eventRow, len(ops))
		for i, o := range ops {
			rows[i] = eventRow{ID: o.ID, URL: r.url("/bulk/" + strconv.FormatInt(o.ID, 10)), When: timeText(c, o.OccurredAt),
				Who: actorText(o.ActorType, o.ActorID), ActingAs: o.ActingAs, Action: o.Action, Record: o.SubjectType,
				Rows: thousands(o.RowCount), Via: via(o.ViaKind, o.ViaName)}
			last = o.ID
		}
		r.p.label(c, rows)
		al.Rows = rows
		if more {
			al.Older = r.older(c, last)
		}
	} else {
		es, err := db.Query[audit.Entry](c).Where(f.conds(c, true)...).OrderBy(db.C("id").Desc()).Limit(activityPage + 1).Get()
		if err != nil {
			return err
		}
		more := len(es) > activityPage
		es = es[:min(len(es), activityPage)]
		rows := make([]eventRow, len(es))
		for i, e := range es {
			rows[i] = r.p.entryRow(c, e)
			last = e.ID
		}
		r.p.label(c, rows)
		al.Rows = rows
		if more {
			al.Older = r.older(c, last)
		}
	}
	if f.before > 0 {
		al.Newest = al.Entries
		if f.Kind == "bulk" {
			al.Newest = al.Bulk
		}
	}
	return r.p.render(c, "activity", page{Title: "Activity", Crumbs: []navItem{{Title: r.p.cfg.Title, URL: r.p.URL()}}, Data: al})
}

// older is the link to the page after the entry last.
func (r *activityRes) older(c *web.Ctx, last int64) string {
	q := c.Request().URL.Query()
	q.Set("before", strconv.FormatInt(last, 10))
	return "?" + q.Encode()
}

// entryRow makes the line of an entry.
func (p *Panel) entryRow(c context.Context, e audit.Entry) eventRow {
	row := eventRow{ID: e.ID, URL: p.base + "/activity/" + strconv.FormatInt(e.ID, 10), When: timeText(c, e.OccurredAt),
		Who: actorText(e.ActorType, e.ActorID), ActingAs: e.ActingAs, Action: e.Action, Via: via(e.ViaKind, e.ViaName)}
	if e.SubjectType != "" {
		row.Record = e.SubjectType + " #" + e.SubjectID
		row.RecordTo = p.recordURL(e.SubjectType, e.SubjectID)
	}
	return row
}

// label names the users among rows' actors, and links them for those who
// may see the users' pages.
func (p *Panel) label(ctx context.Context, rows []eventRow) {
	if p.userLabels == nil {
		return
	}
	var ids []string
	for _, r := range rows {
		if id, ok := strings.CutPrefix(r.Who, "user:"); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	names, err := p.userLabels(ctx, ids)
	if err != nil {
		p.app.Logger().WarnContext(ctx, "admin: naming the activity's users", "error", err)
		return // the IDs are shown instead
	}
	link := false
	if u := p.byName[p.usersName]; u != nil {
		link = rbac.Can(ctx, u.info().perm("view"))
	}
	for i, r := range rows {
		if id, ok := strings.CutPrefix(r.Who, "user:"); ok {
			if n := names[id]; n != "" {
				rows[i].Who = n
			}
			if link {
				rows[i].WhoURL = p.base + "/" + p.usersName + "/" + url.PathEscape(id)
			}
		}
	}
}

// recordURL returns the page of a record of table in the admin, "" if no
// resource shows it.
func (p *Panel) recordURL(table, id string) string {
	for _, r := range p.res {
		if t, ok := r.(interface{ tableName() string }); ok && t.tableName() == table {
			return p.base + "/" + r.info().Name + "/" + url.PathEscape(id)
		}
	}
	return ""
}

func actorText(typ, id string) string {
	if id == "" {
		return typ
	}
	return typ + ":" + id
}

func via(kind, name string) string {
	if kind == "" {
		return ""
	}
	return kind + " " + name
}

// change is a column of an entry's changes.
type change struct {
	Column, Old, New string
}

// valueText shows a logged value: text as is, others as JSON.
func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func (r *activityRes) entry(c *web.Ctx) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return db.ErrNotFound
	}
	e, err := db.Find[audit.Entry](c, id)
	if err != nil {
		return err
	}
	rows := []eventRow{r.p.entryRow(c, e)}
	r.p.label(c, rows)
	var changes []change
	for _, col := range e.Changes.Fields() {
		old, hadOld := e.Changes.Old[col]
		nu, hasNew := e.Changes.New[col]
		ch := change{Column: col, Old: valueText(old), New: valueText(nu)}
		if !hadOld {
			ch.Old = ""
		}
		if !hasNew {
			ch.New = ""
		}
		changes = append(changes, ch)
	}
	var props string
	if len(e.Properties) > 0 {
		b, err := json.MarshalIndent(e.Properties, "", "  ")
		if err != nil {
			return err
		}
		props = string(b)
	}
	data := struct {
		Event      eventRow
		Entry      audit.Entry
		Changes    []change
		Properties string
		History    string
	}{Event: rows[0], Entry: e, Changes: changes, Properties: props}
	if e.SubjectType != "" {
		data.History = r.url("?type=" + url.QueryEscape(e.SubjectType) + "&id=" + url.QueryEscape(e.SubjectID))
	}
	title := fmt.Sprintf("Entry %d", e.ID)
	return r.p.render(c, "entry", page{Title: title, Crumbs: []navItem{{Title: r.p.cfg.Title, URL: r.p.URL()},
		{Title: "Activity", URL: r.url("")}, {Title: title}}, Data: data})
}

// bulkRow is a row a bulk write touched.
type bulkRow struct {
	Key, URL, Before, After string
}

func (r *activityRes) bulkOp(c *web.Ctx) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return db.ErrNotFound
	}
	o, err := db.Find[audit.BulkOp](c, id)
	if err != nil {
		return err
	}
	keys, err := db.Pluck(db.Query[audit.BulkItem](c).Where(db.C("bulk_id").Eq(o.ID)).OrderBy(db.C("subject_id").Asc()).Limit(200),
		db.Col[string]("subject_id"))
	if err != nil {
		return err
	}
	before, after := map[string]string{}, map[string]string{}
	for _, row := range o.Before {
		before[row.Key] = valueText(row.Values)
	}
	for _, row := range o.After {
		after[row.Key] = valueText(row.Values)
	}
	var rows []bulkRow
	for _, k := range keys {
		rows = append(rows, bulkRow{Key: k, URL: r.p.recordURL(o.SubjectType, k), Before: before[k], After: after[k]})
	}
	ev := []eventRow{{Who: actorText(o.ActorType, o.ActorID), ActingAs: o.ActingAs, Action: o.Action, Record: o.SubjectType,
		When: timeText(c, o.OccurredAt), Rows: thousands(o.RowCount), Via: via(o.ViaKind, o.ViaName)}}
	r.p.label(c, ev)
	data := struct {
		Event       eventRow
		Op          audit.BulkOp
		Condition   string
		Assignments string
		Rows        []bulkRow
		More        bool
	}{Event: ev[0], Op: o, Rows: rows, More: o.RowCount > int64(len(rows))}
	if o.Condition.SQL != "" {
		data.Condition = o.Condition.SQL
		if len(o.Condition.Args) > 0 {
			data.Condition += "  " + valueText(o.Condition.Args)
		}
	}
	if len(o.Assignments) > 0 {
		data.Assignments = valueText(o.Assignments)
	}
	title := fmt.Sprintf("Bulk write %d", o.ID)
	return r.p.render(c, "bulkop", page{Title: title, Crumbs: []navItem{{Title: r.p.cfg.Title, URL: r.p.URL()},
		{Title: "Activity", URL: r.url("?kind=bulk")}, {Title: title}}, Data: data})
}

// historyData is the history section's.
type historyData struct {
	Rows  []eventRow
	All   string
	Error string
}

// historyLimit is how many events a record's history shows.
const historyLimit = 10

// history is a record's history section: its latest events in the log,
// for those who may see the activity, of models the log tracks. When the
// log can't be read, the section says so (and the error is logged): the
// record's page still works.
func (p *Panel) history(c *web.Ctx, table string, row any) (*section, error) {
	if !p.activity || !rbac.Can(c, ViewActivity) || !audit.Tracked(c, table) {
		return nil, nil
	}
	subject, err := audit.SubjectOf(row)
	if err != nil {
		return nil, err
	}
	events, _, err := audit.History(c, subject, historyLimit+1, "")
	if err != nil {
		p.app.Logger().ErrorContext(c, "admin: reading a record's history", "table", table, "id", subject.ID, "error", err)
		body, err := part("history", historyData{Error: "It couldn't be loaded."})
		if err != nil {
			return nil, err
		}
		return &section{Title: "History", Body: body}, nil
	}
	more := len(events) > historyLimit
	events = events[:min(len(events), historyLimit)]
	var rows []eventRow
	for _, ev := range events {
		if ev.Entry != nil {
			rows = append(rows, p.entryRow(c, *ev.Entry))
			continue
		}
		o := ev.Bulk
		rows = append(rows, eventRow{ID: o.ID, URL: p.base + "/activity/bulk/" + strconv.FormatInt(o.ID, 10), When: timeText(c, o.OccurredAt),
			Who: actorText(o.ActorType, o.ActorID), ActingAs: o.ActingAs, Action: o.Action + " (bulk, " + thousands(o.RowCount) + " rows)",
			Via: via(o.ViaKind, o.ViaName)})
	}
	p.label(c, rows)
	data := historyData{Rows: rows}
	if more {
		data.All = p.base + "/activity?type=" + url.QueryEscape(subject.Type) + "&id=" + url.QueryEscape(subject.ID)
	}
	body, err := part("history", data)
	if err != nil {
		return nil, err
	}
	return &section{Title: "History", Body: body}, nil
}

// RecentActivity is a widget of the audit log's latest n entries, for
// those who may see the activity ([Activity] adds its pages).
func RecentActivity(n int) Widget {
	return Widget{Title: "Recent activity", Permission: ViewActivity, Load: func(ctx context.Context) (Content, error) {
		p := panelFrom(ctx)
		if p == nil {
			return Content{}, errors.New("admin: RecentActivity outside the admin")
		}
		es, err := db.Query[audit.Entry](ctx).OrderBy(db.C("id").Desc()).Limit(n).Get()
		if err != nil {
			return Content{}, err
		}
		rows := make([]eventRow, len(es))
		for i, e := range es {
			rows[i] = p.entryRow(ctx, e)
		}
		p.label(ctx, rows)
		t := &Table{Headers: []string{"When", "Who", "What", "Record"}}
		for _, row := range rows {
			t.Rows = append(t.Rows, []string{row.When, row.Who, row.Action, row.Record})
			if p.activity {
				t.Links = append(t.Links, row.URL)
			}
		}
		return Content{Table: t, Link: &Link{Title: "All activity", URL: "activity"}}, nil
	}}
}
