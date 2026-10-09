// SPDX-License-Identifier: Apache-2.0

// Package tailwind runs Tailwind CSS's standalone CLI for the projects of
// the tailwind design kit: it downloads the release this CLI pins, for the
// computer's platform, checks it against the digest written here, and keeps
// it in the user's cache directory (design D301).
package tailwind

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Version is the Tailwind CSS release the tailwind kit is built for, and
// the one Binary downloads.
const Version = "4.3.3"

// Input is the project's source stylesheet, which marks a project of the
// tailwind kit; Output is the stylesheet Build compiles from it.
const (
	Input  = "views/ui/tailwind.css"
	Output = "public/static/app.css"
)

// Env names a tailwindcss binary to use instead of the downloaded one.
const Env = "ANETOS_TAILWIND"

// checksums are the SHA-256 digests of Version's release assets, from
// its sha256sums.txt (scripts/update-kits.sh prints them for a new one).
var checksums = map[string]string{
	"tailwindcss-linux-arm64":      "55fd0b241214eff3de1e8ee4f22796662f2d2e7a49bcfca7477cfd0bac398195",
	"tailwindcss-linux-arm64-musl": "71ea4be79c9de9827545682df3e040053fb535d37c71ed2cfdedf9385a0868e0",
	"tailwindcss-linux-x64":        "dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a",
	"tailwindcss-linux-x64-musl":   "a04d34ceacc8f52cbe8920ad846cdeb61d3d0021dba32db0d1f77c9d9fad7a6c",
	"tailwindcss-macos-arm64":      "cdf646702987a743464dff4d9c60fd4480d1c1e73dd819a9a67f1078815dce9d",
	"tailwindcss-macos-x64":        "7922e0953f2110c05976e3bf58f14e643d90427575e766b7d433f5f80cbee7e1",
	"tailwindcss-windows-x64.exe":  "e0e260ce048014e9268f6237ff18f8ccf02cef521cbd0ae04e82c2cdf7aa3955",
}

// Where releases and the cache are; tests change them.
var (
	releaseURL = "https://github.com/tailwindlabs/tailwindcss/releases/download/v" + Version + "/"
	cacheDir   = os.UserCacheDir
	musl       = hasMusl
)

// Uses reports whether the project in root is of the tailwind kit: it has
// views/ui/tailwind.css.
func Uses(root string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(Input)))
	return err == nil
}

// Asset returns the name of the release asset for a platform (GOOS,
// GOARCH; musl for a Linux without glibc), or an error naming the
// platforms Tailwind publishes.
func Asset(goos, goarch string, musl bool) (string, error) {
	sys := map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}[goos]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	name := "tailwindcss-" + sys + "-" + arch
	switch {
	case sys == "windows":
		name += ".exe"
	case sys == "linux" && musl:
		name += "-musl"
	}
	if _, ok := checksums[name]; !ok || sys == "" || arch == "" {
		return "", fmt.Errorf("tailwind: Tailwind CSS publishes no standalone CLI for %s/%s: install one and set %s to its path", goos, goarch, Env)
	}
	return name, nil
}

// hasMusl reports whether Linux's C library is musl (Alpine), whose
// systems need the -musl build: /bin/sh's program interpreter is musl's.
// A glibc system with musl installed beside it (Debian's musl package)
// keeps the glibc build, which is the only one that runs there.
func hasMusl() bool {
	if f, err := elf.Open("/bin/sh"); err == nil {
		defer f.Close()
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP {
				b, err := io.ReadAll(p.Open())
				return err == nil && bytes.Contains(b, []byte("musl"))
			}
		}
	}
	// No interpreter to read: musl's loader without glibc's.
	m, _ := filepath.Glob("/lib/ld-musl-*.so.1")
	g, _ := filepath.Glob("/lib*/ld-linux*.so.*")
	return len(m) > 0 && len(g) == 0
}

// Binary returns the path of the tailwindcss binary to run: $ANETOS_TAILWIND
// when set (checked to be tailwindcss, and warned about through logf when
// its version isn't Version), else the cached download, downloading and
// checking it first if needed (with a line through logf).
func Binary(ctx context.Context, logf func(format string, args ...any)) (string, error) {
	if bin := os.Getenv(Env); bin != "" {
		if strings.ContainsRune(bin, filepath.Separator) || strings.ContainsRune(bin, '/') {
			// Compile runs it in the project's root, not here.
			if abs, err := filepath.Abs(bin); err == nil {
				bin = abs
			}
		}
		v, err := binaryVersion(ctx, bin)
		if err != nil {
			return "", fmt.Errorf("tailwind: %s=%s: %w", Env, bin, err)
		}
		if v != Version {
			logf("%s is Tailwind CSS v%s; this anetos is made for v%s, whose output may differ", bin, v, Version)
		}
		return bin, nil
	}
	asset, err := Asset(runtime.GOOS, runtime.GOARCH, runtime.GOOS == "linux" && musl())
	if err != nil {
		return "", err
	}
	base, err := cacheDir()
	if err != nil {
		return "", fmt.Errorf("tailwind: no cache directory (%w): set %s to a tailwindcss binary", err, Env)
	}
	dir := filepath.Join(base, "anetos", "tailwindcss", "v"+Version)
	bin := filepath.Join(dir, asset)
	// The cached binary is checked again before it runs: the cache may
	// be shared (a BuildKit mount) or damaged since.
	if err := verify(bin, asset); err == nil {
		return bin, nil
	}
	logf("downloading Tailwind CSS v%s (%s) into %s", Version, asset, dir)
	if err := download(ctx, asset, dir); err != nil {
		return "", fmt.Errorf("%w\nDownload %s%s yourself (SHA-256 %s) and set %s to its path", err, releaseURL, asset, checksums[asset], Env)
	}
	return bin, nil
}

