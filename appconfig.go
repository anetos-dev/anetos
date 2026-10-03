// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"anetos.dev/anetos/internal/appkey"
)

// Environment names the deployment environment (APP_ENV).
type Environment string

// Supported environments.
const (
	Development Environment = "development"
	Testing     Environment = "testing"
	Staging     Environment = "staging"
	Production  Environment = "production"
)

// IsProduction reports whether e is [Production].
func (e Environment) IsProduction() bool { return e == Production }

// IsDevelopment reports whether e is [Development].
func (e Environment) IsDevelopment() bool { return e == Development }

// IsTesting reports whether e is [Testing].
func (e Environment) IsTesting() bool { return e == Testing }

func (e Environment) valid() bool {
	switch e {
	case Development, Testing, Staging, Production:
		return true
	}
	return false
}

// AppConfig is the framework's own configuration, read from APP_* and LOG_*
// variables. See docs/site/reference/configuration.md for every key.
//
// The zero value is not valid; use [DefaultAppConfig] or let [New] load it.
type AppConfig struct {
	// Name identifies the application in logs. APP_NAME, default "anetos".
	Name string `env:"APP_NAME" default:"anetos"`

	// Env is the deployment environment. APP_ENV, default "production", so a
	// missing setting fails safe.
	Env Environment `env:"APP_ENV" default:"production"`

	// URL is the app's public base URL ("https://example.com"), for links
	// that leave the app: OAuth callbacks, links in emails (mailer.URL).
	// APP_URL; empty until set, and the features that need it say so.
	URL string `env:"APP_URL"`

	// Debug enables diagnostics that must never reach production users.
	// APP_DEBUG, default false. Not allowed together with APP_ENV=production.
	Debug bool `env:"APP_DEBUG"`

	// ShutdownTimeout is the total budget for graceful shutdown: components
	// first, then shutdown hooks. Hooks always get at least the smaller of 5s
	// and a fifth of the budget, which components may not use.
	// APP_SHUTDOWN_TIMEOUT, default 30s (Kubernetes' default grace period).
	// Set it at or below your platform's grace period.
	ShutdownTimeout time.Duration `env:"APP_SHUTDOWN_TIMEOUT" default:"30s"`

	// Key encrypts and authenticates session cookies and other data: 32
	// random bytes written as "base64:…". APP_KEY. Required by the features
	// that use it (sessions), which fail at startup without it. Keep it
	// secret; changing it logs everyone out unless the old key is kept in
	// APP_PREVIOUS_KEYS.
	Key Secret `env:"APP_KEY"`

	// PreviousKeys still decrypt data written with them, so keys can be
	// rotated without logging everyone out. APP_PREVIOUS_KEYS,
	// comma-separated.
	PreviousKeys []Secret `env:"APP_PREVIOUS_KEYS"`

	// TimeZone is the app's time zone, an IANA name ("Asia/Dhaka"):
	// the process's local zone, the zone of [Now] and the default of
	// SCHEDULE_TIMEZONE. Times are stored in UTC whatever it is.
	// APP_TIMEZONE, default UTC; empty means UTC.
	TimeZone string `env:"APP_TIMEZONE" default:"UTC"`

	// Log configures the default logger. LOG_* variables.
	Log LogConfig `prefix:"LOG_"`
}

// LogConfig configures the default structured logger.
type LogConfig struct {
	// Level is the minimum level: debug, info, warn or error.
	// LOG_LEVEL, default info.
	Level slog.Level `env:"LEVEL" default:"info"`

	// Format is "text", "json", or empty for automatic: JSON in production,
	// text elsewhere. LOG_FORMAT.
	Format string `env:"FORMAT"`
}

// DefaultAppConfig returns the configuration used when no variables are set.
func DefaultAppConfig() AppConfig {
	return AppConfig{
		Name:            "anetos",
		Env:             Production,
		ShutdownTimeout: 30 * time.Second,
		TimeZone:        "UTC",
		Log:             LogConfig{Level: slog.LevelInfo},
	}
}

// Validate implements config.Validator.
func (c AppConfig) Validate() error {
	var errs []error
	if !c.Env.valid() {
		errs = append(errs, fmt.Errorf("APP_ENV %q is not one of development, testing, staging, production", c.Env))
	}
	if c.Env.IsProduction() && c.Debug {
		errs = append(errs, errors.New("APP_DEBUG=true is not allowed when APP_ENV=production"))
	}
	if c.URL != "" {
		if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			errs = append(errs, fmt.Errorf("APP_URL %q must be an http or https URL without a path (https://example.com)", c.URL))
		}
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("APP_SHUTDOWN_TIMEOUT must be positive"))
	}
	if c.Key != "" {
		if _, err := appkey.Parse(string(c.Key)); err != nil {
			errs = append(errs, fmt.Errorf("APP_KEY: %w", err))
		}
	}
	for i, k := range c.PreviousKeys {
		if _, err := appkey.Parse(string(k)); err != nil {
			errs = append(errs, fmt.Errorf("APP_PREVIOUS_KEYS[%d]: %w", i, err))
		}
	}
	if _, err := loadZone(c.TimeZone); err != nil {
		errs = append(errs, fmt.Errorf("APP_TIMEZONE %q is not an IANA time zone (UTC, Asia/Dhaka, Europe/Paris)", c.TimeZone))
	}
	switch c.Log.Format {
	case "", "text", "json":
	default:
		errs = append(errs, fmt.Errorf("LOG_FORMAT %q is not one of text, json", c.Log.Format))
	}
	return errors.Join(errs...)
}

// Secret is a configuration value that must never be printed or logged,
// such as APP_KEY. Its String, GoString, MarshalJSON and LogValue methods
// all hide it, so fmt, encoding/json and log/slog show "[redacted]".
// Convert it to a string to use it: string(cfg.Key).
type Secret string

// String returns "[redacted]", or "" for an empty secret.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "[redacted]"
}

// GoString hides the secret from %#v.
func (s Secret) GoString() string { return fmt.Sprintf("anetos.Secret(%q)", s.String()) }

// MarshalJSON hides the secret from encoding/json.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// LogValue hides the secret from log/slog.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }
