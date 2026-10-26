// SPDX-License-Identifier: Apache-2.0

// Package inertia is the server side of Inertia's protocol
// (https://inertiajs.com, v3): pages written in Vue, React or Svelte,
// which Inertia's client renders, with the routes, handlers, sessions,
// validation and redirects in Go. A handler renders a page component with
// its props instead of HTML; the first visit gets the root view's HTML
// holding the page, and the client's visits after it the page object as
// JSON.
//
// [New] sets it up in setup, with the root view (a templ component that
// loads the frontend with vite.Tags and places [App]):
//
//	if _, err := inertia.New(app, views.Root()); err != nil {
//		return nil, err
//	}
//
// routes/web.go runs [Middleware] after the session and CSRF (whose
// XSRF-TOKEN cookie Inertia's client sends back), and makes the error
// pages the frontend's:
//
//	g := r.Group("", sessions.Middleware, web.CSRF(web.WithXSRFCookie()), inertia.Middleware)
//	r.ErrorPages(inertia.ErrorPages("Error"))
//
// and a handler renders a [Page], declared once with its props' type:
//
//	var postsIndex = inertia.NewPage[PostsIndex]("Posts/Index")
//
//	func (h *Posts) Index(c *web.Ctx) error {
//		return postsIndex.Render(c, PostsIndex{Posts: posts, Stats: inertia.Defer(h.stats)})
//	}
//
// [Prop] fields are the protocol's special props: optional, deferred,
// merged, once and always props. A failed form post redirects back with
// its field errors, which the page gets in its errors prop; [Flash],
// [Location], [ClearHistory] and [EncryptHistory] cover the rest of the
// protocol. Server-side rendering, infinite scroll and Precognition
// aren't supported yet.
//
// See the guide "Pages in Vue, React or Svelte with Inertia"
// (https://docs.anetos.dev/guides/inertia/).
package inertia
