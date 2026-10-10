// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/supervisor"
	"anetos.dev/anetos/web"
)

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	app, err := anetos.New(anetos.WithSource(env), anetos.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

// startServer runs app until the test ends. It returns the server's base
// URL, a cancel func, and a wait func returning Run's result.
func startServer(t *testing.T, app *anetos.App, srv *web.Server) (string, context.CancelFunc, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var runErr error
	finished := make(chan struct{})
	go func() {
		runErr = app.Run(ctx)
		close(finished)
	}()
	wait := func() error {
		<-finished
		return runErr
	}
	t.Cleanup(func() {
		cancel()
		_ = wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for !srv.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("server never became ready")
		}
		time.Sleep(time.Millisecond)
	}
	return "http://" + srv.Addr(), cancel, wait
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestServerEndToEnd(t *testing.T) {
	app := newApp(t, config.Map{"APP_ENV": "development", "HTTP_ADDR": "127.0.0.1:0"})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	type in struct {
		ID int `path:"id"`
	}
	srv.Router().Get("/posts/{id}", web.H(func(c *web.Ctx, in in) (map[string]int, error) {
		if c.App() != app {
			return nil, errors.New("Ctx.App is not the app")
		}
		return map[string]int{"id": in.ID}, nil
	})).Name("posts.show")

	base, _, _ := startServer(t, app, srv)

	status, body, hdr := get(t, base+"/posts/42")
	if status != 200 || strings.TrimSpace(body) != `{"id":42}` {
		t.Errorf("GET /posts/42 = %d %s", status, body)
	}
	if hdr.Get("X-Request-ID") == "" || hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("default middleware headers missing: %v", hdr)
	}
	if hdr.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS outside production")
	}
	if status, body, _ := get(t, base+"/health/live"); status != 200 || !strings.Contains(body, "ok") {
		t.Errorf("live = %d %s", status, body)
	}
	if status, _, _ := get(t, base+"/health/ready"); status != 200 {
		t.Errorf("ready = %d", status)
	}

	var names []string
	for _, rt := range srv.Router().Routes() {
		names = append(names, rt.Name)
	}
	if strings.Join(names, ",") != "health.live,health.ready,posts.show" {
		t.Errorf("routes = %v", names)
	}
	for _, st := range app.Supervisor().Status() {
		if st.Name == "http" && (st.Stage != supervisor.StageIngress || st.Restart != supervisor.StopOnFailure || st.Roles[0] != "http") {
			t.Errorf("http component spec = %+v", st)
		}
	}
}

func TestGracefulShutdownFinishesInFlightRequests(t *testing.T) {
	app := newApp(t, config.Map{"HTTP_ADDR": "127.0.0.1:0", "HTTP_ACCESS_LOG": "false"})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	srv.Router().Get("/slow", func(c *web.Ctx) error {
		close(entered)
		time.Sleep(100 * time.Millisecond)
		if c.Err() != nil {
			return errors.New("request context canceled during graceful shutdown")
		}
		return c.Text(200, "finished")
	})
	base, cancel, wait := startServer(t, app, srv)

	result := make(chan string, 1)
	go func() {
		_, body, _ := get(t, base+"/slow")
		result <- body
	}()
	<-entered
	cancel() // SIGTERM equivalent while the request is in flight

	if body := <-result; body != "finished" {
		t.Errorf("in-flight request got %q", body)
	}
	if err := wait(); err != nil {
		t.Errorf("Run = %v", err)
	}
	if srv.Ready() {
		t.Error("server still ready after shutdown")
	}
}

func TestListenFailureStopsApp(t *testing.T) {
	app := newApp(t, config.Map{"HTTP_ADDR": "256.0.0.1:99999"})
	if _, err := web.NewServer(app); err != nil {
		t.Fatal(err)
	}
	err := app.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `component "http"`) {
		t.Fatalf("Run = %v, want the listen error", err)
	}
}

