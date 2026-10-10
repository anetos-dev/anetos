// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/view/htmx"
	"anetos.dev/anetos/web"
)

// Access is the permission to use the admin at all. Each resource has
// four more: admin.<name>.view, .create, .update and .delete.
const Access rbac.Permission = "admin.access"

// Config is the admin's settings.
type Config struct {
	// Path is where the admin is mounted: ADMIN_PATH, default /admin.
	// With a Host, the admin is at that host's root unless Path is set.
	Path string `env:"ADMIN_PATH"`
	// Host is the host the admin answers on alone (admin.example.com),
	// "" for every host: ADMIN_HOST.
	Host string `env:"ADMIN_HOST"`
	// Title is the admin's name in its pages: ADMIN_TITLE, default
	// APP_NAME.
	Title string `env:"ADMIN_TITLE"`
	// PerPage is how many records a list shows: ADMIN_PER_PAGE, default
	// 25.
	PerPage int `env:"ADMIN_PER_PAGE" default:"25"`
	// Confirm asks users for their password again (auth's
	// ConfirmPassword, valid for AUTH_CONFIRM_TTL) before dangerous
	// actions: deleting, disabling, roles and permissions, acting as a
	// user, forgetting every failed job, and actions marked Danger.
	// ADMIN_CONFIRM, default true.
	Confirm bool `env:"ADMIN_CONFIRM" default:"true"`
	// TwoFactor is "required" to let in only users with two-factor
	// sign-in on (the others are told to turn it on, at
	// AUTH_TWO_FACTOR_URL), or "optional". ADMIN_TWO_FACTOR, default
	// optional.
	TwoFactor string `env:"ADMIN_TWO_FACTOR" default:"optional"`
	// AllowIPs are the only addresses, or networks (10.0.0.0/8), the
	// admin answers; others get 404. Empty for every address. The
	// client's address is found behind trusted proxies only
	// (HTTP_TRUSTED_PROXIES).
	// ADMIN_ALLOW_IPS, comma-separated.
	AllowIPs []string `env:"ADMIN_ALLOW_IPS"`
}

