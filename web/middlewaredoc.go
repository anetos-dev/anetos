// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// MiddlewareDoc describes a middleware of a route, for tools that
// describe an app (route:list --json, package web/openapi): its name,
// and what it adds to the routes it wraps, the credentials it asks for
// and the errors it may answer with. A middleware gives the latter with
// [Documented]; [Router.Routes] reports every middleware of a route in
// RouteInfo.Middleware, the undocumented ones with their name only.
type MiddlewareDoc struct {
	// Name names the middleware as Go code does: "web.CSRF",
	// "session.Manager.Middleware", "auth.Auth.Require",
	// "routes.adminOnly". Unless the middleware's Documented gives one,
	// it's the name of the function that makes it: a closure written
	// inside routes.Register is "routes.Register", one assigned to a
	// package-level variable "routes.(func literal)". A middleware that
	// returns another's documented handler as it is reports that one's.
	Name string
	// Detail is what the middleware was made with, when that matters to
	// a reader: a rate limit's name ("login"), the permissions rbac
	// requires, a body limit's bytes, a timeout. "" otherwise.
	Detail string
	// Security names the security scheme a request must pass: "bearer"
	// for an API token in the Authorization header (package auth's
	// Require), "" for none.
	Security string
	// Scopes are what the request's credentials must allow, such as an
	// API token's abilities ("*").
	Scopes []string
	// Responses are the error statuses the middleware may answer with,
	// and when: 401 "The request has no valid API token.", 429…
	Responses map[int]string
	// Inner describes the middleware it runs, for one that runs others
	// for some requests: [FirstParty]'s session and CSRF middleware.
	// [RouteInfo.Uses] looks in it too.
	Inner []MiddlewareDoc
}

// Documented returns h, the handler a middleware returns, with doc: the
// routes it wraps report doc in [RouteInfo].Middleware.
//
//	func partnerKey(next http.Handler) http.Handler {
//		return web.Documented(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//			// … 401 without a valid X-Partner-Key header
//		}), web.MiddlewareDoc{Security: "partnerKey", // an openapi.Config.SecuritySchemes entry
//			Responses: map[int]string{http.StatusUnauthorized: "No valid partner key."}})
//	}
//
// A route sees the middleware of its routers (Group, With, Use); the
// router's global middleware ([Router.UseGlobal]) is described by
// [Router.GlobalMiddleware]. The handler returned serves exactly as h.
func Documented(h http.Handler, doc MiddlewareDoc) http.Handler {
	return &documented{h, doc}
}

// describe returns what mw says of itself, made around next: its
// Documented doc (a middleware returning a handler already seen in
// prev, such as next as it is, said nothing), with its name.
func describe(mw Middleware, next http.Handler, prev *documented) (MiddlewareDoc, http.Handler) {
	h := mw(next)
	var doc MiddlewareDoc
	if d, ok := h.(*documented); ok && d != prev {
		doc = d.doc
	}
	if doc.Name == "" {
		doc.Name = middlewareName(mw)
	}
	return doc, h
}

var (
	closureSuffix = regexp.MustCompile(`(\.func\d+|\.\d+)+$`)
	receiver      = regexp.MustCompile(`\(\*?([A-Za-z0-9_]+)\)\.`)
	typeArgs      = regexp.MustCompile(`\[[^\]]*\]`)
)

// middlewareName names the function that makes mw, as Go code does:
// "web.CSRF" for web.CSRF()'s closure, "auth.Auth.Require" for the
// method value a.Require.
func middlewareName(mw Middleware) string {
	f := runtime.FuncForPC(reflect.ValueOf(mw).Pointer())
	if f == nil {
		return ""
	}
	name := strings.TrimSuffix(f.Name(), "-fm")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = typeArgs.ReplaceAllString(name, "") // auth.(*Auth[...]).Require
	name = closureSuffix.ReplaceAllString(name, "")
	if pkg, rest, _ := strings.Cut(name, "."); rest == "init" || strings.HasPrefix(rest, "glob.") {
		// A function literal assigned to a package-level variable.
		return pkg + ".(func literal)"
	}
	return receiver.ReplaceAllString(name, "$1.")
}

// GlobalMiddleware describes the router's global middleware
// ([Router.UseGlobal]), outermost first, as [RouteInfo].Middleware
// describes a route's.
func (r *Router) GlobalMiddleware() []MiddlewareDoc {
	r.core.mu.RLock()
	mws := slices.Clone(r.core.global)
	r.core.mu.RUnlock()
	return describeAll(mws)
}

// describeAll describes mws, outermost first, made around a handler
// that does nothing.
func describeAll(mws []Middleware) []MiddlewareDoc {
	docs := make([]MiddlewareDoc, len(mws))
	var h http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	var last *documented
	for i, mw := range slices.Backward(mws) {
		docs[i], h = describe(mw, h, last)
		last, _ = h.(*documented)
	}
	return docs
}

type documented struct {
	http.Handler
	doc MiddlewareDoc
}
