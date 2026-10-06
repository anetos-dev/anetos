// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestIDs(t *testing.T) {
	var seen string
	h := RequestIDs(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestID(r.Context()) }))

	got := do(t, h, "GET", "/", nil, RequestIDHeader, "lb-abc_123:4.5")
	if seen != "lb-abc_123:4.5" || got.header.Get(RequestIDHeader) != seen {
		t.Errorf("valid incoming id not reused: %q", seen)
	}
	for _, bad := range []string{"", "has space", "evil\nlog", strings.Repeat("x", 129)} {
		do(t, h, "GET", "/", nil, RequestIDHeader, bad)
		if seen == bad || len(seen) != 16 {
			t.Errorf("incoming %q: got id %q, want a new 16-char id", bad, seen)
		}
	}
	if RequestID(context.Background()) != "" {
		t.Error("RequestID without middleware")
	}
}

func TestAccessLog(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	r := newTestRouter()
	r.UseGlobal(RequestIDs, AccessLog(logger))
	r.Get("/posts/{id}", text("hello"))
	r.Get("/health/live", text("ok")).Name("health.live")

	do(t, r, "GET", "/posts/1", nil, RequestIDHeader, "rid1")
	line := logs.String()
	for _, want := range []string{"msg=request", "method=GET", "path=/posts/1", "status=200", "bytes=5", `route="GET /posts/{id}"`, "request_id=rid1", "ip=192.0.2.1"} {
		if !strings.Contains(line, want) {
			t.Errorf("access log missing %q: %s", want, line)
		}
	}
	logs.Reset()
	do(t, r, "GET", "/health/live", nil)
	if logs.Len() != 0 {
		t.Errorf("health checks should log at debug: %s", logs.String())
	}
	do(t, r, "GET", "/missing", nil)
	if !strings.Contains(logs.String(), "status=404") {
		t.Errorf("404 not logged: %s", logs.String())
	}
}

func TestRecoverMiddleware(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	boom := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("middleware bug") })
	}
	h := Recover(logger)(boom(nil))
	got := do(t, h, "GET", "/", nil)
	if got.status != 500 || !strings.Contains(logs.String(), "middleware bug") {
		t.Errorf("= %d; logs %s", got.status, logs.String())
	}
	mustPanic(t, "ErrAbortHandler passes through", func() {
		Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })).
			ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	})
}

func TestRealIP(t *testing.T) {
	trusted, err := ParsePrefixes([]string{"10.0.0.0/8", "192.168.1.5"})
	if err != nil {
		t.Fatal(err)
	}
	var ip string
	h := RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip = ClientIP(r)
		if from := ClientIPFrom(r.Context()); from != ip {
			t.Errorf("ClientIPFrom = %q, ClientIP = %q", from, ip)
		}
	}))
	serve := func(remote string, headers ...string) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Add(headers[i], headers[i+1])
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		return ip
	}

	tests := []struct {
		name, remote string
		headers      []string
		want         string
	}{
		{"untrusted peer ignores headers", "203.0.113.9:5000", []string{"X-Forwarded-For", "1.2.3.4"}, "203.0.113.9"},
		{"trusted peer uses XFF", "10.1.2.3:5000", []string{"X-Forwarded-For", "198.51.100.7"}, "198.51.100.7"},
		{"skips trusted hops from the right", "10.1.2.3:5000", []string{"X-Forwarded-For", "6.6.6.6, 198.51.100.7, 10.9.9.9"}, "198.51.100.7"},
		{"multiple XFF headers", "192.168.1.5:1", []string{"X-Forwarded-For", "198.51.100.1", "X-Forwarded-For", "10.0.0.1"}, "198.51.100.1"},
		{"X-Real-IP isn't read", "10.1.2.3:5000", []string{"X-Real-IP", "198.51.100.8"}, "10.1.2.3"},
		{"malformed chain keeps peer", "10.1.2.3:5000", []string{"X-Forwarded-For", "nonsense"}, "10.1.2.3"},
		{"ipv6 peer", "[2001:db8::1]:443", nil, "2001:db8::1"},
	}
	for _, tt := range tests {
		if got := serve(tt.remote, tt.headers...); got != tt.want {
			t.Errorf("%s: ClientIP = %q, want %q", tt.name, got, tt.want)
		}
	}
	if _, err := ParsePrefixes([]string{"not-an-ip"}); err == nil {
		t.Error("bad prefix accepted")
	}
	if got := ClientIPFrom(t.Context()); got != "" {
		t.Errorf("ClientIPFrom outside a request = %q", got)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "garbage"
	if ClientIP(req) != "garbage" {
		t.Error("ClientIP fallback")
	}
}

func TestTimeoutMiddleware(t *testing.T) {
	r := newTestRouter()
	r.UseGlobal(Timeout(10 * time.Millisecond))
	r.Get("/slow", func(c *Ctx) error {
		select {
		case <-c.Done():
			return c.Err()
		case <-time.After(time.Second):
			return c.Text(200, "too slow")
		}
	})
	if got := do(t, r, "GET", "/slow", nil, "Accept", "application/json"); got.status != 503 {
		t.Errorf("timeout status = %d", got.status)
	}
	if Timeout(0)(http.NotFoundHandler()) == nil {
		t.Error("Timeout(0) should pass through")
	}
}