// Validate checks the settings.
func (c Config) Validate() error {
	var errs []error
	if c.Path != "" && (!strings.HasPrefix(c.Path, "/") || strings.ContainsAny(c.Path, " {}")) {
		errs = append(errs, fmt.Errorf("admin: ADMIN_PATH %q must start with / and have no spaces or wildcards", c.Path))
	}
	if c.Host == "" && strings.Trim(c.Path, "/") == "" && c.Path != "" {
		errs = append(errs, errors.New("admin: ADMIN_PATH / takes the whole site: set ADMIN_HOST to give the admin a host of its own, or use a path like /admin"))
	}
	if strings.ContainsAny(c.Host, "/ ") {
		errs = append(errs, fmt.Errorf("admin: ADMIN_HOST %q must be a host name (admin.example.com)", c.Host))
	}
	if c.PerPage < 1 || c.PerPage > 500 {
		errs = append(errs, errors.New("admin: ADMIN_PER_PAGE must be between 1 and 500"))
	}
	if c.TwoFactor != "" && c.TwoFactor != "optional" && c.TwoFactor != "required" {
		errs = append(errs, fmt.Errorf("admin: ADMIN_TWO_FACTOR %q must be optional or required", c.TwoFactor))
	}
	if _, err := parseAllowed(c.AllowIPs); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Panel is an admin interface: its settings, resources and pages. Create
// it with [New], add resources with [Add], and [Panel.Mount] it on the
// app's router.
type Panel struct {
	app      *anetos.App
	cfg      Config
	require  web.Middleware // auth's Require for the app's user type
	reg      *rbac.Registry
	pages    map[string]*template.Template
	assets   *view.Assets
	res      []resource
	byName   map[string]resource
	mounted  bool
	base     string // the admin's path prefix
	userName func(ctx context.Context) string
	stop     web.HandlerFunc // stops acting as a user
	// usersName is the users resource's name (Users), "" without.
	usersName string
	// widgets are the dashboard's.
	widgets []Widget
	// activity says the activity pages are added.
	activity bool
	// userLabels names users by ID, with the users resource.
	userLabels func(ctx context.Context, ids []string) (map[string]string, error)
	// sec is what the admin needs of auth to protect itself.
	sec security
	// allowed are ADMIN_ALLOW_IPS's networks.
	allowed []netip.Prefix
	// appRoutes is the app's router, to find its settings page.
	appRoutes    *web.Router
	settingsOnce sync.Once
	settings     bool
}

// hasSettings reports whether the app has its account settings page
// (AUTH_SETTINGS_URL, make:auth's), which the user's name links to.
func (p *Panel) hasSettings() bool {
	p.settingsOnce.Do(func() {
		if p.appRoutes == nil || p.sec.settingsURL == "" {
			return
		}
		for _, rt := range p.appRoutes.Routes() {
			if rt.Host == "" && rt.Pattern == p.sec.settingsURL && (rt.Method == http.MethodGet || rt.Method == "") {
				p.settings = true
				return
			}
		}
	})
	return p.settings
}

// Option changes a [Panel].
type Option func(*Panel)

// Title sets the admin's name, shown in its pages; ADMIN_TITLE overrides
// it.
func Title(title string) Option {
	return func(p *Panel) {
		if p.cfg.Title == "" {
			p.cfg.Title = title
		}
	}
}

// UserName sets how the admin names the signed-in user in its pages;
// by default, their AdminName or String method, else "User <id>".
func UserName[U auth.Authenticatable](name func(u U) string) Option {
	return func(p *Panel) {
		p.userName = func(ctx context.Context) string {
			if u, ok := auth.User[U](ctx); ok {
				return name(u)
			}
			return ""
		}
	}
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,49}$`)

// reservedNames are the admin's own pages, which no resource can be.
var reservedNames = []string{strings.Trim(confirmPath, "/"), strings.Trim(twoFactorPath, "/"), "impersonation"}

// New creates the app's admin, for the users of a (the app's auth.Auth,
// from auth.New): it reads the ADMIN_* settings and declares
// [Access] in the app's roles and permissions (rbac.New must have run).
// Only signed-in users with that permission get in; give it with a role
// (a super role, or one in the database), for example
// `rbac:assign <user-id> admin`.
func New[U auth.Authenticatable](app *anetos.App, a *auth.Auth[U], opts ...Option) (*Panel, error) {
	if _, ok := anetos.Lookup[*Panel](app); ok {
		return nil, errors.New("admin: New called twice for one app")
	}
	cfg, err := config.Get[Config](app.Source())
	if err != nil {
		return nil, err
	}
	reg, ok := anetos.Lookup[*rbac.Registry](app)
	if !ok {
		return nil, errors.New("admin: New needs the app's roles and permissions: call rbac.New first")
	}
	if err := reg.Declare(Access); err != nil {
		return nil, err
	}
	p := &Panel{app: app, cfg: cfg, require: a.Require, reg: reg, byName: map[string]resource{}, sec: securityOf(a)}
	if p.allowed, err = parseAllowed(cfg.AllowIPs); err != nil {
		return nil, err
	}
	if cfg.TwoFactor == "required" && p.sec.twoFactorOn == nil {
		return nil, errors.New("admin: ADMIN_TWO_FACTOR=required needs two-factor sign-in: auth.Users.TwoFactor and SetTwoFactor")
	}
	p.userName = func(ctx context.Context) string {
		u, ok := auth.User[U](ctx)
		if !ok {
			return ""
		}
		if n, ok := any(u).(interface{ AdminName() string }); ok {
			return n.AdminName()
		}
		if n, ok := any(u).(fmt.Stringer); ok {
			return n.String()
		}
		return "User " + u.AuthID()
	}
	p.stop = stopImpersonating(a, p)
	for _, opt := range opts {
		opt(p)
	}
	if p.cfg.Title == "" {
		p.cfg.Title = app.Config().Name
	}
	if p.cfg.Title == "" {
		p.cfg.Title = "Admin"
	}
	if p.pages, err = parsePages(); err != nil {
		return nil, err
	}
	anetos.Provide(app, p)
	return p, nil
}

// Config returns the admin's settings.
func (p *Panel) Config() Config { return p.cfg }

// Mount adds the admin's routes to r, under ADMIN_PATH (or at ADMIN_HOST),
// after mws: the app's session, CSRF and auth middleware, as its own
// pages have them:
//
//	err := panel.Mount(r, sessions.Middleware, web.CSRF(), a.Middleware)
//
// Guests are sent to sign in (AUTH_LOGIN_URL); signed-in users without
// [Access] get 403. Routes are named admin.*. Mount it once, after
// adding the resources.
func (p *Panel) Mount(r *web.Router, mws ...web.Middleware) error {
	if p.mounted {
		return errors.New("admin: Mount called twice")
	}
	p.mounted = true
	p.appRoutes = r
	path := p.cfg.Path
	if path == "" && p.cfg.Host == "" {
		path = "/admin"
	}
	if p.cfg.Host != "" {
		r = r.Host(p.cfg.Host)
	}
	p.base = strings.TrimSuffix(path, "/")
	// Under _assets, which no resource name can be, nor (at a host of
	// its own) the app's own /assets.
	assets, err := view.NewAssets(p.base+"/_assets", staticFS, htmx.FS)
	if err != nil {
		return err
	}
	p.assets = assets
	// Assets need no session: they are the same for everyone.
	r.Group(p.base, p.allowIPs).HandleStd(http.MethodGet, "/_assets/{file...}", assets).Name("admin.assets")
	// Stopping acting as a user needs no admin permission: the user
	// acted as may have none.
	r.Group(p.base, append([]web.Middleware{p.allowIPs}, append(slices.Clip(mws), secureHeaders)...)...).
		Post(stopPath, p.stop).Name("admin.impersonation.stop")
	signedIn := r.Group(p.base, append([]web.Middleware{p.allowIPs}, append(slices.Clip(mws), secureHeaders, p.require, rbac.Require(Access))...)...)
	if p.cfg.TwoFactor == "required" {
		signedIn.Get(twoFactorPath, p.twoFactorRequired).Name("admin.two-factor")
	}
	g := signedIn.Group("", p.requireTwoFactor)
	g.Get("/", p.home).Name("admin.home")
	g.Get(confirmPath, p.confirmPage).Name("admin.confirm")
	g.Post(confirmPath, p.confirmPassword).Name("admin.confirm.store")
	for _, res := range p.res {
		res.mount(g.Group("/" + res.info().Name))
	}
	return nil
}

// URL returns the path of the admin's first page: "/admin", or "/" at a
// host of its own. It is known once the panel is mounted ("/" before).
func (p *Panel) URL() string {
	if p.base == "" {
		return "/"
	}
	return p.base
}

// secureHeaders keeps the admin's pages out of frames and caches, and
// limits what they may load to the admin's own files.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// add registers a resource and declares its permissions.
func (p *Panel) add(res resource) error {
	if p.mounted {
		return errors.New("admin: Add after Mount: add the resources first")
	}
	in := res.info()
	if !nameRe.MatchString(in.Name) {
		return fmt.Errorf("admin: invalid resource name %q (lowercase letters, digits and -, starting with a letter)", in.Name)
	}
	if p.byName[in.Name] != nil {
		return fmt.Errorf("admin: resource %q added twice", in.Name)
	}
	if slices.Contains(reservedNames, in.Name) {
		return fmt.Errorf("admin: the resource name %q is the admin's own page", in.Name)
	}
	if err := p.reg.Declare(in.perms()...); err != nil {
		return err
	}
	p.res = append(p.res, res)
	p.byName[in.Name] = res
	return nil
}

// PermissionsOf returns the permissions of resource name: admin.<name>.
// followed by each of kinds ("view", "create", "update", "delete"), or by
// all four without kinds. For roles declared in code, before the panel
// exists:
//
//	editor := slices.Concat([]rbac.Permission{admin.Access}, admin.PermissionsOf("posts"))
func PermissionsOf(name string, kinds ...string) []rbac.Permission {
	if len(kinds) == 0 {
		kinds = []string{"view", "create", "update", "delete"}
	}
	in := resInfo{Name: name}
	out := make([]rbac.Permission, len(kinds))
	for i, k := range kinds {
		out[i] = in.perm(k)
	}
	return out
}

// Permissions returns the admin's permissions: [Access] and those of the
// resources added so far, for building roles.
func (p *Panel) Permissions() []rbac.Permission {
	out := []rbac.Permission{Access}
	for _, r := range p.res {
		out = append(out, r.info().perms()...)
	}
	return out
}

// home is the admin's first page: the resources the user may see.
func (p *Panel) home(c *web.Ctx) error {
	type card struct {
		Title string
		URL   string
		Count string
	}
	data := struct {
		Widgets []widgetView
		Cards   []card
	}{Widgets: p.loadWidgets(c)}
	for _, r := range p.res {
		in := r.info()
		if !rbac.Can(c, in.perm("view")) {
			continue
		}
		n, err := r.count(c)
		if err != nil {
			// The card shows without its count; its page shows the error.
			p.app.Logger().ErrorContext(c, "admin: counting a resource", "resource", in.Name, "error", err)
			n = -1
		}
		cd := card{Title: in.Title, URL: p.base + "/" + in.Name}
		if n >= 0 {
			cd.Count = thousands(n)
		}
		data.Cards = append(data.Cards, cd)
	}
	return p.render(c, "home", page{Title: p.cfg.Title, Data: data})
}

// navItem is a link in the admin's navigation.
type navItem struct {
	Title  string
	URL    string
	Active bool
}

// page is what every admin page gets.
type page struct {
	Title   string
	Panel   string // the admin's title
	Home    string // the admin's first page
	User    string
	Account string // the user's account settings, in the app
	Nav     []navItem
	Crumbs  []navItem
	Flash   string
	Error   string
	CSRF    string
	CSS     string
	JS      string
	AdminJS string
	URL     string // the page's path and query
	Banner  template.HTML
	Data    any
	status  int // 200 if 0
}

// render renders the named page in the admin's layout.
func (p *Panel) render(c *web.Ctx, name string, pg page) error {
	t := p.pages[name]
	if t == nil {
		return fmt.Errorf("admin: no page %q", name)
	}
	pg.Panel, pg.Home, pg.User = p.cfg.Title, p.URL(), p.userName(c)
	if pg.User != "" && p.hasSettings() {
		pg.Account = p.appURL(c, p.sec.settingsURL)
	}
	pg.CSRF = view.CSRFToken(c)
	pg.CSS, pg.JS, pg.AdminJS = p.assets.URL("admin.css"), p.assets.URL("htmx.min.js"), p.assets.URL("admin.js")
	pg.URL = c.Request().URL.RequestURI()
	banner, err := bannerHTML(c)
	if err != nil {
		return err
	}
	pg.Banner = banner
	pg.Flash = view.Flash(c, "admin.status")
	if pg.Error == "" {
		pg.Error = view.Flash(c, "admin.error")
	}
	for _, r := range p.res {
		in := r.info()
		if rbac.Can(c, in.perm("view")) {
			pg.Nav = append(pg.Nav, navItem{in.Title, p.base + "/" + in.Name, strings.HasPrefix(c.Request().URL.Path, p.base+"/"+in.Name)})
		}
	}
	status := http.StatusOK
	if pg.status != 0 {
		status = pg.status
	}
	return c.Render(status, view.Template(t, "layout", pg))
}

// flash keeps a message for the next page.
func flash(c *web.Ctx, key, msg string) {
	if s := c.Session(); s != nil {
		s.Flash(key, msg)
	}
}
