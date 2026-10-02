// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ---- Request ID ----

type requestIDKey struct{}

// RequestIDHeader is the header used to read and return request IDs.
const RequestIDHeader = "X-Request-ID"

// RequestID returns the request ID stored by the [RequestIDs] middleware,
// or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// RequestIDs assigns every request an ID, stores it in the context (see
// [RequestID]) and returns it in the X-Request-ID response header. An
// incoming X-Request-ID is reused if it is short and made of safe
// characters, so IDs from a load balancer carry through; otherwise a new
// random ID is generated.
func RequestIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newRequestID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return strings.ToLower(idEncoding.EncodeToString(b[:]))
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == ':'
		if !ok {
			return false
		}
	}
	return true
}

// ---- Access log ----

// AccessLog logs one line per request with method, path, route, status,
// bytes, duration, client IP and request ID. Requests to routes named
// "health.*" are logged at debug level to keep probes out of the logs.
func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := wrapWriter(w)
			next.ServeHTTP(rw, r)

			status := rw.Status()
			if status == 0 {
				status = http.StatusOK
			}
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int64("bytes", rw.written),
				slog.Duration("duration", time.Since(start)),
				slog.String("ip", ClientIP(r)),
			}
			level := slog.LevelInfo
			if rt := RouteFromContext(r.Context()); rt != nil {
				attrs = append(attrs, slog.String("route", rt.method+" "+rt.pattern))
				if strings.HasPrefix(rt.RouteName(), "health.") {
					level = slog.LevelDebug
				}
			}
			if id := RequestID(r.Context()); id != "" {
				attrs = append(attrs, slog.String("request_id", id))
			}
			logger.LogAttrs(r.Context(), level, "request", attrs...)
		})
	}
}

// ---- Recover ----

// Recover catches panics in the middleware around the router (panics in
// handlers are already turned into error responses by the router), logs
// them with a stack trace and responds 500 if nothing was written yet.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := wrapWriter(w)
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
					panic(v)
				}
				logger.Error("panic in middleware", "panic", v, "path", r.URL.Path, "stack", string(debug.Stack()))
				if !rw.started() {
					http.Error(rw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}

// ---- Real client IP ----

type clientIPKey struct{}

// ClientIP returns the client's IP address: the one determined by the
// [RealIP] middleware if it ran, otherwise the host part of r.RemoteAddr.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RealIP determines the client IP behind reverse proxies. Forwarding
// headers are trusted only when the direct peer is in trusted (for example
// your load balancer's network); then X-Forwarded-For is read from the
// right, skipping trusted proxies, falling back to X-Real-IP. Without
// trusted proxies, headers are ignored, because any client can forge them.
// The result is available through [ClientIP].
func RealIP(trusted []netip.Prefix) Middleware {
	isTrusted := func(a netip.Addr) bool {
		return slices.ContainsFunc(trusted, func(p netip.Prefix) bool { return p.Contains(a.Unmap()) })
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peerIP(r)
			if ip.IsValid() && isTrusted(ip) {
				if client, ok := forwardedClient(r, isTrusted); ok {
					ip = client
				}
			}
			if ip.IsValid() {
				r = r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip.Unmap().String()))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// parseHop parses an address from a forwarding header: "1.2.3.4",
// "1.2.3.4:5678", "2001:db8::1" or "[2001:db8::1]:443".
func parseHop(h string) (netip.Addr, bool) {
	h = strings.TrimSpace(h)
	if a, err := netip.ParseAddr(h); err == nil {
		return a, true
	}
	if ap, err := netip.ParseAddrPort(h); err == nil {
		return ap.Addr(), true
	}
	return netip.Addr{}, false
}

func peerIP(r *http.Request) netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err == nil {
		return ap.Addr()
	}
	a, _ := netip.ParseAddr(r.RemoteAddr)
	return a
}

func forwardedClient(r *http.Request, isTrusted func(netip.Addr) bool) (netip.Addr, bool) {
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for h := range strings.SplitSeq(v, ",") {
			hops = append(hops, strings.TrimSpace(h))
		}
	}
	for _, h := range slices.Backward(hops) {
		a, ok := parseHop(h)
		if !ok {
			return netip.Addr{}, false // malformed chain: don't guess
		}
		if !isTrusted(a) {
			return a, true
		}
	}
	if len(hops) > 0 {
		// Every hop is a trusted proxy; X-Real-IP could have been set by
		// the client and passed through, so don't use it.
		return netip.Addr{}, false
	}
	if a, ok := parseHop(r.Header.Get("X-Real-IP")); ok {
		return a, true
	}
	return netip.Addr{}, false
}

