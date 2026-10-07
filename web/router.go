// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"anetos.dev/anetos"
	"anetos.dev/anetos/view"
)

// HandlerFunc is the standard Anetos handler: it writes a response through
// the [Ctx] or returns an error, which the router turns into an error
// response (see [ErrorHandler]).
type HandlerFunc func(c *Ctx) error

// Middleware is standard net/http middleware. Any middleware written for
// net/http works with Anetos, and Anetos's middleware works anywhere.
type Middleware = func(http.Handler) http.Handler

// ErrorHandler turns an error returned by a handler (or a panic) into a
// response. The default is [DefaultErrorHandler].
type ErrorHandler func(c *Ctx, err error)

// Router registers routes on an http.ServeMux and adds groups, route names,
// URL generation, middleware, consistent 404/405 handling and error
// rendering. It implements http.Handler.
//
// Patterns use ServeMux syntax without the method: "/posts/{id}",
// "/files/{path...}". Unlike raw ServeMux, a pattern ending in "/" matches
// only that exact path ("/posts/" does not match "/posts/1"); use a
// "{name...}" wildcard to match a subtree. Inside a group, "" and "/" both
// mean the group's own path ("/posts", without a trailing slash).
//
// A Router value returned by [Router.Group] or [Router.With] shares routes
// and names with its parent but has its own prefix and middleware.
type Router struct {
	core     *routerCore
	parent   *Router
	host     string // Host: routes match only this host
	prefix   string
	namePfx  string
	mws      []Middleware
	hasRoute bool // this router or a descendant has routes
}

type routerCore struct {
	mux          *http.ServeMux
	app          *anetos.App
	logger       *slog.Logger
	debug        bool
	errorHandler ErrorHandler
	errorPage    func(*Ctx, ErrorPage) view.Component // ErrorPages; under mu

	mu      sync.RWMutex
	names   map[string]*Route
	routes  []*Route
	methods map[string]bool // methods used by registered routes, for Allow
	global  []Middleware
	built   bool
	handler atomic.Pointer[http.Handler] // global(mux), built on first request
}

// RouterOption configures [NewRouter].
type RouterOption func(*routerCore)

// WithApp connects the router to an application: [Ctx.App] returns it, and
// the logger and debug mode come from its configuration.
func WithApp(app *anetos.App) RouterOption {
	return func(c *routerCore) {
		c.app = app
		c.logger = app.Logger()
		c.debug = app.Config().Debug
	}
}

// WithLogger sets the logger used for errors. Default slog.Default().
func WithLogger(l *slog.Logger) RouterOption {
	return func(c *routerCore) { c.logger = l }
}

// WithDebug enables detailed error pages, including error chains and stack
// traces. Never enable it in production; apps take it from APP_DEBUG.
func WithDebug(debug bool) RouterOption {
	return func(c *routerCore) { c.debug = debug }
}

// WithErrorHandler replaces [DefaultErrorHandler].
func WithErrorHandler(h ErrorHandler) RouterOption {
	return func(c *routerCore) { c.errorHandler = h }
}

// NewRouter returns an empty router. Most applications get their router from
// [Server.Router] instead.
func NewRouter(opts ...RouterOption) *Router {
	core := &routerCore{
		mux:          http.NewServeMux(),
		logger:       slog.Default(),
		errorHandler: DefaultErrorHandler,
		names:        map[string]*Route{},
		methods:      map[string]bool{},
	}
	for _, opt := range opts {
		opt(core)
	}
	r := &Router{core: core}
	core.mux.Handle("/", http.HandlerFunc(r.fallback))
	return r
}

// UseGlobal adds middleware around the whole router, including requests that
// match no route (404/405). Use it for middleware that must see every
// request, such as CORS, request IDs and access logs. It must be called
// before the router serves its first request.
func (r *Router) UseGlobal(mws ...Middleware) {
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	if r.core.built {
		panic("web: UseGlobal called after the router started serving")
	}
	r.core.global = append(r.core.global, mws...)
}