func TestServerConfig(t *testing.T) {
	cfg, err := web.LoadConfig(config.Map{
		"HTTP_ADDR": ":9000", "HTTP_MAX_BODY": "2MB", "HTTP_TRUSTED_PROXIES": "10.0.0.0/8",
		"HTTP_CORS_ORIGINS": "https://a.test", "HTTP_HEALTH_ROUTES": "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9000" || cfg.MaxBody != 2*config.MB || cfg.HealthRoutes || cfg.CORS.Origins[0] != "https://a.test" ||
		cfg.RequestTimeout != 30*time.Second || cfg.CORS.MaxAge != 10*time.Minute {
		t.Errorf("cfg = %+v", cfg)
	}

	_, err = web.LoadConfig(config.Map{"HTTP_TRUSTED_PROXIES": "nope", "HTTP_CORS_ORIGINS": "*", "HTTP_CORS_CREDENTIALS": "true"})
	for _, want := range []string{"HTTP_TRUSTED_PROXIES", "HTTP_CORS_CREDENTIALS"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %s", err, want)
		}
	}

	app := newApp(t, config.Map{})
	if _, err := web.NewServer(app, web.WithConfig(web.Config{})); err == nil {
		t.Error("empty Addr accepted")
	}
	cfg = web.DefaultConfig()
	cfg.Addr, cfg.HealthRoutes = ":0", false
	srv, err := web.NewServer(app, web.WithConfig(cfg))
	if err != nil || srv.Config().Addr != ":0" || srv.Config().ShutdownGrace != 15*time.Second || len(srv.Router().Routes()) != 0 {
		t.Errorf("WithConfig: %v %+v", err, srv)
	}
}

func TestReadinessEndpointReflectsSupervisor(t *testing.T) {
	app := newApp(t, config.Map{"HTTP_ADDR": "127.0.0.1:0"})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	// A readiness-aware component that never becomes ready.
	if err := app.Component(notReady{}); err != nil {
		t.Fatal(err)
	}
	base, _, _ := startServer(t, app, srv)
	status, body, _ := get(t, base+"/health/ready")
	var v map[string]string
	_ = json.Unmarshal([]byte(body), &v)
	if status != 503 || v["status"] != "unavailable" {
		t.Errorf("ready = %d %s", status, body)
	}
}

type notReady struct{}

func (notReady) Name() string { return "warming-up" }
func (notReady) Ready() bool  { return false }
func (notReady) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func TestShutdownBoundsLongRunningRequests(t *testing.T) {
	app := newApp(t, config.Map{"HTTP_ADDR": "127.0.0.1:0", "HTTP_ACCESS_LOG": "false",
		"HTTP_REQUEST_TIMEOUT": "0", "HTTP_WRITE_TIMEOUT": "0", "APP_SHUTDOWN_TIMEOUT": "1s"})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	stream := func(c *web.Ctx) error {
		c.Writer().WriteHeader(200)
		_ = http.NewResponseController(c.Writer()).Flush()
		entered <- struct{}{}
		<-c.Done() // ends only when shutdown cancels the request
		return nil
	}
	srv.Router().Get("/events", stream)
	srv.Router().Get("/polite", func(c *web.Ctx) error {
		c.Writer().WriteHeader(200)
		_ = http.NewResponseController(c.Writer()).Flush()
		entered <- struct{}{}
		select {
		case <-c.Done():
		case <-srv.Stopping(): // ends as soon as shutdown begins
		}
		return nil
	})
	base, cancel, wait := startServer(t, app, srv)
	for _, p := range []string{"/events", "/polite"} {
		go func() {
			req, _ := http.NewRequestWithContext(context.Background(), "GET", base+p, nil)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
	}
	<-entered
	<-entered
	start := time.Now()
	cancel()
	if err := wait(); err != nil {
		t.Errorf("Run = %v", err)
	}
	// Grace is capped at half of APP_SHUTDOWN_TIMEOUT (500ms), then the
	// stream is canceled; the whole shutdown stays within the budget.
	if d := time.Since(start); d < 400*time.Millisecond || d > time.Second {
		t.Errorf("shutdown took %v, want about 500ms", d)
	}
}

func TestServerCommands(t *testing.T) {
	app := newApp(t, config.Map{"HTTP_ADDR": "127.0.0.1:0"})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	srv.Router().Get("/posts/{id}", func(c *web.Ctx) error { return nil }).Name("posts.show")
	srv.Router().Handle("", "/any", func(c *web.Ctx) error { return nil })
	var out, errOut bytes.Buffer
	if code := app.ExecuteArgs(t.Context(), []string{"route:list"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"METHOD", "GET     /posts/{id}", "posts.show", "ANY     /any", "health.live"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	// serve runs the http role until the context ends.
	app2 := newApp(t, config.Map{"HTTP_ADDR": "127.0.0.1:0"})
	srv2, err := web.NewServer(app2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int)
	go func() { done <- app2.ExecuteArgs(ctx, []string{"serve"}, &out, &errOut) }()
	for !srv2.Ready() {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("serve: exit %d: %s", code, errOut.String())
	}
}

// health:check asks the running server, as a container's health check
// does from another process.
func TestHealthCheckCommand(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	env := config.Map{"HTTP_ADDR": fmt.Sprintf(":%d", port)}
	app := newApp(t, env)
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	_, stop, wait := startServer(t, app, srv)

	check := func(args ...string) (int, string) {
		t.Helper()
		other := newApp(t, env) // another process
		if _, err := web.NewServer(other); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		code := other.ExecuteArgs(context.Background(), append([]string{"health:check"}, args...), &out, &errOut)
		return code, out.String() + errOut.String()
	}
	if code, out := check(); code != 0 || strings.TrimSpace(out) != "ok" {
		t.Errorf("ready: %d %q", code, out)
	}
	if code, _ := check("--live", "--timeout=2s"); code != 0 {
		t.Errorf("live: %d", code)
	}
	stop()
	_ = wait()
	if code, out := check("--timeout=0s"); code != 2 || !strings.Contains(out, "--timeout must be positive") {
		t.Errorf("timeout 0: %d %q", code, out)
	}
	if code, out := check(); code != 1 || !strings.Contains(out, "health:check: Get \"http://127.0.0.1:") || strings.Count(out, "health:check") != 1 {
		t.Errorf("stopped: %d %q", code, out)
	}

	off := newApp(t, config.Map{"HTTP_HEALTH_ROUTES": "false"})
	if _, err := web.NewServer(off); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := off.ExecuteArgs(context.Background(), []string{"health:check"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "HTTP_HEALTH_ROUTES=false") {
		t.Errorf("routes off: %d %q", code, errOut.String())
	}
}

// TestLoadConfigPORT: a hosting platform's PORT sets the address unless
// HTTP_ADDR does.
func TestLoadConfigPORT(t *testing.T) {
	for _, c := range []struct {
		src  config.Map
		want string
	}{
		{config.Map{}, ":8080"},
		{config.Map{"PORT": "5000"}, ":5000"},
		{config.Map{"PORT": "5000", "HTTP_ADDR": "127.0.0.1:9000"}, "127.0.0.1:9000"},
	} {
		cfg, err := web.LoadConfig(c.src)
		if err != nil || cfg.Addr != c.want {
			t.Errorf("%v: %q, %v; want %q", c.src, cfg.Addr, err, c.want)
		}
	}
	if _, err := web.LoadConfig(config.Map{"PORT": "web"}); err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Errorf("PORT=web: %v", err)
	}
}
