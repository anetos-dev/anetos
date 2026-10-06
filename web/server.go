// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/supervisor"
)

// Config configures the HTTP server. Environment keys use the HTTP_ prefix,
// for example HTTP_ADDR; see docs/site/reference/configuration.md.
type Config struct {
	Addr              string          `env:"ADDR" default:":8080"`              // address to listen on
	ReadHeaderTimeout time.Duration   `env:"READ_HEADER_TIMEOUT" default:"10s"` // as http.Server's
	ReadTimeout       time.Duration   `env:"READ_TIMEOUT" default:"30s"`        // as http.Server's
	WriteTimeout      time.Duration   `env:"WRITE_TIMEOUT" default:"30s"`       // as http.Server's
	IdleTimeout       time.Duration   `env:"IDLE_TIMEOUT" default:"2m"`         // as http.Server's
	ShutdownGrace     time.Duration   `env:"SHUTDOWN_GRACE" default:"15s"`      // wait for in-flight requests before canceling them; capped at half of APP_SHUTDOWN_TIMEOUT
	RequestTimeout    time.Duration   `env:"REQUEST_TIMEOUT" default:"30s"`     // context deadline per request; 0 disables
	MaxBody           config.ByteSize `env:"MAX_BODY" default:"10MB"`           // 0 disables
	TrustedProxies    []string        `env:"TRUSTED_PROXIES"`                   // IPs or CIDRs whose forwarding headers are trusted
	AccessLog         bool            `env:"ACCESS_LOG" default:"true"`         // log every request (health checks at debug level)
	HealthRoutes      bool            `env:"HEALTH_ROUTES" default:"true"`      // GET /health/live and /health/ready
	CORS              CORSConfig      `prefix:"CORS_"`                          // HTTP_CORS_*
}

// Validate implements config.Validator.
func (c Config) Validate() error {
	var errs []error
	if c.Addr == "" {
		errs = append(errs, errors.New("HTTP_ADDR must not be empty"))
	}
	if c.ShutdownGrace <= 0 {
		errs = append(errs, errors.New("HTTP_SHUTDOWN_GRACE must be positive"))
	}
	if _, err := ParsePrefixes(c.TrustedProxies); err != nil {
		errs = append(errs, fmt.Errorf("HTTP_TRUSTED_PROXIES: %w", err))
	}
	if c.CORS.Credentials && slices.Contains(c.CORS.Origins, "*") {
		errs = append(errs, errors.New(`HTTP_CORS_CREDENTIALS=true can't be combined with HTTP_CORS_ORIGINS="*"; list the origins`))
	}
	return errors.Join(errs...)
}

// DefaultConfig returns the configuration used when no HTTP_* variables are
// set. Start from it when passing a config with [WithConfig].
func DefaultConfig() Config {
	cfg, err := LoadConfig(config.Map{})
	if err != nil {
		panic(err) // the defaults are valid; a failure is a bug
	}
	return cfg
}

// LoadConfig reads the HTTP_* settings from src.
func LoadConfig(src config.Source) (Config, error) {
	var wrapper struct {
		HTTP Config `prefix:"HTTP_"`
	}
	err := config.Bind(src, &wrapper)
	return wrapper.HTTP, err
}

// Server is the application's HTTP server: a [Router] plus a supervised
// component that listens, reports readiness and shuts down gracefully.
type Server struct {
	cfg    Config
	app    *anetos.App
	router *Router
	log    *slog.Logger

	ready        atomic.Bool
	mu           sync.Mutex
	addr         string
	stopping     chan struct{}
	stoppingOnce sync.Once
}

// ServerOption configures [NewServer].
type ServerOption func(*serverOptions)

type serverOptions struct {
	cfg *Config
}

// WithConfig uses cfg instead of reading HTTP_* settings. Start from
// [DefaultConfig] so unset fields keep their defaults.
func WithConfig(cfg Config) ServerOption {
	return func(o *serverOptions) { o.cfg = &cfg }
}

