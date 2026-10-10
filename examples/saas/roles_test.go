// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/encryption"

	"anetos.dev/anetos/examples/saas/app/models"
)

// TestRoles builds the app and runs the one binary as four processes,
// one per role (run --only=http, workers, listeners, scheduler), sharing
// a SQLite database and, when ANETOS_TEST_REDIS_URL is set, Redis for
// pub/sub. It follows the work across them: a sign-up on the web process
// is welcomed by the workers; the scheduler ends the trial; a message on
// the billing topic reaches the listeners. Then it runs everything in one
// process. The scheduler runs every minute, so the test takes up to a
// minute or so.
func TestRoles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the app and runs it in several processes, for up to a minute")
	}
	// The processes are killed before the test's deadline, so a timeout
	// leaves none behind.
	ctx, cancel := context.WithCancel(context.Background())
	if deadline, ok := t.Deadline(); ok {
		ctx, cancel = context.WithDeadline(context.Background(), deadline.Add(-10*time.Second))
	}
	t.Cleanup(cancel)
	dir := t.TempDir()
	bin := filepath.Join(dir, "saas")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	addr := freeAddr(t)
	env := map[string]string{
		"APP_ENV": "development", "APP_NAME": "saas", "APP_KEY": encryption.GenerateKey(),
		"APP_URL": "http://" + addr, "HTTP_ADDR": addr, "LOG_FORMAT": "json", "LOG_LEVEL": "info",
		"HTTP_ACCESS_LOG": "false", "DB_DRIVER": "sqlite", "DB_NAME": filepath.Join(dir, "app.db"),
		"QUEUE_DRIVER": "database", "CACHE_DRIVER": "database", "MAIL_DRIVER": "log", "MAIL_FROM_ADDRESS": "hello@example.com", "STORAGE_DRIVER": "memory",
		"PUBSUB_DRIVER": "memory",
	}
	redisURL := os.Getenv("ANETOS_TEST_REDIS_URL")
	if redisURL != "" {
		prefix := "saas-test-" + strings.ToLower(rand.Text()[:10]) + ":"
		env["PUBSUB_DRIVER"], env["REDIS_URL"], env["PUBSUB_PREFIX"] = "redis", redisURL, prefix
		t.Cleanup(func() { deleteKeys(t, redisURL, prefix) })
	}
	a := &apps{ctx, dir, bin, env}
	cli := func(args ...string) string {
		t.Helper()
		out, err := a.command(args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	cli("migrate")

	// A role no component has is an error.
	if out, err := a.command("run", "--only=worker").CombinedOutput(); err == nil || !strings.Contains(string(out), `unknown role "worker"`) {
		t.Errorf("--only=worker: %v\n%s", err, out)
	}

	web := a.start(t, "run", "--only=http")
	workers := a.start(t, "run", "--only=workers")
	listeners := a.start(t, "run", "--only=listeners")
	scheduler := a.start(t, "run", "--only=scheduler")
	for p, role := range map[*proc]string{web: "http", workers: "workers", listeners: "listeners", scheduler: "scheduler"} {
		p.waitLog(t, 10*time.Second, func(l logLine) bool { return l.Msg == "supervisor started" && l.attr("roles") == role })
	}
	b := newBrowser(t, "http://"+addr)
	b.waitUp(t)

	// A sign-up on the web process; the workers send the welcome email.
	b.register(t, "Ada", "ada@example.com")
	isWelcome := func(l logLine) bool {
		return l.Msg == "mail (log driver: not sent)" && l.attr("subject") == "Welcome to SaaS" && strings.Contains(l.attr("to"), "ada@example.com")
	}
	workers.waitLog(t, 15*time.Second, isWelcome)
	if web.hasLog(isWelcome) {
		t.Error("the web process sent the welcome email")
	}
	b.dashboard(t, "Plan: trial")

	// The trial ends (we move its end back); the scheduler's next run, on
	// the minute, moves Ada to the free plan.
	endTrial(t, env["DB_NAME"], "ada@example.com")
	scheduler.waitLog(t, 75*time.Second, func(l logLine) bool { return l.Msg == "trials ended" && l.attr("users") == "1" })
	b.dashboard(t, "Plan: free")

	// The billing service publishes a subscription; the listeners apply it.
	if redisURL == "" {
		t.Log("ANETOS_TEST_REDIS_URL isn't set: pub/sub across processes not checked")
	} else {
		cli("pubsub:publish", "billing.subscription_changed", `{"email":"ada@example.com","plan":"pro"}`)
		listeners.waitLog(t, 15*time.Second, func(l logLine) bool { return l.Msg == "plan changed" && l.attr("plan") == "pro" })
		b.dashboard(t, "Plan: pro")
	}
	for _, p := range []*proc{web, workers, listeners, scheduler} {
		p.stop(t)
	}

	// Everything in one process.
	all := a.start(t, "run")
	all.waitLog(t, 10*time.Second, func(l logLine) bool { return l.Msg == "supervisor started" && l.attr("roles") == "all" })
	b = newBrowser(t, "http://"+addr)
	b.waitUp(t)
	b.register(t, "Bob", "bob@example.com")
	all.waitLog(t, 15*time.Second, func(l logLine) bool {
		return l.Msg == "mail (log driver: not sent)" && l.attr("subject") == "Welcome to SaaS" && strings.Contains(l.attr("to"), "bob@example.com")
	})
	if redisURL != "" {
		cli("pubsub:publish", "billing.subscription_changed", `{"email":"bob@example.com","plan":"team"}`)
		all.waitLog(t, 15*time.Second, func(l logLine) bool { return l.Msg == "plan changed" && l.attr("plan") == "team" })
	}
	all.stop(t)
}

// apps runs the built app.
type apps struct {
	ctx      context.Context // killed when it ends
	dir, bin string
	env      map[string]string
}

// command returns the app's command with args, in its directory, with
// its settings and nothing else of the test's environment (a DB_URL or
// SOCIAL_* exported in the shell would change the app).
func (a *apps) command(args ...string) *exec.Cmd {
	c := exec.CommandContext(a.ctx, a.bin, args...)
	c.Dir = a.dir
	for _, k := range []string{"PATH", "HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			c.Env = append(c.Env, k+"="+v)
		}
	}
	for k, v := range a.env {
		c.Env = append(c.Env, k+"="+v)
	}
	return c
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// logLine is a line of the app's JSON log.
type logLine struct {
	Msg   string
	attrs map[string]any
}

func (l logLine) attr(key string) string {
	if v, ok := l.attrs[key]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

// proc is a running app process, with its log.
type proc struct {
	ctx    context.Context // the test's: done shortly before its deadline
	name   string
	cmd    *exec.Cmd
	mu     sync.Mutex
	log    []logLine
	raw    bytes.Buffer
	exited chan struct{} // closed when it has exited, with err set
	err    error
}

func (a *apps) start(t *testing.T, args ...string) *proc {
	t.Helper()
	p := &proc{ctx: a.ctx, name: strings.Join(args, " "), cmd: a.command(args...), exited: make(chan struct{})}
	r, w := io.Pipe()
	p.cmd.Stdout, p.cmd.Stderr = w, w
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 64<<10), 1<<20)
		for s.Scan() {
			var m map[string]any
			p.mu.Lock()
			p.raw.Write(append(s.Bytes(), '\n'))
			if json.Unmarshal(s.Bytes(), &m) == nil {
				msg, _ := m["msg"].(string)
				p.log = append(p.log, logLine{msg, m})
			}
			p.mu.Unlock()
		}
		_, _ = io.Copy(io.Discard, r) // after a line too long to scan, so the app never blocks writing
	}()
	go func() {
		p.err = p.cmd.Wait()
		w.Close()
		close(p.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-p.exited:
		default:
			_ = p.cmd.Process.Kill()
			<-p.exited
		}
		if t.Failed() {
			p.mu.Lock()
			t.Logf("%s:\n%s", p.name, p.raw.String())
			p.mu.Unlock()
		}
	})
	return p
}