// Use adds middleware to the routes registered on this router (and groups
// created from it) from now on. To keep the order obvious, Use panics if
// this router or any group or With router derived from it already has
// routes; add middleware first, or use [Router.With].
func (r *Router) Use(mws ...Middleware) {
	if r.hasRoute {
		panic("web: Use called after routes were added to this router or its groups; call Use first or use With")
	}
	r.mws = append(r.mws, mws...)
}

// With returns a router with the same prefix and extra middleware, for
// applying middleware to individual routes:
//
//	r.With(auth.Required).Post("/posts", h.Store)
func (r *Router) With(mws ...Middleware) *Router {
	return &Router{core: r.core, parent: r, host: r.host, prefix: r.prefix, namePfx: r.namePfx, mws: append(slices.Clip(r.mws), mws...)}
}

// Group returns a router whose routes share prefix and middleware:
//
//	admin := r.Group("/admin", auth.Required).As("admin.")
//	admin.Get("/users", h.Users).Name("users") // route name "admin.users"
func (r *Router) Group(prefix string, mws ...Middleware) *Router {
	checkPattern(prefix)
	return &Router{core: r.core, parent: r, host: r.host, prefix: joinPath(r.prefix, prefix), namePfx: r.namePfx, mws: append(slices.Clip(r.mws), mws...)}
}

// Host returns a router whose routes match only requests for host
// ("admin.example.com"), with the same prefix and middleware. A port
// ("admin.localhost:8080") goes into the routes' URLs; requests match on
// the host name alone, whatever their port. Routes without a host match every
// host, but a host's routes win for their host (net/http.ServeMux's
// rules). The URLs of a host's routes ([Router.URL]) are absolute, with
// APP_URL's scheme (https without one):
//
//	admin := r.Host("admin.example.com")
//	admin.Get("/", h.Dashboard).Name("admin.home") // https://admin.example.com/
//
// It panics if host is empty or has a scheme, a path or spaces.
func (r *Router) Host(host string) *Router {
	if host == "" || strings.ContainsAny(host, "/ \t") {
		panic(fmt.Sprintf("web: invalid host %q: use a host name, with a port if needed (admin.example.com)", host))
	}
	return &Router{core: r.core, parent: r, host: strings.ToLower(host), prefix: r.prefix, namePfx: r.namePfx, mws: slices.Clip(r.mws)}
}

// hostName returns host without its port, as net/http.ServeMux matches
// hosts.
func hostName(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// As sets a prefix added to the names of routes registered on this router.
func (r *Router) As(namePrefix string) *Router {
	r.namePfx += namePrefix
	return r
}

// Get registers h for GET (and HEAD) requests to pattern.
func (r *Router) Get(pattern string, h HandlerFunc) *Route {
	return r.Handle(http.MethodGet, pattern, h)
}

// Post registers h for POST requests to pattern.
func (r *Router) Post(pattern string, h HandlerFunc) *Route {
	return r.Handle(http.MethodPost, pattern, h)
}

// Put registers h for PUT requests to pattern.
func (r *Router) Put(pattern string, h HandlerFunc) *Route {
	return r.Handle(http.MethodPut, pattern, h)
}

// Patch registers h for PATCH requests to pattern.
func (r *Router) Patch(pattern string, h HandlerFunc) *Route {
	return r.Handle(http.MethodPatch, pattern, h)
}

// Delete registers h for DELETE requests to pattern.
func (r *Router) Delete(pattern string, h HandlerFunc) *Route {
	return r.Handle(http.MethodDelete, pattern, h)
}

// Options registers h for OPTIONS requests to pattern. Without it, OPTIONS
// requests get 204 No Content with an Allow header automatically.
func (r *Router) Options(pattern string, h HandlerFunc) *Route {
	return r.Handle(http.MethodOptions, pattern, h)
}

// Handle registers h for method ("" matches every method) and pattern.
func (r *Router) Handle(method, pattern string, h HandlerFunc) *Route {
	if h == nil {
		panic("web: nil handler for " + method + " " + pattern)
	}
	rt := r.newRoute(method, pattern)
	r.register(rt, r.adapt(rt, h))
	return rt
}

// HandleStd registers a standard http.Handler. Route middleware still
// applies; errors can't be returned, so the handler writes its own response.
func (r *Router) HandleStd(method, pattern string, h http.Handler) *Route {
	if h == nil {
		panic("web: nil handler for " + method + " " + pattern)
	}
	rt := r.newRoute(method, pattern)
	r.register(rt, h)
	return rt
}

func (r *Router) newRoute(method, pattern string) *Route {
	checkPattern(pattern)
	if r.prefix != "" && pattern == "/" {
		pattern = "" // a group's "/" is the group's own path
	}
	full := joinPath(r.prefix, pattern)
	if full == "" {
		full = "/"
	}
	return &Route{core: r.core, method: method, host: r.host, pattern: full, namePfx: r.namePfx}
}

func (r *Router) register(rt *Route, h http.Handler) {
	for p := r; p != nil; p = p.parent {
		p.hasRoute = true
	}
	h = chain(r.mws, h)
	// Record the route for outer middleware (access log, RouteFromContext),
	// for plain handlers too.
	next := h
	h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if st := stateFrom(req.Context()); st != nil {
			st.route = rt
			st.served = true
		}
		next.ServeHTTP(w, req)
	})
	muxPattern := hostName(rt.host) + rt.pattern
	if strings.HasSuffix(muxPattern, "/") {
		muxPattern += "{$}" // exact match, not a subtree
	}
	if rt.method != "" {
		muxPattern = rt.method + " " + muxPattern
	}
	r.core.mux.Handle(muxPattern, h) // panics on conflicting patterns
	r.core.mu.Lock()
	r.core.routes = append(r.core.routes, rt)
	if rt.method != "" {
		r.core.methods[rt.method] = true
	}
	r.core.mu.Unlock()
}