// NewServer creates the HTTP server for app and adds it to the app as the
// "http" component (role "http", shutdown stage [supervisor.StageIngress],
// and [supervisor.StopOnFailure], since the app is useless if it can't
// serve). Register routes on [Server.Router] before calling app.Run.
//
// The router comes with these global middleware, outermost first: Recover,
// RequestIDs, RealIP, AccessLog (HTTP_ACCESS_LOG), SecureHeaders (HSTS in
// production), CORS (when HTTP_CORS_ORIGINS is set), the request's locale
// (with i18n.ForApp: the locale in the URL with LOCALE_URL, the redirects
// to the visitor's locale, ?locale= switches; see [LocaleURL]), BodyLimit
// (HTTP_MAX_BODY) and Timeout (HTTP_REQUEST_TIMEOUT). Unless
// HTTP_HEALTH_ROUTES=false, it also serves GET /health/live and
// GET /health/ready.
func NewServer(app *anetos.App, opts ...ServerOption) (*Server, error) {
	var o serverOptions
	for _, opt := range opts {
		opt(&o)
	}
	var cfg Config
	if o.cfg != nil {
		cfg = *o.cfg
		if err := cfg.Validate(); err != nil {
			return nil, fmt.Errorf("web: invalid config: %w", err)
		}
	} else {
		var err error
		if cfg, err = LoadConfig(app.Source()); err != nil {
			return nil, err
		}
	}
	trusted, _ := ParsePrefixes(cfg.TrustedProxies) // validated above

	s := &Server{cfg: cfg, app: app, log: app.Logger(), stopping: make(chan struct{})}
	s.router = NewRouter(WithApp(app))

	global := []Middleware{Recover(s.log), RequestIDs, RealIP(trusted), units(app)}
	if cfg.AccessLog {
		global = append(global, AccessLog(s.log))
	}
	global = append(global, SecureHeaders(app.Config().Env.IsProduction()))
	if len(cfg.CORS.Origins) > 0 {
		global = append(global, CORS(cfg.CORS))
	}
	global = append(global, localize(app), BodyLimit(int64(cfg.MaxBody)), Timeout(cfg.RequestTimeout))
	s.router.UseGlobal(global...)

	if cfg.HealthRoutes {
		s.router.Get("/health/live", func(c *Ctx) error {
			return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
		}).Name("health.live")
		s.router.Get("/health/ready", func(c *Ctx) error {
			if !app.Supervisor().Ready() {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			}
			return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
		}).Name("health.ready")
	}

	err := app.Component(s,
		anetos.Roles("http"),
		anetos.Stage(supervisor.StageIngress),
		anetos.Restart(supervisor.StopOnFailure),
	)
	if err != nil {
		return nil, err
	}
	for _, c := range s.commands(app) {
		if err := app.AddCommand(c); err != nil {
			return nil, err
		}
	}
	s.addCheck(app)
	anetos.Provide(app, s) // for plugins' routes (ext.Load)
	return s, nil
}

