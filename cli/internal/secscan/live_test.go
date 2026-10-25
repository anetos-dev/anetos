// SPDX-License-Identifier: Apache-2.0

package secscan

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// site serves h over TLS for https://shop.test and over plain HTTP for
// http://shop.test, and returns a client that reaches them.
func site(t *testing.T, h http.Handler, plain http.Handler) (*http.Client, time.Time) {
	t.Helper()
	tlsSrv := httptest.NewTLSServer(h)
	t.Cleanup(tlsSrv.Close)
	return siteOn(t, tlsSrv, plain)
}

// siteOn is site with a TLS server of the test's.
func siteOn(t *testing.T, tlsSrv *httptest.Server, plain http.Handler) (*http.Client, time.Time) {
	t.Helper()
	httpSrv := httptest.NewServer(plain)
	t.Cleanup(httpSrv.Close)
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		target := tlsSrv.Listener.Addr().String()
		if strings.HasSuffix(addr, ":80") {
			target = httpSrv.Listener.Addr().String()
		}
		return d.DialContext(ctx, network, target)
	}
	tr := &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // the test's certificate
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	dials[client] = dial
	return client, tlsSrv.Certificate().NotAfter
}

// dials are the test clients' dialers, for the TLS version probe.
var dials = map[*http.Client]func(context.Context, string, string) (net.Conn, error){}

func TestLiveFindsMistakes(t *testing.T) {
	bad := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx/1.18.0")
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		switch {
		case r.Method == "TRACE":
			w.Header().Set("Content-Type", "message/http")
			_, _ = w.Write([]byte("TRACE / HTTP/1.1\r\nx-anetos-trace: " + r.Header.Get("X-Anetos-Trace") + "\r\n"))
		case r.URL.Path == "/":
			http.SetCookie(w, &http.Cookie{Name: "app_session", Value: "x"})
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<h1>Shop</h1>"))
		case r.URL.Path == "/.env":
			_, _ = w.Write([]byte("APP_KEY=base64:x\nDB_PASSWORD=y\n"))
		case r.URL.Path == "/.git/HEAD":
			_, _ = w.Write([]byte("ref: refs/heads/main\n"))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte("panic: oops\n\ngoroutine 1 [running]:\nmain.main()\n\t/app/main.go:12 +0x1d\n"))
		}
	})
	plain := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<h1>Shop</h1>")) })
	client, notAfter := site(t, bad, plain)
	got, err := Live(t.Context(), LiveOptions{URL: "https://shop.test/", Level: Advanced, Hosts: []string{"shop.test"}, Client: client,
		Now: notAfter.Add(-10 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	rank := map[string]int{"note": 1, "warning": 2, "problem": 3}
	seen := map[string]string{} // the worst finding of each rule and place
	for _, f := range got {
		if k := f.Rule + " " + f.Where; rank[f.Severity] > rank[seen[k]] {
			seen[k] = f.Severity
		}
	}
	want := map[string]string{
		"live.headers Strict-Transport-Security": "warning", "live.headers X-Content-Type-Options": "warning",
		"live.headers X-Frame-Options": "warning", "live.headers Referrer-Policy": "note", "live.headers Content-Security-Policy": "note",
		"live.cookie app_session": "problem", "live.https shop.test": "problem",
		"live.exposed /.env": "problem", "live.exposed /.git/HEAD": "problem",
		"live.debug 404 page":                   "problem",
		"live.cors Access-Control-Allow-Origin": "problem", "live.trace TRACE": "warning", "live.version Server": "note",
		"live.cert-expiry shop.test": "warning",
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("%s: %q, want %q", k, seen[k], v)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("findings: %v", seen)
	}
	// At the basic level, the advanced checks don't run.
	got, _ = Live(t.Context(), LiveOptions{URL: "https://shop.test/", Hosts: []string{"shop.test"}, Client: client})
	for _, f := range got {
		if rules[f.Rule].level > Basic {
			t.Errorf("basic: %+v", f)
		}
	}
}

func TestLiveLeavesGoodSitesAlone(t *testing.T) {
	good := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'")
		h.Set("Content-Type", "text/html")
		http.SetCookie(w, &http.Cookie{Name: "app_session", Value: "x", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		if r.Method == "TRACE" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// A single-page app answers every path with its page.
		_, _ = w.Write([]byte("<!doctype html><title>Shop</title><div id=app></div>"))
	})
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://shop.test"+r.URL.Path, http.StatusMovedPermanently)
	})
	client, _ := site(t, good, redirect)
	got, err := Live(t.Context(), LiveOptions{URL: "https://shop.test/", Level: Advanced, Hosts: []string{"shop.test"}, Client: client})
	if err != nil || len(got) != 0 {
		t.Errorf("%v %+v", err, got)
	}
}

