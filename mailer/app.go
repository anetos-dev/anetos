// SPDX-License-Identifier: Apache-2.0

package mailer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"slices"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/queue"
)

// Config selects and configures the app's mail transport.
type Config struct {
	// Driver is how emails are sent: log (written to the app's log),
	// smtp, memory (kept, for tests), or one passed to ForApp
	// (postmark). MAIL_DRIVER, default log.
	Driver string `env:"MAIL_DRIVER" default:"log"`
	// FromAddress is the sender of messages without one.
	// MAIL_FROM_ADDRESS.
	FromAddress string `env:"MAIL_FROM_ADDRESS"`
	// FromName is the sender's name. MAIL_FROM_NAME, default APP_NAME.
	FromName string `env:"MAIL_FROM_NAME"`
	// SMTPURL is the SMTP server: smtp://user:password@host:587 (with
	// STARTTLS) or smtps://…:465 (TLS); a Secret, since it holds the
	// password. MAIL_SMTP_URL, default smtp://127.0.0.1:1025 (Mailpit,
	// for development).
	SMTPURL anetos.Secret `env:"MAIL_SMTP_URL" default:"smtp://127.0.0.1:1025"`
}

// LoadConfig reads the MAIL_* settings.
func LoadConfig(src config.Source) (Config, error) {
	cfg, err := config.Get[Config](src)
	if err != nil {
		return cfg, err
	}
	var errs []error
	if cfg.FromAddress != "" {
		if a, err := mail.ParseAddress(cfg.FromAddress); err != nil || a.Address != cfg.FromAddress || a.Name != "" {
			errs = append(errs, fmt.Errorf("MAIL_FROM_ADDRESS %q must be an email address (hello@example.com)", cfg.FromAddress))
		}
	}
	if strings.ContainsAny(cfg.FromName, "\r\n") {
		errs = append(errs, errors.New("MAIL_FROM_NAME has a line break"))
	}
	return cfg, errors.Join(errs...)
}

// Driver opens a transport for [ForApp]. The log, smtp and memory
// drivers are built in; driver modules provide others
// (postmark.Driver()).
type Driver struct {
	// Name is the value of MAIL_DRIVER that selects the driver.
	Name string
	// Open returns the transport for the app. If it implements
	// io.Closer, it is closed when the app shuts down.
	Open func(app *anetos.App, cfg Config) (Transport, error)
}

// LogDriver writes emails to the app's log instead of sending them
// (MAIL_DRIVER=log, the default): for development.
func LogDriver() Driver {
	return Driver{Name: "log", Open: func(app *anetos.App, _ Config) (Transport, error) {
		return NewLogTransport(app.Logger().With("component", "mailer")), nil
	}}
}

// MemoryDriver keeps emails in memory (MAIL_DRIVER=memory): for tests,
// which check them with the mailer's *[MemoryTransport].
func MemoryDriver() Driver {
	return Driver{Name: "memory", Open: func(*anetos.App, Config) (Transport, error) { return NewMemoryTransport(), nil }}
}

// SMTPDriver sends emails to the SMTP server of MAIL_SMTP_URL
// (MAIL_DRIVER=smtp).
func SMTPDriver() Driver {
	return Driver{Name: "smtp", Open: func(_ *anetos.App, cfg Config) (Transport, error) {
		t, err := NewSMTPTransport(string(cfg.SMTPURL))
		if err != nil {
			return nil, fmt.Errorf("MAIL_SMTP_URL: %w", err)
		}
		return t, nil
	}}
}

// ForApp sets up the app's mailer from the MAIL_* settings: it opens the
// transport MAIL_DRIVER names (log, smtp and memory are built in; pass
// others, such as postmark.Driver()) and makes the mailer available in
// every context the app creates, for [Send] and [Queue]. If the app has
// a queue (queue.ForApp, before or after), it registers the job that
// sends queued emails.
//
//	m, err := mailer.ForApp(app, postmark.Driver())
func ForApp(app *anetos.App, drivers ...Driver) (*Mailer, error) {
	if _, err := anetos.Resolve[*Mailer](app); err == nil {
		return nil, errors.New("mailer: ForApp called twice for one app")
	}
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	all := append([]Driver{LogDriver(), SMTPDriver(), MemoryDriver()}, drivers...)
	i := slices.IndexFunc(all, func(d Driver) bool { return d.Name == cfg.Driver })
	if i < 0 {
		names := make([]string, len(all))
		for j, d := range all {
			names[j] = d.Name
		}
		return nil, fmt.Errorf("mailer: MAIL_DRIVER is %q, but the drivers are [%s]; pass its driver to mailer.ForApp (postmark.Driver() from anetos.dev/anetos/plugins/postmark)", cfg.Driver, strings.Join(names, ", "))
	}
	t, err := all[i].Open(app, cfg)
	if err != nil {
		return nil, fmt.Errorf("mailer: open the %s transport: %w", cfg.Driver, err)
	}
	name := cfg.FromName
	if name == "" {
		name = app.Config().Name
	}
	log := app.Logger().With("component", "mailer")
	m := New(t, DefaultFrom(Address{Name: name, Address: cfg.FromAddress}), BaseURL(app.Config().URL), WithLogger(log))
	if q, err := anetos.Resolve[*queue.Queue](app); err == nil {
		if err := m.register(q); err != nil {
			return nil, err
		}
	} else if !app.Booted() {
		app.Use(registrar{m}) // queue.ForApp may come later
	}
	switch {
	case cfg.Driver == "log" && app.Config().Env.IsProduction():
		log.Warn("mailer: MAIL_DRIVER is log in production: emails are written to the log, not sent")
	case cfg.FromAddress == "" && cfg.Driver != "log" && cfg.Driver != "memory":
		log.Warn("mailer: MAIL_FROM_ADDRESS isn't set: messages need a From of their own")
	}
	if c, ok := t.(io.Closer); ok {
		app.OnShutdown("mailer", func(_ context.Context) error { return c.Close() })
	}
	app.AddContextValue(mailerKey{}, m)
	anetos.Provide(app, m)
	return m, nil
}

// registrar registers the job of queued emails when the app boots, if
// queue.ForApp came after mailer.ForApp.
type registrar struct{ m *Mailer }

func (registrar) Name() string { return "mailer" }

func (r registrar) Register(app *anetos.App) error {
	if q, err := anetos.Resolve[*queue.Queue](app); err == nil && !r.m.queued.Load() {
		return r.m.register(q)
	}
	return nil
}

func (registrar) Boot(context.Context, *anetos.App) error { return nil }
