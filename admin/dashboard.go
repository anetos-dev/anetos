// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
)

// Widget is a part of the admin's first page ([Panel.Dashboard]).
type Widget struct {
	// Title is its heading.
	Title string
	// Permission is what a user needs to see it; [Access] if empty.
	Permission rbac.Permission
	// Load returns what it shows, for the request's context. An error
	// is shown in its place (and logged); the rest of the page works.
	Load func(ctx context.Context) (Content, error)
}

// Content is what a widget shows, in this order: figures, a bar chart, a
// table, HTML of its own, and a link.
type Content struct {
	// Stats are figures: "Users 1,204".
	Stats []Stat
	// Bars is a bar chart, a bar a label (days, say), oldest first.
	Bars []Bar
	// Table is a table.
	Table *Table
	// HTML is anything else, a templ component say.
	HTML view.Component
	// Link leads to more: "Every failed job".
	Link *Link
}

// Stat is a figure of a widget.
type Stat struct {
	// Label says what it counts.
	Label string
	// Value is the figure, formatted.
	Value string
	// Hint is a line under it ("in 30 days"), optional.
	Hint string
	// Warn shows it as needing attention.
	Warn bool
}

// Bar is a bar of a widget's chart.
type Bar struct {
	// Label names it ("Oct 6"), shown on hover and below the chart.
	Label string
	// Value is its height, 0 or more.
	Value float64
}

// Table is a widget's table.
type Table struct {
	// Headers are the columns' headings.
	Headers []string
	// Rows are the rows' cells, as text.
	Rows [][]string
	// Links are the rows' links ("" for none), if any.
	Links []string
}

// Link is a widget's link. A URL without a leading / is the admin's own
// page: "jobs" is /admin/jobs.
type Link struct {
	// Label is its text.
	Label string
	// URL is where it goes.
	URL string
}

// Dashboard adds widgets to the admin's first page, in order, above the
// resources. Call it before [Panel.Mount].
//
//	p.Dashboard(admin.SignUps[models.User]("created_at"), admin.QueueHealth(q))
func (p *Panel) Dashboard(widgets ...Widget) error {
	if p.mounted {
		return errors.New("admin: Dashboard after Mount")
	}
	for _, w := range widgets {
		if w.Title == "" || w.Load == nil {
			return errors.New("admin: a widget needs a Title and Load")
		}
		// The built-in widgets' are declared here, in case their pages
		// aren't added; the others must be already.
		if slices.Contains(widgetPermissions, w.Permission) {
			if err := p.reg.Declare(w.Permission); err != nil {
				return err
			}
		} else if w.Permission != "" && !p.reg.Declared(w.Permission) {
			return fmt.Errorf("admin: widget %q: permission %q isn't declared", w.Title, w.Permission)
		}
	}
	p.widgets = append(p.widgets, widgets...)
	return nil
}

// widgetPermissions are the built-in widgets' permissions.
var widgetPermissions = []rbac.Permission{"admin.jobs.view", ViewActivity}

// widgetView is a widget as the page shows it.
type widgetView struct {
	Title  string
	Error  string
	Stats  []Stat
	Bars   []barView
	First  string // the first and last bars' labels, under the chart
	Last   string
	Width  int // the chart's width, in its units
	Table  *tableView
	HTML   template.HTML
	Link   *Link
	Prefix string // the admin's path, for its own links
}

type barView struct {
	Label  string
	Value  string
	X, Y   int
	Height int
}

type tableView struct {
	Headers []string
	Rows    []tableRow
}

type tableRow struct {
	Cells []string
	URL   string
}

// chartHeight is the bar chart's height, in its units.
const chartHeight = 60

// panelKey is the context key of the Panel, for built-in widgets.
type panelKey struct{}

// panelFrom returns the Panel of a widget's context.
func panelFrom(ctx context.Context) *Panel {
	p, _ := ctx.Value(panelKey{}).(*Panel)
	return p
}

