// SPDX-License-Identifier: Apache-2.0

package secscan

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// LiveOptions says what Live checks.
type LiveOptions struct {
	// URL is the site's page to start from: https://example.com/.
	URL string
	// Level is basic or advanced.
	Level Level
	// Hosts are the hosts it may scan (APP_URL's, --allow-host); localhost
	// is always allowed.
	Hosts []string
	// Client sends the requests (tests); nil: one with a 10-second
	// timeout that doesn't follow redirects.
	Client *http.Client
	// Version is the tool's, for the User-Agent.
	Version string
	// Now is the time certificates are compared with (tests); zero: now.
	Now time.Time
	// Dial connects for the TLS version probe (tests); nil: net.Dialer's.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// ErrHost is returned for a URL whose host Live may not scan.
var ErrHost = errors.New("not a host this project serves")

// live checks a running site with a few GET requests (and, advanced, a
// TRACE): headers, cookies, TLS, the HTTPS redirect, exposed files,
// stack traces in error pages, CORS.
type live struct {
	opts     LiveOptions
	client   *http.Client
	base     *url.URL
	out      []Finding
	requests int
}

// maxRequests bounds the requests of a scan.
const maxRequests = 24

// CheckURL checks a --url before a scan: an http or https URL, without
// a user or password, on an allowed host (else ErrHost).
func CheckURL(raw string, hosts []string) (*url.URL, error) {
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return nil, fmt.Errorf("--url %q isn't an http or https URL", raw)
	}
	if base.User != nil {
		return nil, fmt.Errorf("--url %s has a user or password: the scan reads logged-out pages only, and never sends credentials", base.Redacted())
	}
	if !allowedHost(base.Hostname(), hosts) {
		return nil, fmt.Errorf("%s: %w (APP_URL's host, or localhost); scan only your own sites, or add it with --allow-host=%s", base.Hostname(), ErrHost, base.Hostname())
	}
	if base.Path == "" {
		base.Path = "/"
	}
	return base, nil
}

// ErrNoAnswer is returned when the site doesn't answer.
var ErrNoAnswer = errors.New("the site doesn't answer")

// liveDeadline bounds a whole scan.
const liveDeadline = time.Minute

// Live runs the live checks on opts.URL (see CheckURL). A site that
// doesn't answer is ErrNoAnswer; one that sends its page to another
// host, an error that says to scan that host.
func Live(ctx context.Context, opts LiveOptions) ([]Finding, error) {
	base, err := CheckURL(opts.URL, opts.Hosts)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, liveDeadline)
	defer cancel()
	l := &live{opts: opts, client: opts.Client, base: base}
	if l.client == nil {
		l.client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if l.opts.Now.IsZero() {
		l.opts.Now = time.Now()
	}
	start := *base
	resp, _, err := l.get(ctx, base.String(), nil)
	// Follow redirects on the same host (http to https included), to the
	// page itself: the rest of the scan starts from there.
	for i := 0; err == nil && i < 3; i++ {
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" {
			break
		}
		next, perr := l.base.Parse(loc)
		if perr != nil || next.User != nil {
			return nil, fmt.Errorf("%s redirects to %q, which isn't a URL to follow", l.base.Redacted(), loc)
		}
		if next.Hostname() != l.base.Hostname() {
			return nil, fmt.Errorf("%s redirects to %s: scan that address (--url=%s), with --allow-host=%s if it's yours", l.base.Redacted(), next.Host, next.Redacted(), next.Hostname())
		}
		l.base = next
		resp, _, err = l.get(ctx, next.String(), nil)
	}
	if err != nil {
		var cert *tls.CertificateVerificationError
		switch {
		case errors.As(err, &cert) || strings.Contains(err.Error(), "x509:"):
			l.add("live.tls", "problem", l.base.Host, "the certificate doesn't verify ("+shortErr(err)+"): browsers refuse the site, and clients that accept it can be fooled; serve a certificate for this host from a trusted authority (Let's Encrypt)")
			return l.out, nil
		case strings.Contains(err.Error(), "protocol version"):
			l.add("live.tls", "problem", l.base.Host, "the site offers only TLS older than 1.2, which is broken and which browsers refuse: allow TLS 1.2 and 1.3")
			return l.out, nil
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrNoAnswer, l.base.Redacted(), err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("%s keeps redirecting (%d): give the page's own address as --url", start.Redacted(), resp.StatusCode)
	}
	l.headers(resp)
	l.cookies(resp)
	l.tls(ctx, resp)
	l.httpsRedirect(ctx)
	l.exposed(ctx)
	l.errorPages(ctx)
	if opts.Level >= Advanced {
		l.cors(ctx)
		l.trace(ctx)
		l.versions(resp)
	}
	return l.out, nil
}

// allowedHost reports whether host may be scanned.
func allowedHost(host string, hosts []string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	if strings.HasSuffix(host, ".localhost") {
		return true
	}
	return slices.ContainsFunc(hosts, func(h string) bool { return strings.EqualFold(h, host) })
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && strings.Contains(s[i:], "x509") {
		return s[i+2:]
	}
	return s
}

func (l *live) add(rule, severity, where, msg string) {
	l.out = append(l.out, Finding{Rule: rule, Severity: severity, Where: where, Message: msg})
}

// page is a response, read.
type page struct {
	StatusCode int
	Header     http.Header
	TLS        *tls.ConnectionState
	cookies    []*http.Cookie
}

// Cookies are the cookies the response sets.
func (p *page) Cookies() []*http.Cookie { return p.cookies }

// get sends a GET (or method, with header) and reads up to 256 KB of
// the body.
func (l *live) get(ctx context.Context, target string, header http.Header) (*page, []byte, error) {
	return l.do(ctx, http.MethodGet, target, header)
}

func (l *live) do(ctx context.Context, method, target string, header http.Header) (*page, []byte, error) {
	if l.requests >= maxRequests {
		return nil, nil, errors.New("too many requests")
	}
	l.requests++
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, nil, err
	}
	maps.Copy(req.Header, header)
	req.Header.Set("User-Agent", "anetos-security-check/"+cmpString(l.opts.Version, "dev")+" (+https://docs.anetos.dev/guides/security-check/)")
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return &page{StatusCode: resp.StatusCode, Header: resp.Header, TLS: resp.TLS, cookies: resp.Cookies()}, body, nil
}

func cmpString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// path is the site's URL with p as its path.
func (l *live) path(p string) string {
	u := *l.base
	u.Path, u.RawQuery, u.Fragment = p, "", ""
	return u.String()
}

func (l *live) headers(resp *page) {
	h := resp.Header
	if l.base.Scheme == "https" && h.Get("Strict-Transport-Security") == "" {
		l.add("live.headers", "warning", "Strict-Transport-Security", "the site doesn't send Strict-Transport-Security: a visitor's first request can go over plain HTTP and be redirected anywhere; send it in production (web.SecureHeaders does, with APP_ENV=production)")
	}
	if !strings.EqualFold(h.Get("X-Content-Type-Options"), "nosniff") {
		l.add("live.headers", "warning", "X-Content-Type-Options", "the site doesn't send X-Content-Type-Options: nosniff: browsers may run an upload as a script; send it (web.SecureHeaders does)")
	}
	csp := h.Get("Content-Security-Policy")
	if h.Get("X-Frame-Options") == "" && !strings.Contains(csp, "frame-ancestors") {
		l.add("live.headers", "warning", "X-Frame-Options", "the site can be framed by any other (no X-Frame-Options, no CSP frame-ancestors): clickjacking; send X-Frame-Options: DENY or SAMEORIGIN (web.SecureHeaders does)")
	}
	if h.Get("Referrer-Policy") == "" {
		l.add("live.headers", "note", "Referrer-Policy", "the site doesn't send Referrer-Policy: other sites see the full URLs your pages link from; send strict-origin-when-cross-origin (web.SecureHeaders does)")
	}
	if csp == "" && strings.Contains(h.Get("Content-Type"), "html") {
		l.add("live.headers", "note", "Content-Security-Policy", "the page has no Content-Security-Policy: an injected script runs; add a policy that fits your pages (Secure your app, step 5)")
	}
}

// sessionCookie is a cookie name that holds a session or a login.
var sessionCookie = regexp.MustCompile(`(?i)(sess|sid|auth|token|login|remember|jwt|identity)`)

// csrfCookie is a cookie scripts read on purpose (XSRF-TOKEN).
var csrfCookie = regexp.MustCompile(`(?i)(csrf|xsrf)`)

func (l *live) cookies(resp *page) {
	for _, c := range resp.Cookies() {
		session := sessionCookie.MatchString(c.Name) && !csrfCookie.MatchString(c.Name)
		if l.base.Scheme == "https" && !c.Secure {
			sev := "warning"
			if session {
				sev = "problem"
			}
			l.add("live.cookie", sev, c.Name, fmt.Sprintf("the cookie %s isn't Secure: browsers send it over plain HTTP too, where anyone on the network reads it; set Secure (SESSION_SECURE=true for the session)", c.Name))
		}
		if session && !c.HttpOnly {
			l.add("live.cookie", "warning", c.Name, fmt.Sprintf("the cookie %s isn't HttpOnly: an injected script can read the session; set HttpOnly", c.Name))
		}
		if c.SameSite == http.SameSiteNoneMode && !c.Secure && !allowedHost(l.base.Hostname(), nil) {
			l.add("live.cookie", "problem", c.Name, fmt.Sprintf("the cookie %s is SameSite=None without Secure: browsers drop it, or send it with every other site's requests; set Secure, or SameSite=Lax", c.Name))
		}
	}
}

func (l *live) tls(ctx context.Context, resp *page) {
	if resp.TLS == nil {
		if l.base.Scheme == "http" && !allowedHost(l.base.Hostname(), nil) {
			l.add("live.https", "problem", l.base.Host, "the site is served over plain HTTP: anyone on the network reads and changes its pages, passwords and cookies; serve it over HTTPS")
		}
		return
	}
	if l.oldTLS(ctx) {
		l.add("live.tls", "problem", l.base.Host, "the site accepts TLS 1.0 or 1.1, which are broken: a client tricked into them can be read; allow TLS 1.2 and 1.3 only")
	}
	if l.opts.Level >= Advanced && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		if left := cert.NotAfter.Sub(l.opts.Now); left < 30*24*time.Hour {
			l.add("live.cert-expiry", "warning", l.base.Host, fmt.Sprintf("the certificate expires on %s, in %d days: renew it, and check that renewing is automatic", cert.NotAfter.Format(time.DateOnly), int(left.Hours()/24)))
		}
	}
}

// oldTLS reports whether the site completes a handshake offering only
// TLS 1.0 and 1.1 (a request of the budget).
func (l *live) oldTLS(ctx context.Context) bool {
	if l.requests >= maxRequests {
		return false
	}
	l.requests++
	addr := l.base.Host
	if l.base.Port() == "" {
		addr = net.JoinHostPort(l.base.Hostname(), "443")
	}
	dial := l.opts.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	c := tls.Client(conn, &tls.Config{ServerName: l.base.Hostname(), MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11, //nolint:gosec // the probe: does the site accept them?
		VerifyConnection: func(tls.ConnectionState) error { return nil }, InsecureSkipVerify: true}) //nolint:gosec // the certificate was checked by the first request
	return c.HandshakeContext(ctx) == nil
}

// httpsRedirect checks that the site's http:// address redirects to
// https://.
func (l *live) httpsRedirect(ctx context.Context) {
	if l.base.Scheme != "https" || allowedHost(l.base.Hostname(), nil) {
		return // localhost is left alone
	}
	u := *l.base
	u.Scheme, u.User = "http", nil
	if p := u.Port(); p != "" {
		return // a custom port: its http address isn't known
	}
	resp, _, err := l.get(ctx, u.String(), nil)
	if err != nil {
		return // nothing listens on port 80: fine
	}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		if next, err := u.Parse(resp.Header.Get("Location")); err != nil || next.Scheme != "https" {
			l.add("live.https", "warning", u.Host, "http://"+u.Host+" redirects, but not to https: visitors who type the address stay on plain HTTP; redirect to https://")
		}
	case resp.StatusCode < 300:
		l.add("live.https", "problem", u.Host, "http://"+u.Host+" serves the site over plain HTTP instead of redirecting to https: anyone on the network reads and changes it; redirect to https:// (your proxy, or Cloudflare's \"Always use HTTPS\")")
	}
}

