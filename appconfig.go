// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
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

	// Debug enables diagnostics that must never reach production users.
	// APP_DEBUG, default false. Not allowed together with APP_ENV=production.
	Debug bool `env:"APP_DEBUG"`

	// ShutdownTimeout is the total budget for graceful shutdown: components
	// first, then shutdown hooks. Hooks always get at least the smaller of 5s
	// and a fifth of the budget, which components may not use.
	// APP_SHUTDOWN_TIMEOUT, default 30s (Kubernetes' default grace period).
	// Set it at or below your platform's grace period.
	ShutdownTimeout time.Duration `env:"APP_SHUTDOWN_TIMEOUT" default:"30s"`

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
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("APP_SHUTDOWN_TIMEOUT must be positive"))
	}
	switch c.Log.Format {
	case "", "text", "json":
	default:
		errs = append(errs, fmt.Errorf("LOG_FORMAT %q is not one of text, json", c.Log.Format))
	}
	return errors.Join(errs...)
}