// verify checks that the file at path is asset's release, by its digest.
func verify(path, asset string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != checksums[asset] {
		return fmt.Errorf("tailwind: %s has SHA-256 %s, not %s: not using it", asset, got, checksums[asset])
	}
	return nil
}

// Limits of a download: the largest a release's binary may be, and how
// long the server may send nothing (a dead proxy, a dropped network).
var (
	maxSize = int64(200 << 20)
	stall   = 30 * time.Second
)

// download fetches asset into dir, checks its digest, and renames it into
// place only then, so a cut-off or altered download is never run.
func download(ctx context.Context, asset, dir string) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// A watchdog: the download stops when no byte came for stall.
	errStalled := fmt.Errorf("tailwind: downloading %s: nothing received for %s", asset, stall)
	idle := time.AfterFunc(stall, func() { cancel(errStalled) })
	defer idle.Stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL+asset, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if c := context.Cause(ctx); errors.Is(c, errStalled) {
			return c
		}
		return fmt.Errorf("tailwind: downloading %s: %w", asset, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("tailwind: downloading %s: %s", asset, res.Status)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tailwind: %w", err)
	}
	removeStale(dir, asset)
	tmp, err := os.CreateTemp(dir, asset+".*.tmp")
	if err != nil {
		return fmt.Errorf("tailwind: %w", err)
	}
	defer os.Remove(tmp.Name()) // after the rename, there's nothing to remove
	h := sha256.New()
	body := &progress{r: io.LimitReader(res.Body, maxSize+1), idle: idle, stall: stall}
	n, err := io.Copy(io.MultiWriter(tmp, h), body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if c := context.Cause(ctx); err != nil && errors.Is(c, errStalled) {
		return c
	}
	if err != nil {
		return fmt.Errorf("tailwind: downloading %s: %w", asset, err)
	}
	if n > maxSize {
		return fmt.Errorf("tailwind: %s is larger than %d MB: not using it", asset, maxSize>>20)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != checksums[asset] {
		return fmt.Errorf("tailwind: %s has SHA-256 %s, not %s: not using it", asset, got, checksums[asset])
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return fmt.Errorf("tailwind: %w", err)
	}
	bin := filepath.Join(dir, asset)
	if err := os.Rename(tmp.Name(), bin); err != nil {
		// On Windows, another anetos may have put the same binary there
		// first, and be running it: use that one.
		if verify(bin, asset) == nil {
			return nil
		}
		return fmt.Errorf("tailwind: %w", err)
	}
	return nil
}

// progress reads a download, putting off the stall watchdog at each read
// that brings bytes.
type progress struct {
	r     io.Reader
	idle  *time.Timer
	stall time.Duration
}

func (p *progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.idle.Reset(p.stall)
	}
	return n, err
}

// removeStale removes the temporary files of downloads that ended without
// cleaning up (a killed process), an hour old or more.
func removeStale(dir, asset string) {
	old, _ := filepath.Glob(filepath.Join(dir, asset+".*.tmp"))
	for _, f := range old {
		if fi, err := os.Stat(f); err == nil && time.Since(fi.ModTime()) > time.Hour {
			_ = os.Remove(f)
		}
	}
}

var versionRE = regexp.MustCompile(`tailwindcss v(\d+\.\d+\.\d+\S*)`)

// binaryVersion runs bin --help and reads its version.
func binaryVersion(ctx context.Context, bin string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	m := versionRE.FindSubmatch(out)
	if m == nil {
		if err == nil {
			err = errors.New("no version in its --help")
		}
		return "", fmt.Errorf("not a tailwindcss binary: %w", err)
	}
	return string(m[1]), nil
}

// Compile runs bin on the project in root and returns the minified
// stylesheet, without writing it.
func Compile(ctx context.Context, bin, root string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(ctx, bin, "--input", filepath.FromSlash(Input), "--output", "-", "--minify")
	c.Dir = root
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("tailwindcss: %w%s", err, plain(stderr.Bytes()))
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("tailwindcss wrote nothing%s", plain(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

var (
	ansi   = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
	banner = regexp.MustCompile(`(?m)^\S* ?tailwindcss v\S+\s*$`)
)

// plain is Tailwind's messages for an error: without its colors (it
// writes them to pipes too) or its banner, after a newline; "" when
// there are none.
func plain(b []byte) string {
	s := banner.ReplaceAllString(ansi.ReplaceAllString(string(b), ""), "")
	if s = strings.TrimSpace(s); s == "" {
		return ""
	}
	return "\n" + s
}

// Build compiles the project's stylesheet into public/static/app.css,
// writing it only when its content changes, and reports whether it did.
func Build(ctx context.Context, bin, root string) (bool, error) {
	css, err := Compile(ctx, bin, root)
	if err != nil {
		return false, err
	}
	out := filepath.Join(root, filepath.FromSlash(Output))
	if old, err := os.ReadFile(out); err == nil && bytes.Equal(old, css) {
		return false, nil
	}
	if err := os.WriteFile(out, css, 0o644); err != nil {
		return false, fmt.Errorf("tailwind: %w", err)
	}
	return true, nil
}