// exposedFiles are files a site must not serve, with what shows they're
// the real thing (not a page that answers every path).
var exposedFiles = []struct {
	path, what string
	is         *regexp.Regexp
}{
	{"/.env", "the settings file, with its secrets", regexp.MustCompile(`(?m)^[A-Z][A-Z0-9_]*=`)},
	{"/.env.production", "the settings file, with its secrets", regexp.MustCompile(`(?m)^[A-Z][A-Z0-9_]*=`)},
	{"/.git/config", "the git repository (and so the code)", regexp.MustCompile(`(?m)^\[core\]`)},
	{"/.git/HEAD", "the git repository (and so the code)", regexp.MustCompile(`^ref: refs/`)},
	{"/debug/pprof/", "Go's profiler (memory, goroutines, settings)", regexp.MustCompile(`Types of profiles available`)},
	{"/debug/vars", "expvar (the command line, memory statistics)", regexp.MustCompile(`"(cmdline|memstats)"`)},
}

func (l *live) exposed(ctx context.Context) {
	for _, f := range exposedFiles {
		resp, body, err := l.get(ctx, l.path(f.path), nil)
		if err != nil || resp.StatusCode != http.StatusOK || !f.is.Match(body) {
			continue
		}
		l.add("live.exposed", "problem", f.path, fmt.Sprintf("the site serves %s, %s: anyone can download it; stop serving it (deploy only the binary; block dotfiles and /debug/ at the proxy), and change the secrets it held", f.path, f.what))
	}
}

// stackTrace matches a Go stack trace or panic in a page.
var stackTrace = regexp.MustCompile(`goroutine \d+ \[|panic: |\.go:\d+ \+0x|runtime/debug\.Stack`)

func (l *live) errorPages(ctx context.Context) {
	for _, p := range []struct{ where, path string }{
		{"404 page", "anetos-security-check-" + fmt.Sprint(l.opts.Now.Unix())},
		{"malformed path", "%ff%fe"},
	} {
		resp, body, err := l.get(ctx, l.path("/")+p.path, nil)
		if err != nil {
			continue
		}
		if stackTrace.Match(body) {
			l.add("live.debug", "problem", p.where, fmt.Sprintf("the error page (%d, for /%s) shows a stack trace: visitors learn the code's files and functions; turn APP_DEBUG off where others reach the app", resp.StatusCode, p.path))
			return
		}
	}
}

func (l *live) cors(ctx context.Context) {
	origin := "https://anetos-security-check.example"
	resp, _, err := l.get(ctx, l.base.String(), http.Header{"Origin": {origin}})
	if err != nil {
		return
	}
	allow := resp.Header.Get("Access-Control-Allow-Origin")
	creds := strings.EqualFold(resp.Header.Get("Access-Control-Allow-Credentials"), "true")
	switch {
	case allow == origin && creds:
		l.add("live.cors", "problem", "Access-Control-Allow-Origin", "the site allows any origin's scripts to read its responses with the visitor's cookies (it echoes Origin, with credentials): any site can act as the logged-in user; allow only your own origins (HTTP_CORS_ORIGINS)")
	case allow == origin:
		l.add("live.cors", "warning", "Access-Control-Allow-Origin", "the site echoes any Origin in Access-Control-Allow-Origin: any site's scripts can read its responses; allow only the origins you expect (HTTP_CORS_ORIGINS)")
	}
}

func (l *live) trace(ctx context.Context) {
	marker := "anetos-trace-" + fmt.Sprint(l.opts.Now.UnixNano())
	resp, body, err := l.do(ctx, "TRACE", l.base.String(), http.Header{"X-Anetos-Trace": {marker}})
	if err == nil && resp.StatusCode == http.StatusOK && strings.Contains(string(body), marker) {
		l.add("live.trace", "warning", "TRACE", "the site answers TRACE, echoing the request's headers: a script can read headers it shouldn't (cross-site tracing); turn TRACE off at the proxy")
	}
}

// versioned is a header value that names a version: nginx/1.18.0.
var versioned = regexp.MustCompile(`\d+\.\d+`)

func (l *live) versions(resp *page) {
	for _, h := range []string{"Server", "X-Powered-By", "X-AspNet-Version"} {
		if v := resp.Header.Get(h); v != "" && versioned.MatchString(v) {
			l.add("live.version", "note", h, fmt.Sprintf("%s: %s tells everyone which version runs, and so which advisories apply; hide the version at the proxy", h, v))
		}
	}
}
