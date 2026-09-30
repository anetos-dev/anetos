// SPDX-License-Identifier: Apache-2.0

// Package devserver implements `anetos dev`: it watches the project,
// regenerates code (templ, anetos gen), rebuilds and restarts the app on
// every change, and serves it through a reverse proxy on a stable address
// that reloads open pages when the new version is up, or shows the build
// error.
package devserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos/cli/internal/modelgen"
)

// Options configures [Run].
type Options struct {
	Dir      string        // project root (with go.mod)
	Addr     string        // proxy address, e.g. ":8080"
	Args     []string      // app arguments; default "run"
	Poll     time.Duration // how often to look for changes; default 300ms
	Out      io.Writer     // progress and the app's output
	OnListen func(addr string)
}

// Run serves the app with live reload until ctx is canceled.
func Run(ctx context.Context, o Options) error {
	if o.Poll <= 0 {
		o.Poll = 300 * time.Millisecond
	}
	if len(o.Args) == 0 {
		o.Args = []string{"run"}
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	d := &dev{ctx: ctx, opts: o, ready: make(chan struct{}), clients: map[chan struct{}]bool{}}
	d.bin = filepath.Join(o.Dir, "tmp", "anetos-dev", "app")
	if runtime.GOOS == "windows" {
		d.bin += ".exe"
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", o.Addr)
	if err != nil {
		return fmt.Errorf("anetos dev: %w", err)
	}
	srv := &http.Server{Handler: d, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	addr := ln.Addr().String()
	if o.OnListen != nil {
		o.OnListen(addr)
	}
	d.logf("serving on http://%s", displayAddr(addr))

	defer d.stop()
	snap := snapshot(o.Dir)
	d.rebuild()
	tick := time.NewTicker(o.Poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		now := snapshot(o.Dir)
		changed := diff(snap, now)
		if len(changed) == 0 {
			continue
		}
		// Wait for the editor to finish writing.
		for {
			time.Sleep(o.Poll / 3)
			again := snapshot(o.Dir)
			if len(diff(now, again)) == 0 {
				break
			}
			now = again
		}
		snap = now
		d.logf("changed: %s", strings.Join(changed, ", "))
		d.rebuild()
	}
}

type dev struct {
	ctx  context.Context
	opts Options
	bin  string

	mu      sync.Mutex
	target  *url.URL      // the running app, or nil
	ready   chan struct{} // closed when target is set or failure is known
	failure string        // build or start error, shown in the browser
	proc    *exec.Cmd
	exited  chan struct{}
	tail    *tailBuffer
	clients map[chan struct{}]bool
}

func (d *dev) logf(format string, args ...any) {
	fmt.Fprintf(d.opts.Out, "anetos dev: "+format+"\n", args...)
}

// rebuild stops the app, regenerates code, builds and starts it, then
// tells the browsers to reload.
func (d *dev) rebuild() {
	// New requests wait for the new version from now on, not for the app
	// that is shutting down.
	d.mu.Lock()
	d.target, d.failure = nil, ""
	d.ready = make(chan struct{})
	d.mu.Unlock()
	d.stop()

	start := time.Now()
	if err := d.build(); err != nil {
		d.fail("Build failed", err.Error())
		return
	}
	if err := d.start(); err != nil {
		d.fail("The app didn't start", err.Error())
		return
	}
	d.logf("ready in %s", time.Since(start).Round(10*time.Millisecond))
	d.reload()
}

func (d *dev) fail(title, detail string) {
	d.logf("%s:\n%s", strings.ToLower(title), detail)
	d.mu.Lock()
	d.failure = title + "\n\n" + detail
	close(d.ready)
	d.mu.Unlock()
	d.reload()
}

func (d *dev) build() error {
	dir := d.opts.Dir
	if hasFiles(dir, ".templ") {
		if out, err := d.run(dir, "go", "tool", "templ", "generate"); err != nil {
			return fmt.Errorf("templ generate: %w\n%s", err, out)
		}
	}
	changes, err := modelgen.Generate(dir, "./...")
	if err == nil {
		err = modelgen.Apply(changes)
	}
	if err != nil {
		return fmt.Errorf("anetos gen: %w", err)
	}
	if out, err := d.run(dir, "go", "build", "-o", d.bin, "."); err != nil {
		return fmt.Errorf("%s", bytes.TrimSpace(out))
	}
	return nil
}

func (d *dev) run(dir, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(d.ctx, name, args...)
	c.Dir = dir
	return c.CombinedOutput()
}

// start runs the built app on a free local port and waits until it
// accepts connections.
func (d *dev) start() error {
	port, err := freePort(d.ctx)
	if err != nil {
		return err
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	c := exec.CommandContext(d.ctx, d.bin, d.opts.Args...)
	// When dev stops, the app gets an interrupt and 10s to shut down (on
	// Windows, where interrupts can't be sent, it is killed).
	if runtime.GOOS != "windows" {
		c.Cancel = func() error { return c.Process.Signal(os.Interrupt) }
	}
	c.WaitDelay = 10 * time.Second
	c.SysProcAttr = sysProcAttr() // Linux: the app dies with anetos dev
	c.Dir = d.opts.Dir
	c.Env = append(os.Environ(), "HTTP_ADDR="+addr)
	tail := &tailBuffer{max: 8 << 10}
	c.Stdout = io.MultiWriter(d.opts.Out, tail)
	c.Stderr = io.MultiWriter(d.opts.Out, tail)
	if err := c.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	go func() {
		_ = c.Wait()
		close(exited)
		d.mu.Lock()
		crashed := d.proc == c && d.target != nil // not stopped by us, and it was up
		if crashed {
			d.target = nil
			d.failure = fmt.Sprintf("The app stopped\n\nIt exited with %s:\n\n%s", c.ProcessState, tail.String())
		}
		d.mu.Unlock()
		if crashed {
			d.logf("the app stopped (%s); waiting for a change", c.ProcessState)
			d.reload()
		}
	}()
	d.mu.Lock()
	d.proc, d.exited, d.tail = c, exited, tail
	d.mu.Unlock()

	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-exited:
			return fmt.Errorf("it exited with %s:\n\n%s", c.ProcessState, tail.String())
		default:
		}
		if conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(d.ctx, "tcp", addr); err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("it didn't listen on %s within 30s (does it start an HTTP server with web.NewServer?)", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	d.mu.Lock()
	d.target = &url.URL{Scheme: "http", Host: addr}
	close(d.ready)
	d.mu.Unlock()
	return nil
}

// stop ends the running app: interrupt, then kill after 10s.
func (d *dev) stop() {
	d.mu.Lock()
	c, exited := d.proc, d.exited
	d.proc = nil
	d.mu.Unlock()
	if c == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = c.Process.Kill()
	} else {
		_ = c.Process.Signal(os.Interrupt)
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = c.Process.Kill()
		<-exited
	}
}

func freePort(ctx context.Context) (int, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// ---- Proxy ----

const reloadPath = "/_anetos/dev/reload"

// reloadScript reconnects after the dev server restarts and reloads the
// page when told to.
const reloadScript = `<script>(()=>{const es=new EventSource("` + reloadPath + `");es.addEventListener("reload",()=>location.reload());})()</script>`

func (d *dev) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == reloadPath {
		d.events(w, r)
		return
	}
	var target *url.URL
	var failure string
	timeout := time.After(60 * time.Second)
	for {
		d.mu.Lock()
		ready := d.ready
		d.mu.Unlock()
		select {
		case <-ready:
		case <-r.Context().Done():
			return
		case <-timeout:
			http.Error(w, "anetos dev: the app is still starting", http.StatusServiceUnavailable)
			return
		}
		d.mu.Lock()
		current := d.ready == ready
		target, failure = d.target, d.failure
		d.mu.Unlock()
		if current {
			break // otherwise a rebuild started meanwhile: wait for it
		}
	}
	if target == nil {
		errorPage(w, failure)
		return
	}
	p := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host // the app sees the address the browser uses
		},
		FlushInterval:  -1,
		ModifyResponse: injectReload,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "anetos dev: "+err.Error(), http.StatusBadGateway)
		},
	}
	p.ServeHTTP(w, r)
}

