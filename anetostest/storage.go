// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"errors"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/storage"
)

// Disk checks the files of one of the app's disks. In tests, disks keep
// their files in memory (STORAGE_DRIVER=memory), so each test starts
// with empty disks, unless a named disk sets its own driver
// (STORAGE_<NAME>_DRIVER).
type Disk struct {
	a *App
	d *storage.Disk
}

// Disk returns the app's default disk, or the disk name (one of
// STORAGE_DISKS), for assertions:
//
//	app.Disk("avatars").AssertExists("users/1.png")
func (a *App) Disk(name ...string) *Disk {
	a.t.Helper()
	s, err := anetos.Resolve[*storage.Storage](a.App)
	if err != nil {
		a.t.Fatalf("anetostest: the app has no storage (storage.ForApp in setup)")
	}
	d := s.Default()
	if len(name) > 0 && name[0] != "" {
		if d, err = s.Disk(name[0]); err != nil {
			a.t.Fatalf("anetostest: %v", err)
		}
	}
	return &Disk{a: a, d: d}
}

// Storage returns the disk's storage.Disk, to read or add files.
func (d *Disk) Storage() *storage.Disk { return d.d }

// AssertExists checks that the files exist.
func (d *Disk) AssertExists(paths ...string) *Disk {
	d.a.t.Helper()
	for _, p := range paths {
		ok, err := d.d.Exists(d.a.ctx, p)
		switch {
		case err != nil:
			d.a.t.Errorf("anetostest: disk %s: %v", d.d.Name(), err)
		case !ok:
			d.a.t.Errorf("anetostest: disk %s has no file %s; it has: %s", d.d.Name(), p, d.list())
		}
	}
	return d
}

// AssertMissing checks that the files don't exist.
func (d *Disk) AssertMissing(paths ...string) *Disk {
	d.a.t.Helper()
	for _, p := range paths {
		ok, err := d.d.Exists(d.a.ctx, p)
		switch {
		case err != nil:
			d.a.t.Errorf("anetostest: disk %s: %v", d.d.Name(), err)
		case ok:
			d.a.t.Errorf("anetostest: disk %s has the file %s", d.d.Name(), p)
		}
	}
	return d
}

// AssertContent checks that the file exists with the content want.
func (d *Disk) AssertContent(path, want string) *Disk {
	d.a.t.Helper()
	got, err := d.d.Get(d.a.ctx, path)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		d.a.t.Errorf("anetostest: disk %s has no file %s; it has: %s", d.d.Name(), path, d.list())
	case err != nil:
		d.a.t.Errorf("anetostest: disk %s: %v", d.d.Name(), err)
	case string(got) != want:
		d.a.t.Errorf("anetostest: %s on disk %s is %q, want %q", path, d.d.Name(), excerpt(string(got)), excerpt(want))
	}
	return d
}

// Files returns the paths of the disk's files under prefix ("" for
// all), in order.
func (d *Disk) Files(prefix string) []string {
	d.a.t.Helper()
	var paths []string
	for f, err := range d.d.List(d.a.ctx, prefix) {
		if err != nil {
			d.a.t.Fatalf("anetostest: disk %s: %v", d.d.Name(), err)
		}
		paths = append(paths, f.Path)
	}
	return paths
}

// list is the disk's files, for messages.
func (d *Disk) list() string {
	var paths []string
	for f, err := range d.d.List(d.a.ctx, "") {
		if err != nil {
			break
		}
		if paths = append(paths, f.Path); len(paths) == 20 {
			paths = append(paths, "…")
			break
		}
	}
	if len(paths) == 0 {
		return "none"
	}
	return strings.Join(paths, ", ")
}