func TestLiveHosts(t *testing.T) {
	if _, err := Live(t.Context(), LiveOptions{URL: "https://example.org/", Hosts: []string{"shop.test"}}); !errors.Is(err, ErrHost) {
		t.Errorf("another host: %v", err)
	}
	if _, err := Live(t.Context(), LiveOptions{URL: "ftp://shop.test/"}); err == nil {
		t.Error("an ftp URL")
	}
	for _, h := range []string{"localhost", "127.0.0.1", "::1", "app.localhost"} {
		if !allowedHost(h, nil) {
			t.Errorf("%s isn't allowed", h)
		}
	}
}

func TestLiveRedirectsAndCredentials(t *testing.T) {
	// The apex sends everything to www: scan www.
	apex := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://www.shop.test"+r.URL.Path, http.StatusMovedPermanently)
	})
	client, _ := site(t, apex, apex)
	if _, err := Live(t.Context(), LiveOptions{URL: "https://shop.test/", Hosts: []string{"shop.test"}, Client: client}); err == nil ||
		!strings.Contains(err.Error(), "--allow-host=www.shop.test") {
		t.Errorf("an off-host redirect: %v", err)
	}
	// http:// redirects to https://: the scan goes on there.
	tlsSite := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.env" {
			_, _ = w.Write([]byte("APP_KEY=x\n"))
			return
		}
		_, _ = w.Write([]byte("<h1>Shop</h1>"))
	})
	toHTTPS := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://shop.test"+r.URL.Path, http.StatusMovedPermanently)
	})
	client, _ = site(t, tlsSite, toHTTPS)
	got, err := Live(t.Context(), LiveOptions{URL: "http://shop.test/", Hosts: []string{"shop.test"}, Client: client})
	found := map[string]bool{}
	for _, f := range got {
		found[f.Rule+" "+f.Where] = true
	}
	if err != nil || !found["live.exposed /.env"] || !found["live.headers Strict-Transport-Security"] {
		t.Errorf("http to https: %v %v", err, found)
	}
	// A user or password in the URL is refused, never sent.
	if _, err := CheckURL("https://user:s3cret@shop.test/", []string{"shop.test"}); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("credentials: %v", err)
	}
}

func TestLiveOldTLS(t *testing.T) {
	page := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<h1>Shop</h1>")) })
	for _, tc := range []struct {
		name     string
		min, max uint16
	}{{"old only", tls.VersionTLS10, tls.VersionTLS11}, {"old and new", tls.VersionTLS10, tls.VersionTLS13}} {
		srv := httptest.NewUnstartedServer(page)
		srv.TLS = &tls.Config{MinVersion: tc.min, MaxVersion: tc.max}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		client, _ := siteOn(t, srv, page)
		got, err := Live(t.Context(), LiveOptions{URL: "https://shop.test/", Hosts: []string{"shop.test"}, Client: client, Dial: dials[client]})
		if err != nil || !slices.ContainsFunc(got, func(f Finding) bool { return f.Rule == "live.tls" && f.Severity == "problem" }) {
			t.Errorf("%s: %v %+v", tc.name, err, got)
		}
	}
}