// commands are the binary commands the server adds: serve, health:check
// and routes:list.
func (s *Server) commands(app *anetos.App) []cmd.Command {
	return []cmd.Command{
		{
			Name:        "serve",
			Description: "Run the HTTP server (components with the http role, and those without roles)",
			ManagesApp:  true,
			Run: func(ctx context.Context, args *cmd.Args) error {
				fs := flag.NewFlagSet("serve", flag.ContinueOnError)
				if err := args.Parse(fs); err != nil {
					return err
				}
				if fs.NArg() > 0 {
					return cmd.Usagef("unexpected argument %q", fs.Arg(0))
				}
				return app.Run(ctx, "http")
			},
		},
		{
			Name:        "health:check",
			Usage:       "[--live] [--timeout=5s]",
			Description: "Ask the running server whether it is ready (or, with --live, alive), for container health checks; exits 1 if not",
			ManagesApp:  true, // asks the server over HTTP: nothing to boot
			Run: func(ctx context.Context, args *cmd.Args) error {
				fs := flag.NewFlagSet("health:check", flag.ContinueOnError)
				live := fs.Bool("live", false, "check /health/live instead of /health/ready")
				timeout := fs.Duration("timeout", 5*time.Second, "how long to wait for the answer")
				if err := args.Parse(fs); err != nil {
					return err
				}
				if fs.NArg() > 0 {
					return cmd.Usagef("unexpected argument %q", fs.Arg(0))
				}
				if *timeout <= 0 {
					return cmd.Usagef("--timeout must be positive")
				}
				if !s.cfg.HealthRoutes {
					return errors.New("the health routes are off (HTTP_HEALTH_ROUTES=false)")
				}
				path := "/health/ready"
				if *live {
					path = "/health/live"
				}
				return checkHealth(ctx, "http://"+localAddr(s.cfg.Addr)+path, *timeout, args.Stdout)
			},
		},
		{
			Name:        "routes:list",
			Description: "List the HTTP routes",
			Run: func(ctx context.Context, args *cmd.Args) error {
				fs := flag.NewFlagSet("routes:list", flag.ContinueOnError)
				if err := args.Parse(fs); err != nil {
					return err
				}
				if fs.NArg() > 0 {
					return cmd.Usagef("unexpected argument %q", fs.Arg(0))
				}
				tw := tabwriter.NewWriter(args.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "METHOD\tPATH\tNAME")
				for _, rt := range s.router.Routes() {
					m := rt.Method
					if m == "" {
						m = "ANY"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\n", m, rt.Host+rt.Pattern, rt.Name) // a host's routes: admin.example.com/users
				}
				return tw.Flush()
			},
		},
	}
}

// Router returns the server's router.
func (s *Server) Router() *Router { return s.router }

// Config returns the server configuration.
func (s *Server) Config() Config { return s.cfg }

// Name implements supervisor.Component.
func (s *Server) Name() string { return "http" }

// Ready implements supervisor.Readier: true once the server is listening
// and until shutdown begins.
func (s *Server) Ready() bool { return s.ready.Load() }

// Stopping returns a channel that is closed when the server begins shutting
// down. Long-lived handlers (server-sent events, long polling) should select
// on it and end the stream, so shutdown doesn't wait for HTTP_SHUTDOWN_GRACE:
//
//	select {
//	case <-c.Done():
//	case <-srv.Stopping():
//	case msg := <-updates:
//		// write msg
//	}
func (s *Server) Stopping() <-chan struct{} { return s.stopping }

// Addr returns the address the server is listening on (useful with
// HTTP_ADDR=":0" in tests), or "" if it isn't listening yet.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Run implements supervisor.Component. It listens on the configured
// address and serves until ctx is canceled. Then it stops accepting
// connections and waits for in-flight requests, up to HTTP_SHUTDOWN_GRACE or
// half of APP_SHUTDOWN_TIMEOUT, whichever is shorter; requests still running
// after that have their contexts canceled and their connections closed.
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.mu.Unlock()

	// Request contexts must not be canceled as soon as shutdown starts (the
	// grace period lets them finish), but must be canceled when it ends.
	baseCtx, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBase()

	srv := &http.Server{
		Handler:           s.router,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		ReadTimeout:       s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	s.ready.Store(true)
	s.log.Info("http server listening", "addr", s.Addr())

	select {
	case err := <-serveErr:
		s.ready.Store(false)
		return err
	case <-ctx.Done():
	}

	s.ready.Store(false)
	s.stoppingOnce.Do(func() { close(s.stopping) })
	// Leave at least half of the app's shutdown budget to later stages
	// (listeners, workers) and hooks.
	grace := min(s.cfg.ShutdownGrace, s.app.Config().ShutdownTimeout/2)
	s.log.Info("http server shutting down", "grace", grace)

	graceCtx, cancelGrace := context.WithTimeout(context.Background(), grace)
	err = srv.Shutdown(graceCtx)
	cancelGrace()
	if errors.Is(err, context.DeadlineExceeded) {
		s.log.Warn("http requests still running after the grace period; canceling them")
		cancelBase()
		err = srv.Close()
	}
	if serr := <-serveErr; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return serr
	}
	return err
}

// units makes each request a unit of work of the app (anetos.App.StartUnit),
// such as db.Connect's repeated-query detection.
func units(app *anetos.App) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !app.HasAroundUnits() {
				next.ServeHTTP(w, r)
				return
			}
			ctx, end := app.StartUnit(r.Context(), anetos.Unit{Kind: "request", Name: r.Method + " " + r.URL.Path})
			defer end()
			if ctx != r.Context() {
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// localAddr is the address to reach a server listening on addr from the
// same host: the loopback address for ":8080", "0.0.0.0:8080" or
// "[::]:8080".
func localAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// checkHealth asks url, and fails unless it answers 200.
func checkHealth(ctx context.Context, url string, timeout time.Duration, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req) // no proxy: the server is local
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", url, res.Status)
	}
	fmt.Fprintln(out, "ok")
	return nil
}
