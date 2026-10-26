// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"slices"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/internal/securitycheck"
)

// RouteRule returns a rule of the security:check command that checks
// each route of the app's server ([NewServer]): rule's Run is check,
// called with every route, whose Middleware has the router's global
// middleware first ([Router.GlobalMiddleware]) and then the route's.
// A finding without a Where is the route's ("POST /posts"), without a
// File the place that registered it:
//
//	app.AddSecurityRule(web.RouteRule(anetos.SecurityRule{ID: "billing.webhook-signature",
//		Summary: "Payment webhooks without the signature check", OWASP: anetos.OWASPAccessControl},
//		func(rt web.RouteInfo) []anetos.SecurityFinding {
//			if strings.HasPrefix(rt.Pattern, "/webhooks/") && !rt.Uses("billing.VerifySignature") {
//				return []anetos.SecurityFinding{{Severity: anetos.Problem, Message: "…"}}
//			}
//			return nil
//		}))
func RouteRule(rule anetos.SecurityRule, check func(RouteInfo) []anetos.SecurityFinding) anetos.SecurityRule {
	rule.Run = func(_ context.Context, app *anetos.App) []anetos.SecurityFinding {
		srv, ok := anetos.Lookup[*Server](app)
		if !ok || srv == nil {
			return nil
		}
		global := srv.router.GlobalMiddleware()
		var out []anetos.SecurityFinding
		for _, rt := range srv.router.Routes() {
			rt.Middleware = append(slices.Clip(global), rt.Middleware...)
			for _, f := range check(rt) {
				if f.Where == "" {
					f.Where = rt.String()
				}
				if f.File == "" {
					f.File, f.Line = rt.File, rt.Line
				}
				out = append(out, f)
			}
		}
		return out
	}
	return rule
}

// String is the route as reports name it: "POST /posts", "ANY /files",
// "GET admin.example.com/".
func (rt RouteInfo) String() string {
	m := rt.Method
	if m == "" {
		m = "ANY"
	}
	return m + " " + rt.Host + rt.Pattern
}

// Uses reports whether one of the route's middleware has the name
// ([MiddlewareDoc.Name]): "web.CSRF", "auth.Auth.Require".
func (rt RouteInfo) Uses(name string) bool {
	var uses func(d MiddlewareDoc) bool
	uses = func(d MiddlewareDoc) bool { return d.Name == name || slices.ContainsFunc(d.Inner, uses) }
	return slices.ContainsFunc(rt.Middleware, uses)
}

