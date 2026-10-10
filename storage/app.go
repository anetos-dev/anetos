// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
)

// Config configures a disk. The default disk reads STORAGE_*; a disk
// named in STORAGE_DISKS, say "avatars", reads STORAGE_AVATARS_*, with
// the driver (and, if it sets none of them, the settings its driver
// lists in [Driver].Inherit) falling back to the default disk's.
type Config struct {
	// Driver is where files are kept: local (a directory), memory, or
	// one passed to New (s3). STORAGE_DRIVER, default local.
	Driver string `env:"STORAGE_DRIVER" default:"local"`
	// Root is the local driver's directory. STORAGE_ROOT, default
	// storage/app (storage/<name> for a named disk).
	Root string `env:"STORAGE_ROOT"`
	// URL is the base URL the disk's files are served at: a CDN, a
	// public bucket, or the route of the disk's Handler. STORAGE_URL.
	URL string `env:"STORAGE_URL"`
	// Public says the files are readable by anyone at URL, so Disk.URL
	// works; otherwise only signed temporary URLs are. STORAGE_PUBLIC,
	// default false.
	Public bool `env:"STORAGE_PUBLIC" default:"false"`
}

// AppConfig is the app's storage settings.
type AppConfig struct {
	// Disks names the disks besides the default one: lower-case letters,
	// digits and _. STORAGE_DISKS ("avatars,exports").
	Disks []string `env:"STORAGE_DISKS"`
}

var diskName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// LoadConfig reads the settings of the disk name ("" for the default
// disk) from src.
func LoadConfig(src config.Source, name string) (Config, error) {
	cfg, err := config.Get[Config](DiskSource(src, name, nil))
	if err != nil {
		return cfg, err
	}
	if cfg.Root == "" {
		cfg.Root = "storage/app"
		if name != "" {
			cfg.Root = "storage/" + name
		}
	}
	if cfg.URL != "" {
		u, err := url.Parse(cfg.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return cfg, fmt.Errorf("%s %q must be an http or https URL without a query", key(name, "URL"), cfg.URL)
		}
	}
	return cfg, nil
}

// key returns the setting named suffix of the disk name: STORAGE_URL,
// STORAGE_AVATARS_URL.
func key(name, suffix string) string {
	if name == "" {
		return "STORAGE_" + suffix
	}
	return "STORAGE_" + strings.ToUpper(name) + "_" + suffix
}

// DiskSource returns the settings of the disk name ("" for the default
// disk): for a named disk, src's STORAGE_<NAME>_* settings read as
// STORAGE_*. STORAGE_DRIVER falls back to the default disk's, and so do
// the settings in inherit (suffixes such as "S3_REGION"), as a group:
// only if the named disk sets none of them, so a disk with an endpoint
// of its own doesn't get another's keys. Drivers read their settings
// from it.
func DiskSource(src config.Source, name string, inherit []string) config.Source {
	if name == "" {
		return src
	}
	ds := diskSource{src: src, name: name}
	ds.inherit = !slices.ContainsFunc(inherit, func(suffix string) bool {
		_, set := src.Lookup(key(name, suffix))
		return set
	})
	ds.group = inherit
	return ds
}

type diskSource struct {
	src     config.Source
	name    string
	group   []string
	inherit bool // the group is inherited
}

func (s diskSource) Lookup(k string) (string, bool) {
	suffix, ok := strings.CutPrefix(k, "STORAGE_")
	if !ok {
		return s.src.Lookup(k)
	}
	if v, ok := s.src.Lookup(key(s.name, suffix)); ok {
		return v, true
	}
	if suffix == "DRIVER" || s.inherit && slices.Contains(s.group, suffix) {
		return s.src.Lookup(k)
	}
	return "", false
}

// Driver opens a backend for [New]. The local and memory drivers are
// built in; driver modules provide others (s3.Driver()).
type Driver struct {
	// Name is the value of STORAGE_DRIVER that selects the driver.
	Name string
	// Inherit lists the settings (suffixes after STORAGE_, such as
	// "S3_REGION") a named disk takes from the default disk when it sets
	// none of them: a shared connection and credentials, not locations.
	Inherit []string
	// Open returns the backend of the disk name ("" for the default
	// disk), whose settings src has (as STORAGE_*). If the backend
	// implements io.Closer, it is closed when the app shuts down.
	Open func(app *anetos.App, name string, src config.Source, cfg Config) (Backend, error)
}

// LocalDriver keeps files in the directory STORAGE_ROOT
// (STORAGE_DRIVER=local, the default).
func LocalDriver() Driver {
	return Driver{Name: "local", Open: func(_ *anetos.App, _ string, _ config.Source, cfg Config) (Backend, error) {
		return NewLocalBackend(cfg.Root)
	}}
}

// MemoryDriver keeps files in memory (STORAGE_DRIVER=memory): for tests
// (anetostest sets it) and development.
func MemoryDriver() Driver {
	return Driver{Name: "memory", Open: func(*anetos.App, string, config.Source, Config) (Backend, error) {
		return NewMemoryBackend(), nil
	}}
}

// Storage is the app's disks: the default one, and those STORAGE_DISKS
// names.
type Storage struct {
	def   *Disk
	disks map[string]*Disk
}

// NewWithDisks returns storage with def as the default disk and the others as
// named disks (by their names).
func NewWithDisks(def *Disk, others ...*Disk) *Storage {
	s := &Storage{def: def, disks: map[string]*Disk{}}
	for _, d := range others {
		s.disks[d.name] = d
	}
	return s
}