// adapt turns a HandlerFunc into an http.Handler that builds the Ctx,
// recovers panics, handles returned errors and cleans up multipart files.
func (r *Router) adapt(rt *Route, h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c := &Ctx{w: wrapWriter(w), r: req, router: r, route: rt}
		defer func() {
			// net/http only removes temp files of the request it created,
			// not of the copies made by WithContext.
			if c.r.MultipartForm != nil {
				_ = c.r.MultipartForm.RemoveAll()
			}
		}()
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
					panic(v)
				}
				r.handleError(c, newPanicError(v))
			}
		}()
		if err := h(c); err != nil {
			r.handleError(c, err)
		}
	})
}

// handleError runs the error handler. If the response had already started,
// the error can't be reported to the client, so after the handler logs it
// the connection is aborted: the client sees a failed response instead of a
// truncated one that looks successful.
func (r *Router) handleError(c *Ctx, err error) {
	started := c.w.started()
	r.core.errorHandler(c, err)
	if started {
		panic(http.ErrAbortHandler)
	}
}

func chain(mws []Middleware, h http.Handler) http.Handler {
	for _, mw := range slices.Backward(mws) {
		h = mw(h)
	}
	return h
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	hp := r.core.handler.Load()
	if hp == nil {
		hp = r.core.build()
	}
	if st := stateFrom(req.Context()); st == nil || st.served {
		// A new request, or one a handler serves through a router again
		// (a sub-request): it gets a state of its own, inheriting what
		// the request it comes from knew, as context values would, so
		// its middleware doesn't overwrite the outer request's ID,
		// client IP or locale.
		ns := &requestState{core: r.core, url: req.URL}
		if st != nil {
			ns.requestID, ns.clientIP, ns.locale = st.requestID, st.clientIP, st.locale
		}
		req = req.WithContext(context.WithValue(req.Context(), stateKey{}, ns))
	}
	(*hp).ServeHTTP(w, req)
}

func (c *routerCore) build() *http.Handler {
	c.mu.Lock()
	defer c.mu.Unlock()
	if hp := c.handler.Load(); hp != nil {
		return hp
	}
	h := chain(c.global, c.mux) // if a constructor panics, the next request retries
	c.built = true
	c.handler.Store(&h)
	return &h
}