// changes reports whether the route's method may change data.
func (rt RouteInfo) changes() bool {
	switch rt.Method {
	case "", "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// loggedIn reports whether the route needs a logged-in user: auth's
// Require, or rbac's, which refuses guests.
func (rt RouteInfo) loggedIn() bool {
	return rt.Uses("auth.Auth.Require") || rt.Uses("rbac.Require") || rt.Uses("rbac.RequireIn") || rt.Uses("auth.RequireAbilities")
}

// securityRules are web's rules of the security check (design D328),
// each with its section of the guide.
func securityRules() []anetos.SecurityRule {
	rules := []anetos.SecurityRule{
		RouteRule(anetos.SecurityRule{ID: "route.csrf", Summary: "Forms' routes without CSRF protection", OWASP: anetos.OWASPAccessControl},
			func(rt RouteInfo) []anetos.SecurityFinding {
				if !rt.changes() || !rt.Uses("session.Manager.Middleware") || rt.Uses("web.CSRF") {
					return nil
				}
				return []anetos.SecurityFinding{{Severity: anetos.Problem,
					Message: "it changes data with the session's user and has no CSRF protection: another site's page can send it as the logged-in user; add web.CSRF() to its group, after the session middleware"}}
			}),
		RouteRule(anetos.SecurityRule{ID: "route.login", Summary: "Admin routes without a login", OWASP: anetos.OWASPAccessControl},
			func(rt RouteInfo) []anetos.SecurityFinding {
				admin := rt.Pattern == "/admin" || strings.HasPrefix(rt.Pattern, "/admin/") || strings.HasPrefix(rt.Name, "admin.") ||
					strings.HasPrefix(rt.Host, "admin.")
				if !admin || rt.Public != "" || rt.loggedIn() {
					return nil
				}
				return []anetos.SecurityFinding{{Severity: anetos.Problem,
					Message: "an admin route without a login: anyone can open it; put it behind auth's Require (and rbac.Require), or mark it .Public(\"why\") if it's open on purpose"}}
			}),
		RouteRule(anetos.SecurityRule{ID: "route.debug", Summary: "Debug endpoints without a login", OWASP: anetos.OWASPMisconfiguration},
			func(rt RouteInfo) []anetos.SecurityFinding {
				if !rt.debug() || rt.loggedIn() {
					return nil
				}
				return []anetos.SecurityFinding{{Severity: anetos.Problem,
					Message: "Go's debug endpoints (pprof, expvar) without a login: anyone can read the app's memory and settings, or slow it down; remove them in production, or put them behind auth's Require and rbac.Require"}}
			}),
		RouteRule(anetos.SecurityRule{ID: "route.rate-limit", Summary: "Account routes that send email without a rate limit", OWASP: anetos.OWASPAuthentication},
			func(rt RouteInfo) []anetos.SecurityFinding {
				if rt.Method != "POST" || rt.Uses("ratelimit.Middleware") {
					return nil
				}
				for _, p := range []string{"/register", "/forgot-password", "/email/verification-notification"} {
					if strings.HasSuffix(rt.Pattern, p) {
						return []anetos.SecurityFinding{{Severity: anetos.Warning,
							Message: "it creates accounts or sends email, and has no rate limit: a script can fill the database or send floods of mail in your name; add ratelimit.Middleware (make:auth's routes have one)"}}
					}
				}
				return nil
			}),
		RouteRule(anetos.SecurityRule{ID: "route.api-auth", Level: anetos.SecurityAdvanced, Summary: "API routes that change data without authentication", OWASP: anetos.OWASPAccessControl},
			func(rt RouteInfo) []anetos.SecurityFinding {
				api := strings.HasPrefix(rt.Pattern, "/api/") || rt.Uses("web.JSONErrors")
				if !api || !rt.changes() || rt.Public != "" || rt.loggedIn() || publicAccountRoute(rt.Name) {
					return nil
				}
				return []anetos.SecurityFinding{{Severity: anetos.Warning,
					Message: "an API route that changes data, with no authentication: any client can call it; put it behind auth's Require, or mark it .Public(\"why\")"}}
			}),
	}
	for i := range rules {
		rules[i].Docs = securitycheck.RuleDocs(rules[i].ID)
	}
	return rules
}

// debug reports whether the route may serve Go's debug endpoints
// (/debug/pprof/, /debug/vars): a pattern under /debug whose literal
// part leads there ("/debug/", "/debug/{path...}", "/debug/pprof/"),
// for GET or any method, as net/http/pprof and expvar are mounted.
func (rt RouteInfo) debug() bool {
	switch rt.Method {
	case "", "GET", "HEAD":
	default:
		return false
	}
	lit, _, _ := strings.Cut(rt.Pattern, "{")
	if !strings.HasPrefix(lit, "/debug") {
		return false
	}
	for _, p := range []string{"/debug/pprof/", "/debug/vars"} {
		if strings.HasPrefix(p, lit) || strings.HasPrefix(lit, p) {
			return true
		}
	}
	return false
}

// publicAccountRoute reports whether a route's name is one of make:auth's
// routes for guests (register, log in, reset a password).
func publicAccountRoute(name string) bool {
	name = strings.TrimPrefix(name, "api.")
	return slices.Contains([]string{"register", "login", "login.two-factor", "password.email", "password.reset", "password.update", "verification.verify"}, name)
}