// ParsePrefixes parses IP addresses and CIDR ranges ("10.0.0.0/8",
// "192.168.1.5") for [RealIP].
func ParsePrefixes(values []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, v := range values {
		if strings.Contains(v, "/") {
			p, err := netip.ParsePrefix(v)
			if err != nil {
				return nil, err
			}
			if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(a.Unmap(), p.Bits()-96) // addresses are compared unmapped
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(v)
		if err != nil {
			return nil, err
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// ---- Timeout & body limit ----

// Timeout gives each request a context deadline of d. Handlers that pass
// the context to their I/O stop when it expires, and the resulting
// context.DeadlineExceeded error becomes a 503 response. A zero d disables it.
// Streaming handlers remove it with [WithoutTimeout] (as [Ctx.Events] does).
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parent := r.Context()
			if parent.Value(beforeTimeoutKey{}) == nil { // the outermost Timeout's, for WithoutTimeout
				parent = context.WithValue(parent, beforeTimeoutKey{}, parent)
			}
			ctx, cancel := context.WithTimeout(parent, d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// BodyLimit rejects request bodies larger than n bytes (binding reports
// 413 Request Entity Too Large). A zero n disables it.
func BodyLimit(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		if n <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				w.Header().Set("Connection", "close")
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- Security headers ----

// SecureHeaders sets conservative security headers on every response;
// handlers may override them. hsts adds Strict-Transport-Security (only
// enable it when the site is served over HTTPS, as in production).
func SecureHeaders(hsts bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- CORS ----

// CORSConfig configures [CORS]. Environment keys use the HTTP_CORS_ prefix.
type CORSConfig struct {
	// Origins allowed to make cross-origin requests, e.g.
	// "https://app.example.com". "*" allows any origin (not allowed with
	// Credentials); "https://*.example.com" allows subdomains. Empty
	// disables CORS.
	Origins     []string      `env:"ORIGINS"`
	Methods     []string      `env:"METHODS" default:"GET,HEAD,POST,PUT,PATCH,DELETE"`                                  // methods allowed cross-origin
	Headers     []string      `env:"HEADERS" default:"Accept,Authorization,Content-Type,X-Requested-With,X-Request-ID"` // request headers allowed
	Expose      []string      `env:"EXPOSE" default:"X-Request-ID"`                                                     // response headers scripts may read
	Credentials bool          `env:"CREDENTIALS"`                                                                       // allow cookies and Authorization
	MaxAge      time.Duration `env:"MAX_AGE" default:"10m"`                                                             // how long browsers cache a preflight
}

func (c CORSConfig) allows(origin string) bool {
	origin = strings.ToLower(origin)
	for _, o := range c.Origins {
		o = strings.ToLower(o)
		switch {
		case o == "*", o == origin:
			return true
		case strings.Contains(o, "://*."):
			scheme, rest, _ := strings.Cut(o, "://*.")
			if strings.HasPrefix(origin, scheme+"://") && strings.HasSuffix(origin, "."+rest) {
				return true
			}
		}
	}
	return false
}

// CORS answers preflight requests and adds Access-Control-* headers for
// allowed origins. Requests from other origins pass through without CORS
// headers, so browsers block them.
//
// CORS panics if cfg allows any origin ("*") together with credentials,
// which would let every website make authenticated requests.
func CORS(cfg CORSConfig) Middleware {
	if cfg.Credentials && slices.Contains(cfg.Origins, "*") {
		panic(`web: CORS with Origins "*" and Credentials is unsafe; list the allowed origins`)
	}
	methods := strings.Join(cfg.Methods, ", ")
	headers := strings.Join(cfg.Headers, ", ")
	expose := strings.Join(cfg.Expose, ", ")
	maxAge := strconv.Itoa(int(cfg.MaxAge.Seconds()))
	wildcard := slices.Contains(cfg.Origins, "*") && !cfg.Credentials
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			h := w.Header()
			h.Add("Vary", "Origin")
			if origin == "" || !cfg.allows(origin) {
				next.ServeHTTP(w, r)
				return
			}
			if wildcard {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
			}
			if cfg.Credentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				h.Set("Access-Control-Allow-Methods", methods)
				h.Set("Access-Control-Allow-Headers", headers)
				h.Set("Access-Control-Max-Age", maxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if expose != "" {
				h.Set("Access-Control-Expose-Headers", expose)
			}
			next.ServeHTTP(w, r)
		})
	}
}
