// SPDX-License-Identifier: Apache-2.0

package tailwind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAsset(t *testing.T) {
	for _, c := range []struct {
		goos, goarch string
		musl         bool
		want         string
	}{
		{"linux", "amd64", false, "tailwindcss-linux-x64"},
		{"linux", "amd64", true, "tailwindcss-linux-x64-musl"},
		{"linux", "arm64", true, "tailwindcss-linux-arm64-musl"},
		{"darwin", "arm64", false, "tailwindcss-macos-arm64"},
		{"darwin", "amd64", true, "tailwindcss-macos-x64"}, // musl is Linux's
		{"windows", "amd64", false, "tailwindcss-windows-x64.exe"},
		{"windows", "arm64", false, ""},
		{"freebsd", "amd64", false, ""},
		{"linux", "386", false, ""},
	} {
		got, err := Asset(c.goos, c.goarch, c.musl)
		if got != c.want || (err == nil) != (c.want != "") {
			t.Errorf("Asset(%s, %s, %v) = %q, %v", c.goos, c.goarch, c.musl, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), Env) {
			t.Errorf("Asset(%s, %s): %v doesn't name %s", c.goos, c.goarch, err, Env)
		}
	}
	// Every pinned digest is a SHA-256.
	for name, sum := range checksums {
		if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
			t.Errorf("%s: %q", name, sum)
		}
	}
}

// fakeRelease serves body as this platform's asset, with pinned's digest
// pinned, and counts the requests.
func fakeRelease(t *testing.T, body, pinned string, status int) (*atomic.Int32, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("runs shell scripts as the binary")
	}
	asset, err := Asset(runtime.GOOS, runtime.GOARCH, runtime.GOOS == "linux" && musl())
	if err != nil {
		t.Skip(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/"+asset {
			http.NotFound(w, r)
			return
		}
		if status == -2 { // stalls after the headers
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if status == -1 { // cut off: announce more than is sent
			w.Header().Set("Content-Length", "1000000")
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cache := t.TempDir()
	sum := sha256.Sum256([]byte(pinned))
	oldURL, oldCache, oldSum := releaseURL, cacheDir, checksums[asset]
	releaseURL, cacheDir, checksums[asset] = srv.URL+"/", func() (string, error) { return cache, nil }, hex.EncodeToString(sum[:])
	t.Cleanup(func() { releaseURL, cacheDir, checksums[asset] = oldURL, oldCache, oldSum })
	t.Setenv(Env, "")
	return &hits, filepath.Join(cache, "anetos", "tailwindcss", "v"+Version, asset)
}

const fakeBin = "#!/bin/sh\necho 'a{color:red}'\n"

func TestBinaryDownloads(t *testing.T) {
	hits, path := fakeRelease(t, fakeBin, fakeBin, http.StatusOK)
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, f) }
	bin, err := Binary(context.Background(), logf)
	if err != nil || bin != path {
		t.Fatalf("Binary = %q, %v", bin, err)
	}
	if fi, err := os.Stat(bin); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("binary: %v %v", fi, err)
	}
	if len(logged) != 1 || hits.Load() != 1 {
		t.Errorf("logged %v, %d requests", logged, hits.Load())
	}
	// Cached: no second download.
	if bin, err := Binary(context.Background(), logf); err != nil || bin != path || hits.Load() != 1 {
		t.Errorf("again: %q, %v, %d requests", bin, err, hits.Load())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("the cache has %d files: a temporary one left?", len(entries))
	}
	// A cached binary that changed since is downloaded again.
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho planted\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if bin, err := Binary(context.Background(), logf); err != nil || hits.Load() != 2 {
		t.Fatalf("changed: %q, %v, %d requests", bin, err, hits.Load())
	}
	if b, _ := os.ReadFile(path); string(b) != fakeBin {
		t.Errorf("the cache keeps %q", b)
	}
	// A relative ANETOS_TAILWIND is made absolute: Compile runs it
	// elsewhere.
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'tailwindcss v"+Version+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(path))
	t.Setenv(Env, "."+string(filepath.Separator)+filepath.Base(path))
	if bin, err := Binary(context.Background(), logf); err != nil || bin != path {
		t.Errorf("relative %s: %q, %v", Env, bin, err)
	}
}