func TestSecureHeaders(t *testing.T) {
	h := SecureHeaders(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY") // handlers can override
	}))
	got := do(t, h, "GET", "/", nil)
	if got.header.Get("X-Content-Type-Options") != "nosniff" || got.header.Get("X-Frame-Options") != "DENY" ||
		!strings.HasPrefix(got.header.Get("Strict-Transport-Security"), "max-age=") {
		t.Errorf("headers = %v", got.header)
	}
	got = do(t, SecureHeaders(false)(http.NotFoundHandler()), "GET", "/", nil)
	if got.header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS set without hsts")
	}
}

func TestCORS(t *testing.T) {
	cfg := CORSConfig{
		Origins: []string{"https://app.example.com", "https://*.example.org"},
		Methods: []string{"GET", "POST"}, Headers: []string{"Content-Type"}, Expose: []string{"X-Request-ID"},
		Credentials: true, MaxAge: 10 * time.Minute,
	}
	r := newTestRouter()
	r.UseGlobal(CORS(cfg))
	r.Post("/api", text("ok"))

	got := do(t, r, "POST", "/api", nil, "Origin", "https://app.example.com")
	if got.header.Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		got.header.Get("Access-Control-Allow-Credentials") != "true" ||
		got.header.Get("Access-Control-Expose-Headers") != "X-Request-ID" || got.body != "ok" {
		t.Errorf("simple request headers = %v", got.header)
	}
	got = do(t, r, "OPTIONS", "/api", nil, "Origin", "https://eu.example.org", "Access-Control-Request-Method", "POST")
	if got.status != 204 || got.header.Get("Access-Control-Allow-Methods") != "GET, POST" ||
		got.header.Get("Access-Control-Max-Age") != "600" {
		t.Errorf("preflight = %d %v", got.status, got.header)
	}
	got = do(t, r, "POST", "/api", nil, "Origin", "https://evil.test")
	if got.header.Get("Access-Control-Allow-Origin") != "" || got.header.Get("Vary") != "Origin" {
		t.Errorf("disallowed origin got CORS headers: %v", got.header)
	}
	got = do(t, r, "POST", "/api", nil, "Origin", "https://example.org.evil.test")
	if got.header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("subdomain wildcard matched a lookalike origin")
	}

	star := newTestRouter()
	star.UseGlobal(CORS(CORSConfig{Origins: []string{"*"}}))
	star.Get("/", text("ok"))
	if got := do(t, star, "GET", "/", nil, "Origin", "https://any.test"); got.header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("wildcard = %v", got.header)
	}
}

func TestResponseWriterFlushAndUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := wrapWriter(rec)
	if wrapWriter(rw) != rw {
		t.Error("double wrap")
	}
	rw.Flush()
	if !rw.started() || rw.Status() != 200 || !rec.Flushed || rw.Unwrap() != rec {
		t.Errorf("flush/unwrap: started=%v status=%d flushed=%v", rw.started(), rw.Status(), rec.Flushed)
	}
}

// In a router, RequestIDs and RealIP keep their results in its request
// state; a request a handler serves through the router again (a
// sub-request) has its own, and doesn't change the outer request's.
func TestRequestStateSubrequest(t *testing.T) {
	r := NewRouter(WithLogger(slog.New(slog.DiscardHandler)))
	r.UseGlobal(RequestIDs, RealIP(nil))
	var innerID, innerIP string
	r.Get("/inner", func(c *Ctx) error {
		innerID, innerIP = RequestID(c), ClientIP(c.Request())
		return c.Text(http.StatusOK, "inner")
	})
	r.Get("/outer", func(c *Ctx) error {
		id, ip := RequestID(c), ClientIP(c.Request())
		sub := httptest.NewRequestWithContext(c, http.MethodGet, "/inner", nil)
		sub.RemoteAddr = "192.0.2.9:1234"
		r.ServeHTTP(httptest.NewRecorder(), sub)
		if RequestID(c) != id || ClientIP(c.Request()) != ip {
			t.Errorf("after a sub-request: %q %q, want %q %q", RequestID(c), ClientIP(c.Request()), id, ip)
		}
		return c.Text(http.StatusOK, id+" "+ip)
	})
	req := httptest.NewRequest(http.MethodGet, "/outer", nil)
	req.RemoteAddr = "198.51.100.7:4321"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	id := rec.Header().Get(RequestIDHeader)
	if rec.Body.String() != id+" 198.51.100.7" || len(id) != 16 {
		t.Errorf("outer: %q, header %q", rec.Body, id)
	}
	if innerID == id || len(innerID) != 16 || innerIP != "192.0.2.9" {
		t.Errorf("inner: %q %q", innerID, innerIP)
	}
}
