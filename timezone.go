// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
	_ "time/tzdata" // zones load in containers without a zone database (D188)
)

// The process's local zone is the app's (APP_TIMEZONE, default UTC), so
// time.Now, logs and formatting agree on every machine; TZ is ignored.
// It is set when the package initializes, from APP_TIMEZONE in the
// environment, before any goroutine can read time.Local. The first app
// New creates sets it again if its configuration (a .env file, say)
// names another zone, except in a test binary, where tests create apps
// while others run.
var process struct {
	mu   sync.Mutex
	name string         // the zone time.Local was set to
	loc  *time.Location // time.Local's value then
	set  bool           // an app was created: later ones don't change it
}

func init() {
	name := os.Getenv("APP_TIMEZONE")
	loc, err := loadZone(name)
	if err != nil {
		return // New reports the invalid setting
	}
	time.Local = loc
	process.name, process.loc = zoneName(name), loc
}

// zoneName is the name of a zone setting: empty means UTC.
func zoneName(name string) string {
	if name == "" {
		return "UTC"
	}
	return name
}

// loadZone loads the zone named by an APP_TIMEZONE setting. The name
// must be an IANA name or UTC: "Local" would depend on the machine.
func loadZone(name string) (*time.Location, error) {
	name = zoneName(name)
	if name == "Local" {
		return nil, os.ErrInvalid
	}
	return time.LoadLocation(name)
}

// appZone returns the location of the app's zone named name, setting
// the process's local zone to it if no app has set it yet. A process has
// one local zone: later apps with another zone keep theirs for [Now] and
// [Location] only. In a test binary, where apps start while other tests
// run, only init sets it (from APP_TIMEZONE in the environment), so no
// test writes time.Local while another reads it.
func appZone(name string) (*time.Location, error) {
	name = zoneName(name)
	process.mu.Lock()
	defer process.mu.Unlock()
	first := !process.set
	process.set = true
	if name == process.name {
		return process.loc, nil
	}
	loc, err := loadZone(name)
	if err != nil {
		return nil, err
	}
	if first && !testing.Testing() {
		time.Local = loc
		process.name, process.loc = name, loc
	}
	return loc, nil
}

// Location returns the app's time zone (APP_TIMEZONE, default UTC).
func (a *App) Location() *time.Location { return a.clock.loc }

// Location returns the time zone of the app in ctx ([App.Location]), or
// time.Local when ctx has no app.
func Location(ctx context.Context) *time.Location {
	if c, ok := ctx.Value(clockKey{}).(*clock); ok && c.loc != nil {
		return c.loc
	}
	return time.Local
}