func TestPlain(t *testing.T) {
	in := "\x1b[2m≈ tailwindcss v4.3.3\x1b[22m\n\n\x1b[31mError:\x1b[39m Cannot apply unknown utility class `bg-nope`\n"
	if got := plain([]byte(in)); got != "\nError: Cannot apply unknown utility class `bg-nope`" {
		t.Errorf("plain = %q", got)
	}
	if got := plain([]byte("≈ tailwindcss v4.3.3\n")); got != "" {
		t.Errorf("banner alone: %q", got)
	}
}

func TestBinaryRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"altered": {http.StatusOK, "#!/bin/sh\necho evil\n", "SHA-256"},
		"missing": {http.StatusNotFound, fakeBin, "404"},
		"cut off": {-1, fakeBin, "unexpected EOF"},
		"stalled": {-2, fakeBin, "nothing received for"},
	} {
		t.Run(name, func(t *testing.T) {
			old := stall
			stall = 200 * time.Millisecond
			defer func() { stall = old }()
			_, path := fakeRelease(t, c.body, fakeBin, c.status)
			_, err := Binary(context.Background(), func(string, ...any) {})
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), Env) {
				t.Fatalf("Binary: %v", err)
			}
			entries, _ := os.ReadDir(filepath.Dir(path))
			if len(entries) != 0 {
				t.Errorf("the cache keeps %v", entries)
			}
		})
	}
}

func TestBinaryOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs shell scripts as the binary")
	}
	dir := t.TempDir()
	script := func(name, out string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, f) }
	same := script("same", "≈ tailwindcss v"+Version)
	t.Setenv(Env, same)
	if bin, err := Binary(context.Background(), logf); err != nil || bin != same || len(logged) != 0 {
		t.Errorf("same version: %q, %v, %v", bin, err, logged)
	}
	other := script("other", "≈ tailwindcss v4.0.0")
	t.Setenv(Env, other)
	if bin, err := Binary(context.Background(), logf); err != nil || bin != other || len(logged) != 1 {
		t.Errorf("other version: %q, %v, %v", bin, err, logged)
	}
	t.Setenv(Env, script("not", "hello"))
	if _, err := Binary(context.Background(), logf); err == nil || !strings.Contains(err.Error(), "not a tailwindcss binary") {
		t.Errorf("not tailwind: %v", err)
	}
	t.Setenv(Env, filepath.Join(dir, "missing"))
	if _, err := Binary(context.Background(), logf); err == nil {
		t.Error("a missing binary")
	}
}

func TestBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs shell scripts as the binary")
	}
	root := t.TempDir()
	if Uses(root) {
		t.Error("Uses of an empty project")
	}
	for _, f := range []string{Input, Output} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !Uses(root) {
		t.Error("Uses")
	}
	bin := filepath.Join(t.TempDir(), "tailwindcss")
	// The fake checks its arguments, then writes the stylesheet.
	src := "#!/bin/sh\n[ \"$*\" = '--input " + filepath.FromSlash(Input) + " --output - --minify' ] || { echo \"bad args: $*\" >&2; exit 3; }\necho 'a{color:red}'\n"
	if err := os.WriteFile(bin, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, filepath.FromSlash(Output))
	if changed, err := Build(context.Background(), bin, root); err != nil || !changed {
		t.Fatalf("Build = %v, %v", changed, err)
	}
	if b, _ := os.ReadFile(out); string(b) != "a{color:red}\n" {
		t.Errorf("app.css = %q", b)
	}
	if changed, err := Build(context.Background(), bin, root); err != nil || changed {
		t.Errorf("unchanged: Build = %v, %v", changed, err)
	}
	fail := filepath.Join(t.TempDir(), "fail")
	if err := os.WriteFile(fail, []byte("#!/bin/sh\necho 'Error: cannot apply unknown utility class' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), fail, root); err == nil || !strings.Contains(err.Error(), "unknown utility") {
		t.Errorf("failing: %v", err)
	}
	if b, _ := os.ReadFile(out); string(b) != "a{color:red}\n" {
		t.Errorf("a failed build changed app.css: %q", b)
	}
}

// With ANETOS_TEST_TAILWIND=1, the real release: downloaded (or
// $ANETOS_TAILWIND) and run (CI's latest Go job; scripts/update-kits.sh).
func TestRealBinary(t *testing.T) {
	if os.Getenv("ANETOS_TEST_TAILWIND") != "1" {
		t.Skip("set ANETOS_TEST_TAILWIND=1 to download and run Tailwind CSS")
	}
	bin, err := Binary(context.Background(), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := binaryVersion(context.Background(), bin); err != nil || v != Version {
		t.Errorf("version %q, %v", v, err)
	}
}