// fallback handles requests no route matched: 204 for OPTIONS, 405 with an
// Allow header if the path exists for other methods, otherwise 404.
func (r *Router) fallback(w http.ResponseWriter, req *http.Request) {
	c := &Ctx{w: wrapWriter(w), r: req, router: r}
	allowed := r.allowedMethods(req)
	switch {
	case len(allowed) > 0 && req.Method == http.MethodOptions:
		if !slices.Contains(allowed, http.MethodOptions) {
			allowed = append(allowed, http.MethodOptions)
		}
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		w.WriteHeader(http.StatusNoContent)
	case len(allowed) > 0:
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		r.core.errorHandler(c, Error(http.StatusMethodNotAllowed, ""))
	default:
		r.core.errorHandler(c, Error(http.StatusNotFound, ""))
	}
}

var standardMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete,
}

func (r *Router) allowedMethods(req *http.Request) []string {
	r.core.mu.RLock()
	methods := slices.Clone(standardMethods)
	for m := range r.core.methods {
		if !slices.Contains(methods, m) {
			methods = append(methods, m)
		}
	}
	r.core.mu.RUnlock()
	slices.Sort(methods[len(standardMethods):])

	var allowed []string
	probe := *req
	for _, m := range methods {
		if m == req.Method {
			continue
		}
		probe.Method = m
		if _, pattern := r.core.mux.Handler(&probe); pattern != "" && pattern != "/" {
			allowed = append(allowed, m)
		}
	}
	return allowed
}

// Route is a registered route.
type Route struct {
	core    *routerCore
	method  string
	host    string // "" for every host
	pattern string
	namePfx string
	name    atomic.Pointer[string]
}

// Name gives the route a name for URL generation. Names must be unique;
// a group's [Router.As] prefix is prepended. Name panics on duplicates.
func (rt *Route) Name(name string) *Route {
	full := rt.namePfx + name
	rt.core.mu.Lock()
	defer rt.core.mu.Unlock()
	if _, dup := rt.core.names[full]; dup {
		panic(fmt.Sprintf("web: duplicate route name %q", full))
	}
	if old := rt.RouteName(); old != "" {
		delete(rt.core.names, old)
	}
	rt.name.Store(&full)
	rt.core.names[full] = rt
	return rt
}

// Method returns the route's HTTP method ("" for any).
func (rt *Route) Method() string { return rt.method }

// Pattern returns the route's full path pattern, e.g. "/posts/{id}".
func (rt *Route) Pattern() string { return rt.pattern }

// Host returns the host the route matches ([Router.Host]), "" for every
// host.
func (rt *Route) Host() string { return rt.host }

// RouteName returns the route's name, or "" if it has none.
func (rt *Route) RouteName() string {
	if n := rt.name.Load(); n != nil {
		return *n
	}
	return ""
}

// RouteInfo describes a route, for listings such as `routes:list`.
type RouteInfo struct {
	Method  string // GET, POST, …; "" for any method
	Host    string // the host it matches ([Router.Host]), "" for any
	Pattern string // the path pattern, "/posts/{id}"
	Name    string // the route's name, "" if unnamed
}

// Routes lists every registered route in registration order.
func (r *Router) Routes() []RouteInfo {
	r.core.mu.RLock()
	defer r.core.mu.RUnlock()
	out := make([]RouteInfo, len(r.core.routes))
	for i, rt := range r.core.routes {
		out[i] = RouteInfo{Method: rt.method, Host: rt.host, Pattern: rt.pattern, Name: rt.RouteName()}
	}
	return out
}

// ErrUnknownRoute is wrapped by [Router.URL] for names that don't exist.
var ErrUnknownRoute = errors.New("web: unknown route name")

// URL builds the path for the named route, filling its wildcards in order
// from args (formatted with fmt.Sprint and path-escaped):
//
//	r.Get("/posts/{id}/comments/{cid}", h).Name("comments.show")
//	u, _ := r.URL("comments.show", 42, 7) // "/posts/42/comments/7"
//
// A "{path...}" wildcard keeps its slashes. The number of args must match
// the number of wildcards; a [url.Values] after them becomes the query
// string:
//
//	u, _ := r.URL("posts.index", url.Values{"page": {"2"}}) // "/posts?page=2"
//
// The URL of a route of a [Router.Host] is absolute:
// "https://admin.example.com/users".
func (r *Router) URL(name string, args ...any) (string, error) {
	r.core.mu.RLock()
	rt, ok := r.core.names[name]
	r.core.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownRoute, name)
	}
	p, err := buildPath(rt.pattern, args)
	if err != nil || rt.host == "" {
		return p, err
	}
	scheme := "https"
	if r.core.app != nil {
		if u, err := url.Parse(r.core.app.Config().URL); err == nil && u.Scheme != "" {
			scheme = u.Scheme
		}
	}
	return scheme + "://" + rt.host + p, nil
}