// loadWidgets loads the widgets the user may see.
func (p *Panel) loadWidgets(c *web.Ctx) []widgetView {
	var out []widgetView
	ctx := context.WithValue(c, panelKey{}, p)
	for _, w := range p.widgets {
		perm := w.Permission
		if perm == "" {
			perm = Access
		}
		if !rbac.Can(c, perm) {
			continue
		}
		v := widgetView{Title: w.Title}
		content, err := loadWidget(ctx, w)
		if err != nil {
			p.app.Logger().ErrorContext(c, "admin: loading a widget", "widget", w.Title, "error", err)
			v.Error = "It couldn't be loaded."
			out = append(out, v)
			continue
		}
		v.Stats = content.Stats
		if len(content.Bars) > 0 {
			top := 0.0
			for i, b := range content.Bars {
				if !(b.Value > 0) { // NaN and negatives are 0
					content.Bars[i].Value = 0
				}
				top = math.Max(top, content.Bars[i].Value)
			}
			for i, b := range content.Bars {
				h := 0
				if top > 0 {
					h = int(math.Round(b.Value / top * chartHeight))
				}
				if b.Value > 0 && h == 0 {
					h = 1
				}
				v.Bars = append(v.Bars, barView{Label: b.Label, Value: number(b.Value), X: i * 10, Y: chartHeight - h, Height: h})
			}
			v.Width = len(content.Bars) * 10
			v.First, v.Last = content.Bars[0].Label, content.Bars[len(content.Bars)-1].Label
		}
		if t := content.Table; t != nil {
			tv := &tableView{Headers: t.Headers}
			for i, r := range t.Rows {
				row := tableRow{Cells: r}
				if i < len(t.Links) {
					row.URL = p.link(t.Links[i])
				}
				tv.Rows = append(tv.Rows, row)
			}
			v.Table = tv
		}
		if content.HTML != nil {
			s, err := view.String(c, content.HTML)
			if err != nil {
				p.app.Logger().ErrorContext(c, "admin: rendering a widget", "widget", w.Title, "error", err)
				v.Error = "It couldn't be shown."
			}
			v.HTML = template.HTML(s) //nolint:gosec // the component's own markup
		}
		if content.Link != nil {
			if u := p.link(content.Link.URL); u != "" {
				v.Link = &Link{Label: content.Link.Label, URL: u}
			}
		}
		out = append(out, v)
	}
	return out
}

// loadWidget loads w, a panic in it an error: the rest of the page works.
func loadWidget(ctx context.Context, w Widget) (c Content, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("admin: widget %q panicked: %v", w.Title, v)
		}
	}()
	return w.Load(ctx)
}

// link resolves a widget's link: without a leading /, the admin's page,
// "" if it has no such page.
func (p *Panel) link(u string) string {
	if u == "" || strings.HasPrefix(u, "/") || strings.Contains(u, "://") {
		return u
	}
	name, _, _ := strings.Cut(u, "/")
	name, _, _ = strings.Cut(name, "?")
	if p.byName[name] == nil {
		return ""
	}
	return p.base + "/" + u
}

// number formats a figure: whole numbers with thousands separators,
// others with two decimals.
func number(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return thousands(int64(f))
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}

func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// days returns the last n days' starts, oldest first, in UTC.
func days(now time.Time, n int) []time.Time {
	today := now.UTC().Truncate(24 * time.Hour)
	out := make([]time.Time, n)
	for i := range n {
		out[i] = today.AddDate(0, 0, i-n+1)
	}
	return out
}

// perDay sums agg ("COUNT(*)") over the rows of q a day for the last n
// days (UTC), by column, a timestamp: one query, grouped by the database.
func perDay[T any](ctx context.Context, q *db.Q[T], column string, n int, agg string) ([]Bar, error) {
	d, err := db.From(ctx)
	if err != nil {
		return nil, err
	}
	col := d.Dialect().QuoteIdent(column)
	var day string
	switch d.Dialect().Name() {
	case "postgres":
		day = "to_char(" + col + " AT TIME ZONE 'UTC', 'YYYY-MM-DD')"
	case "mysql":
		day = "DATE_FORMAT(" + col + ", '%Y-%m-%d')"
	default:
		day = "strftime('%Y-%m-%d', " + col + ")"
	}
	ds := days(anetos.Now(ctx), n)
	type count struct {
		Day string  `db:"anetos_day"`
		N   float64 `db:"anetos_n"`
	}
	// Grouped by the alias, named so no column of T's is.
	rows, err := db.Select[count](q.Where(db.C(column).Gte(ds[0])).GroupBy("anetos_day"), day+" AS anetos_day", "COALESCE("+agg+", 0) AS anetos_n")
	if err != nil {
		return nil, err
	}
	byDay := map[string]float64{}
	for _, r := range rows {
		byDay[r.Day] = r.N
	}
	out := make([]Bar, n)
	for i, t := range ds {
		out[i] = Bar{Label: t.Format("Jan 2"), Value: byDay[t.Format(time.DateOnly)]}
	}
	return out, nil
}

// SignUps is a widget of model T's records, users say: how many, how
// many created in the last 7 and 30 days, and a bar a day for 30 days
// (UTC), from column, their creation time ("created_at").
func SignUps[T any](title, column string) Widget {
	return Widget{Title: title, Load: func(ctx context.Context) (Content, error) {
		total, err := db.Query[T](ctx).Count()
		if err != nil {
			return Content{}, err
		}
		now := anetos.Now(ctx)
		week, err := db.Query[T](ctx).Where(db.C(column).Gte(now.AddDate(0, 0, -7))).Count()
		if err != nil {
			return Content{}, err
		}
		month, err := db.Query[T](ctx).Where(db.C(column).Gte(now.AddDate(0, 0, -30))).Count()
		if err != nil {
			return Content{}, err
		}
		bars, err := perDay(ctx, db.Query[T](ctx), column, 30, "COUNT(*)")
		if err != nil {
			return Content{}, err
		}
		return Content{Stats: []Stat{
			{Label: "In all", Value: thousands(total)},
			{Label: "New", Value: thousands(week), Hint: "in 7 days"},
			{Label: "New", Value: thousands(month), Hint: "in 30 days"},
		}, Bars: bars}, nil
	}}
}