// Default returns the default disk.
func (s *Storage) Default() *Disk { return s.def }

// Disk returns the disk name, or the default disk for "" (or
// "default").
func (s *Storage) Disk(name string) (*Disk, error) {
	if name == "" || name == "default" {
		return s.def, nil
	}
	if d, ok := s.disks[name]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("storage: no disk named %q: add it to STORAGE_DISKS", name)
}

// New sets up the app's disks from the STORAGE_* settings: the
// default disk, and one per name in STORAGE_DISKS, each with the driver
// its STORAGE_DRIVER (or STORAGE_<NAME>_DRIVER) names (local and memory
// are built in; pass others, such as s3.Driver()). The disks are
// available in every context the app creates ([From]); temporary URLs
// of local disks are signed with APP_KEY.
//
//	st, err := storage.New(app, s3.Driver())
func New(app *anetos.App, drivers ...Driver) (*Storage, error) {
	if _, err := anetos.Resolve[*Storage](app); err == nil {
		return nil, errors.New("storage: New called twice for one app")
	}
	ac, err := config.Get[AppConfig](app.Source())
	if err != nil {
		return nil, err
	}
	all := append([]Driver{LocalDriver(), MemoryDriver()}, drivers...)
	var signer *encryption.Encrypter
	if app.Config().Key != "" {
		if signer, err = encryption.New(app); err != nil {
			return nil, err
		}
	}
	var closers []io.Closer
	open := func(name string) (*Disk, error) {
		cfg, err := LoadConfig(app.Source(), name)
		if err != nil {
			if name != "" {
				return nil, fmt.Errorf("storage: the disk %s (settings %s): %w", name, key(name, "*"), err)
			}
			return nil, err
		}
		i := slices.IndexFunc(all, func(d Driver) bool { return d.Name == cfg.Driver })
		if i < 0 {
			names := make([]string, len(all))
			for j, d := range all {
				names[j] = d.Name
			}
			hint := "its driver"
			switch cfg.Driver {
			case "s3":
				hint = "s3.Driver() from anetos.dev/anetos/drivers/s3"
			case "gcs":
				hint = "gcs.Driver() from anetos.dev/anetos/drivers/gcs"
			}
			return nil, fmt.Errorf("storage: %s is %q, but the drivers are [%s]; pass %s to storage.New",
				key(name, "DRIVER"), cfg.Driver, strings.Join(names, ", "), hint)
		}
		b, err := all[i].Open(app, name, DiskSource(app.Source(), name, all[i].Inherit), cfg)
		if err != nil {
			if name != "" {
				return nil, fmt.Errorf("storage: open the disk %s (%s; its settings are %s): %w", name, cfg.Driver, key(name, "*"), err)
			}
			return nil, fmt.Errorf("storage: open the default disk (%s): %w", cfg.Driver, err)
		}
		if c, ok := b.(io.Closer); ok {
			closers = append(closers, c)
		}
		opts := []DiskOption{BaseURL(cfg.URL), WithLogger(app.Logger().With("component", "storage"))}
		if cfg.Public {
			opts = append(opts, Public())
		}
		if signer != nil {
			opts = append(opts, SignWith(signer))
		}
		d := NewDisk(displayName(name), b, opts...)
		d.now = app.Now // temporary URLs expire on the app's clock, which tests can move
		return d, nil
	}
	closeAll := func() error {
		var errs []error
		for _, c := range closers {
			errs = append(errs, c.Close())
		}
		return errors.Join(errs...)
	}
	def, err := open("")
	if err != nil {
		return nil, err
	}
	var others []*Disk
	for _, name := range ac.Disks {
		name = strings.TrimSpace(name)
		if !diskName.MatchString(name) || name == "default" || slices.ContainsFunc(others, func(d *Disk) bool { return d.name == name }) {
			return nil, errors.Join(fmt.Errorf("storage: STORAGE_DISKS: invalid or repeated disk name %q: use lower-case letters, digits and _, starting with a letter", name), closeAll())
		}
		d, err := open(name)
		if err != nil {
			return nil, errors.Join(err, closeAll())
		}
		others = append(others, d)
	}
	s := NewWithDisks(def, others...)
	app.OnShutdown("storage", func(context.Context) error { return closeAll() })
	app.AddContextValue(storageKey{}, s)
	anetos.Provide(app, s)
	return s, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp(app *anetos.App, drivers ...Driver) (*Storage, error) {
	return New(app, drivers...)
}

func displayName(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

type storageKey struct{}

// WithStorage returns ctx with s, for [From]. [New] makes the app's
// storage available in every context the app creates.
func WithStorage(ctx context.Context, s *Storage) context.Context {
	return context.WithValue(ctx, storageKey{}, s)
}

// ErrNoStorage is returned by [From] when the context has no storage.
var ErrNoStorage = errors.New("storage: no storage in the context: call storage.New at startup, or storage.WithStorage")

// From returns the disk name in ctx, or the default disk without a
// name:
//
//	disk, err := storage.From(ctx)            // the default disk
//	avatars, err := storage.From(ctx, "avatars")
func From(ctx context.Context, name ...string) (*Disk, error) {
	s, ok := ctx.Value(storageKey{}).(*Storage)
	if !ok {
		return nil, ErrNoStorage
	}
	switch len(name) {
	case 0:
		return s.def, nil
	case 1:
		return s.Disk(name[0])
	}
	return nil, errors.New("storage: From takes one disk name")
}
