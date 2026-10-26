// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"

	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// xsrfHeader is the header SPAs send the XSRF-TOKEN cookie back in
// ([WithXSRFCookie]).
const xsrfHeader = "X-XSRF-TOKEN"

type firstPartyKey struct{}

// FirstPartyOrigins marks the requests of the first-party SPAs at
// origins ("https://app.example.com"): those whose Origin header, or
// without one the Referer's origin, is one of them, or that the browser
// says come from the same origin (Sec-Fetch-Site) when the request's own
// host is one of them. A request with an API token (Authorization:
// Bearer) is never marked: it is a token's, whoever sends it.
// [IsFirstParty] reports it, and [FirstParty] runs middleware for them
// only. The server runs it with HTTP_SPA_ORIGINS, which also allows
// those origins CORS with credentials. It panics on a value that isn't
// an origin.
//
// An Origin is a browser's to set: another client can claim one, which
// gets it the session and CSRF checks of an SPA, not a token's access.
func FirstPartyOrigins(origins []string) Middleware {
	set := originSet(origins)
	if len(set) != len(origins) {
		for _, o := range origins {
			if _, err := parseOrigin(o); err != nil {
				panic("web: FirstPartyOrigins: " + err.Error())
			}
		}
	}
	hosts := map[string]bool{}
	for o := range set {
		_, host, _ := strings.Cut(o, "://")
		hosts[host] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !firstParty(r, set, hosts) {
				next.ServeHTTP(w, r)
				return
			}
			if st := stateFrom(r.Context()); st != nil { // in a router: no new context
				st.firstParty = true
			} else {
				r = r.WithContext(context.WithValue(r.Context(), firstPartyKey{}, true))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IsFirstParty reports whether the request ctx belongs to comes from a
// first-party SPA (HTTP_SPA_ORIGINS; see [FirstPartyOrigins]): an
// account's handler logs it in with the session rather than a token.
func IsFirstParty(ctx context.Context) bool {
	if st := stateFrom(ctx); st != nil && st.firstParty {
		return true
	}
	first, _ := ctx.Value(firstPartyKey{}).(bool)
	return first
}

// FirstParty runs mws for the requests of first-party SPAs only
// ([IsFirstParty]); other requests go straight to the handler. An API's
// routes take sessions and CSRF protection this way, for the SPA's
// cookie, while token clients keep neither:
//
//	api := r.Group("/api/v1",
//		web.FirstParty(sessions.Middleware, web.CSRF(web.WithXSRFCookie())),
//		a.TokenMiddleware)
//
// Its [MiddlewareDoc] lists mws in Inner, and their responses.
func FirstParty(mws ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		first := next
		inner := make([]MiddlewareDoc, len(mws))
		doc := MiddlewareDoc{Name: "web.FirstParty", Detail: "HTTP_SPA_ORIGINS"}
		for i := len(mws) - 1; i >= 0; i-- {
			prev, _ := first.(*documented)
			inner[i], first = describe(mws[i], first, prev)
			for status, when := range inner[i].Responses {
				if doc.Responses == nil {
					doc.Responses = map[int]string{}
				}
				doc.Responses[status] = when
			}
		}
		doc.Inner = inner
		return Documented(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsFirstParty(r.Context()) {
				first.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		}), doc)
	}
}

// firstParty reports whether r is a first-party SPA's: no API token,
// and its origin listed, or, from the same origin (a GET under a
// no-referrer policy carries neither Origin nor Referer), its host.
func firstParty(r *http.Request, set, hosts map[string]bool) bool {
	if scheme, _, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") {
		return false
	}
	if o := requestOrigin(r); o != "" {
		return set[o]
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin" && hosts[strings.ToLower(r.Host)]
}

// requestOrigin is the origin the request comes from, normalized: its
// Origin header, else its Referer's; "" if neither is an origin.
func requestOrigin(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" {
		n, _ := parseOrigin(o)
		return n
	}
	ref, err := url.Parse(r.Header.Get("Referer"))
	if err != nil || ref.Host == "" {
		return ""
	}
	n, _ := parseOrigin(ref.Scheme + "://" + ref.Host)
	return n
}

// parseOrigin returns origin ("https://app.example.com:8443") in lower
// case without a trailing slash, or an error if it isn't an http or
// https origin: a scheme and a host, without a path, query or wildcard.
func parseOrigin(origin string) (string, error) {
	u, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(origin), "/"))
	switch {
	case err != nil:
		return "", fmt.Errorf("%q isn't an origin: %w", origin, err)
	case u.Scheme != "http" && u.Scheme != "https":
		return "", fmt.Errorf("%q isn't an origin: it needs http:// or https://", origin)
	case u.Host == "" || strings.Contains(u.Host, "*"):
		return "", fmt.Errorf("%q isn't an origin: it needs a host, without wildcards", origin)
	case u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil:
		return "", fmt.Errorf("%q isn't an origin: only the scheme, the host and the port (https://app.example.com)", origin)
	case strings.HasSuffix(u.Host, ":"):
		return "", fmt.Errorf("%q isn't an origin: its port is empty", origin)
	case strings.ContainsFunc(u.Host, func(r rune) bool { return r > 0x7f }):
		return "", fmt.Errorf("%q isn't an origin as browsers send it: write the host in ASCII (xn--…)", origin)
	}
	host := u.Host
	if u.Port() == "443" && u.Scheme == "https" || u.Port() == "80" && u.Scheme == "http" {
		host = u.Hostname() // as browsers write it
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return strings.ToLower(u.Scheme + "://" + host), nil
}

// originSet is the set of origins' normalized forms, leaving out the
// values that aren't origins (the configuration's validation reports
// them).
func originSet(origins []string) map[string]bool {
	set := map[string]bool{}
	for _, o := range origins {
		if n, err := parseOrigin(o); err == nil {
			set[n] = true
		}
	}
	return set
}