// injectReload adds the reload script to HTML pages.
func injectReload(res *http.Response) error {
	ct := res.Header.Get("Content-Type")
	switch {
	case !strings.HasPrefix(ct, "text/html"), res.Header.Get("Content-Encoding") != "",
		res.Request.Header.Get("HX-Request") != "", res.Request.Method == http.MethodHead,
		res.StatusCode < 200, res.StatusCode == http.StatusNoContent, res.StatusCode == http.StatusNotModified:
		return nil
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		return err
	}
	body = inject(body)
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	res.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

func inject(body []byte) []byte {
	if i := lastIndexFold(body, "</body>"); i >= 0 {
		return append(body[:i:i], append([]byte(reloadScript), body[i:]...)...)
	}
	return append(body, reloadScript...)
}

// lastIndexFold is bytes.LastIndex ignoring ASCII case; other bytes (in
// UTF-8 text) compare exactly, so indexes stay those of body.
func lastIndexFold(body []byte, s string) int {
	for i := len(body) - len(s); i >= 0; i-- {
		match := true
		for j := range len(s) {
			c := body[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != s[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// events streams "reload" events to a page.
func (d *dev) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)
	ch := make(chan struct{}, 1)
	d.mu.Lock()
	d.clients[ch] = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.clients, ch)
		d.mu.Unlock()
	}()
	_, _ = io.WriteString(w, ": connected\n\n")
	_ = rc.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if _, err := io.WriteString(w, "event: reload\ndata: {}\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case <-time.After(30 * time.Second):
			_, _ = io.WriteString(w, ": ping\n\n")
			_ = rc.Flush()
		}
	}
}

func (d *dev) reload() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for ch := range d.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func errorPage(w http.ResponseWriter, failure string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	title, detail, _ := strings.Cut(failure, "\n\n")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title>
<style>body{font:15px/1.5 system-ui,sans-serif;margin:2rem;background:#fff8f7;color:#1d1d1f}pre{white-space:pre-wrap;background:#fff;border:1px solid #f0c0bb;padding:1rem;border-radius:6px;font-size:13px}</style>
</head><body><h1>%s</h1><pre>%s</pre><p>Fix it and save: the page reloads.</p>%s</body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(detail), reloadScript)
}

// ---- Watching ----

type fileState struct {
	mod  time.Time
	size int64
}

// skipDirs are never watched; skipRootDirs only at the project's root.
var (
	skipDirs     = map[string]bool{"node_modules": true, "testdata": true}
	skipRootDirs = map[string]bool{"tmp": true, "vendor": true, "bin": true, "storage": true}
)

func skipDir(rel, name string) bool {
	return skipDirs[name] || strings.HasPrefix(name, ".") || skipRootDirs[name] && !strings.ContainsAny(rel, `/\`)
}

// watched reports whether a change to the file needs a rebuild. Generated
// files are left out, since the build writes them.
func watched(rel string) bool {
	base := filepath.Base(rel)
	switch {
	case strings.HasSuffix(base, "_templ.go"), base == modelgen.FileName, strings.HasSuffix(base, "_test.go"):
		return false
	case strings.HasSuffix(base, ".go"), strings.HasSuffix(base, ".templ"),
		base == "go.mod", base == "go.sum", base == ".env", strings.HasPrefix(base, ".env."):
		return true
	}
	// Other files under public/ are embedded static files.
	return strings.HasPrefix(filepath.ToSlash(rel), "public/")
}

func snapshot(root string) map[string]fileState {
	out := map[string]fileState{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel != "." && skipDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !watched(rel) {
			return nil
		}
		if info, err := d.Info(); err == nil {
			out[rel] = fileState{info.ModTime(), info.Size()}
		}
		return nil
	})
	return out
}

func diff(before, after map[string]fileState) []string {
	var changed []string
	for p, s := range after {
		if b, ok := before[p]; !ok || b != s {
			changed = append(changed, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changed = append(changed, p)
		}
	}
	if len(changed) > 5 {
		changed = append(changed[:5], fmt.Sprintf("and %d more", len(changed)-5))
	}
	return changed
}

func hasFiles(root, ext string) bool {
	found := errors.New("found")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if d.IsDir() && path != root {
			if rel, _ := filepath.Rel(root, path); skipDir(rel, d.Name()) {
				return filepath.SkipDir
			}
		}
		if !d.IsDir() && strings.HasSuffix(path, ext) {
			return found
		}
		return nil
	})
	return errors.Is(err, found)
}

// tailBuffer keeps the last max bytes written, for error pages.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}