// MustURL is like [Router.URL] but panics on error. Use it with route names
// and arguments that are fixed in code.
func (r *Router) MustURL(name string, args ...any) string {
	u, err := r.URL(name, args...)
	if err != nil {
		panic(err)
	}
	return u
}

func buildPath(pattern string, args []any) (string, error) {
	var query url.Values
	if n := len(args); n > 0 {
		if q, ok := args[n-1].(url.Values); ok {
			query, args = q, args[:n-1]
		}
	}
	var b strings.Builder
	used := 0
	rest := pattern
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			b.WriteString(rest)
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return "", fmt.Errorf("web: malformed pattern %q", pattern)
		}
		b.WriteString(rest[:i])
		wild := rest[i+1 : i+j]
		rest = rest[i+j+1:]
		if wild == "$" {
			continue
		}
		if used >= len(args) {
			return "", fmt.Errorf("web: pattern %q needs more arguments than the %d given", pattern, len(args))
		}
		if _, ok := args[used].(url.Values); ok {
			return "", fmt.Errorf("web: pattern %q: a url.Values (the query) must be the last argument, after one per wildcard", pattern)
		}
		val := fmt.Sprint(args[used])
		used++
		if strings.HasSuffix(wild, "...") {
			segs := strings.Split(val, "/")
			for k, s := range segs {
				if s == "." || s == ".." {
					return "", fmt.Errorf("web: argument %q for {%s} contains a dot segment", val, wild)
				}
				segs[k] = url.PathEscape(s)
			}
			b.WriteString(strings.Join(segs, "/"))
		} else {
			if val == "" || val == "." || val == ".." {
				return "", fmt.Errorf("web: argument %q is not a valid value for {%s}", val, wild)
			}
			b.WriteString(url.PathEscape(val))
		}
	}
	if used != len(args) {
		return "", fmt.Errorf("web: pattern %q takes %d arguments, got %d", pattern, used, len(args))
	}
	if len(query) > 0 {
		b.WriteByte('?')
		b.WriteString(query.Encode())
	}
	return b.String(), nil
}

func checkPattern(p string) {
	if p != "" && !strings.HasPrefix(p, "/") {
		panic(fmt.Sprintf("web: pattern %q must start with /", p))
	}
	if strings.ContainsAny(p, " \t") {
		panic(fmt.Sprintf("web: pattern %q must not contain spaces; use r.Get, r.Post, … for methods", p))
	}
}

func joinPath(prefix, p string) string {
	switch {
	case prefix == "":
		return p
	case p == "" || p == "/":
		if p == "/" {
			return strings.TrimSuffix(prefix, "/") + "/"
		}
		return prefix
	}
	return strings.TrimSuffix(prefix, "/") + p
}

// requestState carries per-request routing information to outer middleware
// (such as the access log), which runs before routing.
type requestState struct {
	route *Route
	core  *routerCore
	url   *url.URL // the request's URL, for PageURL

	// Set by RequestIDs and RealIP running in the router, which keeps
	// them here rather than in two more context values.
	requestID string
	clientIP  string
	locale    *localeState // set by the server's locale middleware

	served bool // a route's handler runs: a router serving it again starts a state of its own
}

type stateKey struct{}

func stateFrom(ctx context.Context) *requestState {
	st, _ := ctx.Value(stateKey{}).(*requestState)
	return st
}

// RouteFromContext returns the route that handled the request, or nil. It is
// meant for middleware that runs around the router, after the handler
// returns.
func RouteFromContext(ctx context.Context) *Route {
	if st := stateFrom(ctx); st != nil {
		return st.route
	}
	return nil
}
