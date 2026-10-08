// SPDX-License-Identifier: Apache-2.0

package web

import "net/http"

// MiddlewareDoc describes what a middleware adds to the routes it wraps,
// for tools that describe an API (package web/openapi): the credentials
// it asks for and the errors it may answer with. A middleware gives it
// with [Documented]; [Router.Routes] reports it in RouteInfo.Middleware.
type MiddlewareDoc struct {
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
// Only the middleware of a route's routers (Group, With, Use) is seen,
// not the router's global middleware ([Router.UseGlobal]). The handler
// returned serves exactly as h.
func Documented(h http.Handler, doc MiddlewareDoc) http.Handler {
	return &documented{h, doc}
}

type documented struct {
	http.Handler
	doc MiddlewareDoc
}
