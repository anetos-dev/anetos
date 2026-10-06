// SPDX-License-Identifier: Apache-2.0

package devserver

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInject(t *testing.T) {
	for in, want := range map[string]string{
		"<html><body>x</body></html>":       "<html><body>x" + reloadScript + "</body></html>",
		"<BODY>x</BODY>":                    "<BODY>x" + reloadScript + "</BODY>",
		"fragment":                          "fragment" + reloadScript,
		"<p>İstanbul</p></body>":            "<p>İstanbul</p>" + reloadScript + "</body>",
		strings.Repeat("Ⱥ", 20) + "</Body>": strings.Repeat("Ⱥ", 20) + reloadScript + "</Body>",
	} {
		if got := string(inject([]byte(in))); got != want {
			t.Errorf("inject(%q) = %q", in, got)
		}
	}
}

func TestWatched(t *testing.T) {
	for p, want := range map[string]bool{
		"main.go": true, "views/home.templ": true, ".env": true, ".env.local": true, "go.mod": true,
		"public/static/app.css": true, "views/home_templ.go": false, "app/models/models_gen.go": false,
		"main_test.go": false, "README.md": false, "database/app.db": false, "locales/bn/app.yaml": true,
	} {
		if got := watched(p); got != want {
			t.Errorf("watched(%s) = %v", p, got)
		}
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.go"), "package main")
	write(t, filepath.Join(dir, "tmp", "x.go"), "package x")
	write(t, filepath.Join(dir, "app", "storage", "s.go"), "package storage") // only the root's storage is skipped
	write(t, filepath.Join(dir, ".git", "x.go"), "package x")
	before := snapshot(dir)
	if len(before) != 2 {
		t.Errorf("snapshot = %v", before)
	}
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(dir, "main.go"), "package main // changed")
	write(t, filepath.Join(dir, "b.go"), "package main")
	if got := diff(before, snapshot(dir)); len(got) != 2 {
		t.Errorf("diff = %v", got)
	}
}

func write(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

const app = `package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body>VERSION</body></html>")
	})
	if err := http.ListenAndServe(os.Getenv("HTTP_ADDR"), nil); err != nil {
		panic(err)
	}
}
`

func TestDevLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("builds programs")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module devtest\n\ngo 1.26\n")
	write(t, filepath.Join(dir, "main.go"), strings.Replace(app, "VERSION", "v1", 1))

	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Dir: dir, Addr: "127.0.0.1:0", Poll: 100 * time.Millisecond, Out: io.Discard,
			OnListen: func(a string) { addrCh <- a }})
	}()
	base := "http://" + <-addrCh
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("Run didn't stop")
		}
	})

	get := func() (int, string) {
		res, err := http.Get(base + "/")
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	waitFor := func(what string, ok func(int, string) bool) {
		t.Helper()
		// Generous: under `make test` the first build competes with the other
		// packages' builds and the end-to-end test's go commands.
		deadline := time.Now().Add(3 * time.Minute)
		for {
			code, body := get()
			if ok(code, body) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("waiting for %s: last response %d %q", what, code, body)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitFor("v1", func(code int, body string) bool {
		return code == 200 && strings.Contains(body, "v1"+reloadScript+"</body>")
	})

	// A page listening for reloads is told when the new version is up.
	res, err := http.Get(base + reloadPath) //nolint:bodyclose // closed below, read by a goroutine
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	events := make(chan string, 10)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			events <- sc.Text()
		}
	}()
	write(t, filepath.Join(dir, "main.go"), strings.Replace(app, "VERSION", "v2", 1))
	deadline := time.After(3 * time.Minute)
	for got := false; !got; {
		select {
		case line := <-events:
			got = line == "event: reload"
		case <-deadline:
			t.Fatal("no reload event")
		}
	}
	waitFor("v2", func(code int, body string) bool { return code == 200 && strings.Contains(body, "v2") })

	// A build error shows in the browser, and fixing it recovers.
	write(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() { undefinedThing() }\n")
	waitFor("the build error", func(code int, body string) bool {
		return code == 500 && strings.Contains(body, "Build failed") && strings.Contains(body, "undefinedThing")
	})
	write(t, filepath.Join(dir, "main.go"), strings.Replace(app, "VERSION", "v3", 1))
	waitFor("v3", func(code int, body string) bool { return code == 200 && strings.Contains(body, "v3") })

	// An app that exits is reported too.
	write(t, filepath.Join(dir, "main.go"), "package main\n\nimport \"os\"\n\nfunc main() { println(\"no server here\"); os.Exit(3) }\n")
	waitFor("the start error", func(code int, body string) bool {
		return code == 500 && strings.Contains(body, "didn&#39;t start") && strings.Contains(body, "no server here")
	})
}

// Only local names, IP addresses and the given hosts reach the dev
// server (DNS rebinding).
func TestAllowedHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost:8080": true, "LOCALHOST": true, "app.localhost:8080": true, "127.0.0.1:8080": true,
		"[::1]:8080": true, "192.168.1.20:8080": true, "blog.test:8080": true, "blog.test.": true,
		"attacker.example:8080": false, "localhost.attacker.example": false, "": false, "blog.test.evil": false,
	} {
		if got := allowedHost(host, []string{"blog.test"}); got != want {
			t.Errorf("allowedHost(%q) = %v, want %v", host, got, want)
		}
	}
	d := &dev{opts: Options{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "attacker.example:8080"
	d.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign host: %d", rec.Code)
	}
}
