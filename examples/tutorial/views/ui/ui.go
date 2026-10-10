// Package ui is the app's interface components, in the markup of its
// CSS framework (anetos): the layout and the pages call them, and use
// plain HTML elements, without classes, for the rest. Each framework has
// this package with its stylesheet (public/static/app.css), so another
// one's restyles every page that calls the components. The package is the
// app's: change a component's markup freely, keeping its signature for
// the pages that call it.
package ui

import (
	"context"
	"strings"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
)

// Variant is a button's kind: Primary (the default), Secondary, Danger
// or Ghost. A Size may follow it: @ui.LinkButton(url, ui.Secondary,
// ui.Small).
type Variant uint

// The variants.
const (
	Primary   Variant = iota // the page's main action
	Secondary                // another action
	Danger                   // an action that deletes, or can't be undone
	Ghost                    // a quiet action, as in the header
)

// Size is a button's size or width, given after its variant.
type Size uint

// The sizes.
const (
	Small     Size = iota + 1 // smaller, as in a table's row
	FullWidth                 // as wide as its container
)

// Tone is what a badge's or a message's color means.
type Tone uint

// The tones.
const (
	Neutral Tone = iota
	Success
	Warning
	Error
	Info
)

// Option is an option of a Select: its value, and its name for people.
type Option struct {
	Value, Label string
}

// Pages is what Pagination shows: where the list is, and the links to
// the pages before and after it ("" at the ends).
type Pages struct {
	Label                string // the links' name for screen readers: "Pages"
	Status               string // "Page 2 of 5"
	Prev, Next           string // the URLs of the previous and next pages
	PrevLabel, NextLabel string // their links' text
}

// PagesOf is the Pages of a list's page, with its texts: the links'
// name for screen readers, where the list is, and the previous and next
// links' text. The links keep the query's other parameters (a search, a
// filter: web.PageURL).
func PagesOf[T any](ctx context.Context, page db.Page[T], label, status, prev, next string) Pages {
	p := Pages{Label: label, Status: status, PrevLabel: prev, NextLabel: next}
	if page.HasPrev() {
		// Past the last page (a link kept from when there were more),
		// back to the last.
		p.Prev = web.PageURL(ctx, min(page.CurrentPage-1, max(page.LastPage, 1)))
	}
	if page.HasNext() {
		p.Next = web.PageURL(ctx, page.CurrentPage+1)
	}
	return p
}

// methodOverride is the method a form sends in its _method field
// (view.MethodField): PUT, PATCH or DELETE; "" for POST and GET.
func methodOverride(method string) string {
	switch m := strings.ToUpper(method); m {
	case "PUT", "PATCH", "DELETE":
		return m
	}
	return ""
}

// isGet reports whether a form's method is GET.
func isGet(method string) bool { return strings.EqualFold(method, "GET") }