func (p *proc) hasLog(match func(logLine) bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.ContainsFunc(p.log, match)
}

func (p *proc) waitLog(t *testing.T, timeout time.Duration, match func(logLine) bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if p.hasLog(match) {
			return
		}
		if p.ctx.Err() != nil {
			t.Fatalf("%s: the test's deadline is near (go test -timeout)", p.name) // fail before the timeout's panic, so cleanups run
		}
	}
	t.Fatalf("%s: no such log line in %v", p.name, timeout)
}

// stop ends the process as a platform would, with SIGTERM.
func (p *proc) stop(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.exited:
		if p.err != nil {
			t.Errorf("%s: %v", p.name, p.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("%s didn't stop", p.name)
	case <-p.ctx.Done():
		t.Fatalf("%s: the test's deadline is near (go test -timeout)", p.name)
	}
}

// browser is a user's browser: cookies, and redirects not followed.
type browser struct {
	base   string
	client *http.Client
}

func newBrowser(t *testing.T, base string) *browser {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &browser{base, &http.Client{Jar: jar, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) get(t *testing.T, path string) (int, string) {
	t.Helper()
	res, err := b.client.Get(b.base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

func (b *browser) waitUp(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if res, err := b.client.Get(b.base + "/login"); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
	}
	t.Fatal("the web server didn't start")
}

var csrfField = regexp.MustCompile(`name="_token" value="([^"]+)"`)

func (b *browser) register(t *testing.T, name, email string) {
	t.Helper()
	_, page := b.get(t, "/register")
	m := csrfField.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no CSRF field on /register:\n%s", page)
	}
	res, err := b.client.PostForm(b.base+"/register", url.Values{"_token": {m[1]}, "name": {name}, "email": {email},
		"password": {"correct horse"}, "password_confirmation": {"correct horse"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/dashboard" {
		t.Fatalf("register: %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func (b *browser) dashboard(t *testing.T, want string) {
	t.Helper()
	if code, page := b.get(t, "/dashboard"); code != http.StatusOK || !strings.Contains(page, want) {
		t.Fatalf("dashboard: %d, without %q:\n%s", code, want, page)
	}
}

// endTrial moves the end of the user's trial to the past, in the app's
// database.
func endTrial(t *testing.T, file, email string) {
	t.Helper()
	d, err := db.Open(sqlite.Driver(), db.Config{Driver: "sqlite", Name: file})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	past := time.Now().UTC().Add(-time.Minute)
	n, err := db.Query[models.User](db.WithDB(context.Background(), d)).Where(models.UserCols.Email.Eq(email)).
		Update(models.UserCols.TrialEndsAt.Set(&past))
	if err != nil || n != 1 {
		t.Fatalf("end the trial: %d, %v", n, err)
	}
}

// deleteKeys removes the Redis keys the processes made.
func deleteKeys(t *testing.T, redisURL, prefix string) {
	opt, err := goredis.ParseURL(redisURL)
	if err != nil {
		t.Error(err)
		return
	}
	r := goredis.NewClient(opt)
	defer r.Close()
	ctx := context.Background()
	keys, err := r.Keys(ctx, prefix+"*").Result()
	if err == nil && len(keys) > 0 {
		err = r.Del(ctx, keys...).Err()
	}
	if err != nil {
		t.Error(err)
	}
}
