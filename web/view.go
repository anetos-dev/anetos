// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"net/http"
	"sync"

	"anetos.dev/anetos/view"
)

var renderBufs = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// Render writes comp (a templ component or any [view.Component]) as an
// HTML response with the given status. The component renders with c as
// its context, so view helpers and [URL] work inside it. Output is
// buffered: a rendering error becomes an error page instead of half a
// page.
func (c *Ctx) Render(status int, comp view.Component) error {
	buf := renderBufs.Get().(*bytes.Buffer)
	defer func() {
		if buf.Cap() <= 1<<20 {
			buf.Reset()
			renderBufs.Put(buf)
		}
	}()
	if err := comp.Render(c, buf); err != nil {
		return err
	}
	return c.Blob(status, "text/html; charset=utf-8", buf.Bytes())
}

// View responds 200 OK with the rendered component, for typed handlers:
//
//	func (h Posts) Show(c *web.Ctx, in PostID) (web.Responder, error) {
//		…
//		return web.View(views.Post(post)), nil
//	}
func View(comp view.Component) Responder {
	return ResponderFunc(func(c *Ctx) error { return c.Render(http.StatusOK, comp) })
}

// HTMX describes an htmx request (https://htmx.org/reference/#request_headers).
type HTMX struct {
	Request        bool   // HX-Request: sent by htmx
	Boosted        bool   // HX-Boosted: a boosted link or form
	HistoryRestore bool   // HX-History-Restore-Request
	Target         string // HX-Target: id of the target element
	Trigger        string // HX-Trigger: id of the triggering element
	TriggerName    string // HX-Trigger-Name
	CurrentURL     string // HX-Current-URL: the browser's URL
}

// IsHTMX reports whether htmx sent the request, so a handler can render a
// fragment instead of the whole page:
//
//	if c.IsHTMX() {
//		return c.Render(http.StatusOK, views.PostList(posts))
//	}
//	return c.Render(http.StatusOK, views.PostsPage(posts))
//
// Boosted requests (hx-boost) expect the whole page; check
// c.HTMX().Boosted when you use them. IsHTMX adds "Vary: HX-Request" to
// the response, so caches keep the fragment and the page apart.
func (c *Ctx) IsHTMX() bool {
	addVary(c.w.Header(), "HX-Request")
	return c.r.Header.Get("HX-Request") == "true"
}

// HTMX returns the htmx request headers. Like IsHTMX, it adds "Vary:
// HX-Request".
func (c *Ctx) HTMX() HTMX {
	addVary(c.w.Header(), "HX-Request")
	h := c.r.Header
	return HTMX{
		Request:        h.Get("HX-Request") == "true",
		Boosted:        h.Get("HX-Boosted") == "true",
		HistoryRestore: h.Get("HX-History-Restore-Request") == "true",
		Target:         h.Get("HX-Target"),
		Trigger:        h.Get("HX-Trigger"),
		TriggerName:    h.Get("HX-Trigger-Name"),
		CurrentURL:     h.Get("HX-Current-URL"),
	}
}
