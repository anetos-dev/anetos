// SPDX-License-Identifier: Apache-2.0

package mailer_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"
)

func newApp(t *testing.T, env config.Map, logs io.Writer) *anetos.App {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "APP_NAME": "Shop", "APP_URL": "https://shop.example.com"}
	maps.Copy(src, env)
	if logs == nil {
		logs = io.Discard
	}
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(logs))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestForApp(t *testing.T) {
	for name, env := range map[string]config.Map{
		"unknown driver":   {"MAIL_DRIVER": "carrier-pigeon"},
		"bad from address": {"MAIL_FROM_ADDRESS": "Shop <shop@example.com>"},
		"bad SMTP URL":     {"MAIL_DRIVER": "smtp", "MAIL_SMTP_URL": "http://x"},
	} {
		if _, err := mailer.ForApp(newApp(t, env, nil)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// The hint names Postmark's driver only for postmark.
	for drv, want := range map[string]string{"postmark": "pass postmark.Driver()", "postmrk": "check its spelling"} {
		_, err := mailer.ForApp(newApp(t, config.Map{"MAIL_DRIVER": drv}, nil))
		if err == nil || !strings.Contains(err.Error(), want) || (drv != "postmark" && strings.Contains(err.Error(), "postmark.Driver")) {
			t.Errorf("MAIL_DRIVER=%s: %v", drv, err)
		}
	}
	app := newApp(t, config.Map{"MAIL_DRIVER": "memory", "MAIL_FROM_ADDRESS": "shop@example.com"}, nil)
	m, err := mailer.ForApp(app)
	check(t, err)
	if _, err := mailer.ForApp(app); err == nil {
		t.Error("ForApp twice: no error")
	}
	if anetos.MustResolve[*mailer.Mailer](app) != m {
		t.Error("the app doesn't provide the mailer")
	}
	ctx := app.Context(context.Background())
	check(t, mailer.Send(ctx, welcome{"Ada", "ada@example.com"}))
	sent := m.Transport().(*mailer.MemoryTransport).Sent()
	// MAIL_FROM_NAME defaults to APP_NAME; URL uses APP_URL.
	if len(sent) != 1 || sent[0].From.String() != `"Shop" <shop@example.com>` || !strings.Contains(sent[0].Text, "https://shop.example.com/start") {
		t.Fatalf("sent %+v", sent)
	}
	// Queue needs the queue.
	if err := mailer.Queue(ctx, welcome{"Ada", "ada@example.com"}); err == nil || !strings.Contains(err.Error(), "queue.ForApp") {
		t.Errorf("Queue without a queue = %v", err)
	}
	if err := mailer.Send(context.Background(), welcome{}); !errors.Is(err, mailer.ErrNoMailer) {
		t.Errorf("Send without a mailer = %v", err)
	}
	m.Transport().(*mailer.MemoryTransport).Reset()
	if n := len(m.Transport().(*mailer.MemoryTransport).Sent()); n != 0 {
		t.Errorf("%d emails after Reset", n)
	}
}

func TestQueue(t *testing.T) {
	for _, driver := range []string{"sync", "memory"} {
		t.Run(driver, func(t *testing.T) {
			app := newApp(t, config.Map{"MAIL_DRIVER": "memory", "MAIL_FROM_ADDRESS": "shop@example.com", "QUEUE_DRIVER": driver}, nil)
			q, err := queue.ForApp(app)
			check(t, err)
			m, err := mailer.ForApp(app)
			check(t, err)
			ctx := app.Context(context.Background())
			sentAt := date.Add(time.Hour)
			mailer.SetNow(m, func() time.Time { return date })
			check(t, mailer.Queue(ctx, &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "Queued",
				HTML: html("<p>Hi</p>"), Attachments: []mailer.Attachment{{Filename: "a.txt", Data: []byte("data")}}}))
			mem := m.Transport().(*mailer.MemoryTransport)
			if driver == "memory" {
				if len(mem.Sent()) != 0 {
					t.Fatal("sent before a worker ran the job")
				}
				mailer.SetNow(m, func() time.Time { return sentAt }) // the job runs an hour later
				runCtx, cancel := context.WithCancel(ctx)
				done := make(chan error, 1)
				go func() { done <- q.Run(runCtx) }()
				deadline := time.Now().Add(5 * time.Second)
				for len(mem.Sent()) == 0 && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				cancel()
				check(t, <-done)
			}
			sent := mem.Sent()
			if len(sent) != 1 || sent[0].Subject != "Queued" || string(sent[0].Attachments[0].Data) != "data" || sent[0].Text != "Hi" {
				t.Fatalf("sent %+v", sent)
			}
			if driver == "memory" && !sent[0].Date.Equal(sentAt) {
				t.Errorf("Date = %s, want when the job ran, %s", sent[0].Date, sentAt)
			}
			// Errors are the render's, at once.
			if err := mailer.Queue(ctx, &mailer.Message{Subject: "No one", HTML: html("x")}); err == nil {
				t.Error("an invalid message was queued")
			}
		})
	}
}

// failing is a transport that fails.
type failing struct{ err error }

func (f failing) Send(context.Context, *mailer.Outgoing) error { return f.err }

// queue.ForApp may come after mailer.ForApp.
func TestQueueAfterMailer(t *testing.T) {
	app := newApp(t, config.Map{"MAIL_DRIVER": "memory", "MAIL_FROM_ADDRESS": "shop@example.com", "QUEUE_DRIVER": "sync"}, nil)
	m, err := mailer.ForApp(app)
	check(t, err)
	_, err = queue.ForApp(app)
	check(t, err)
	check(t, app.Boot(context.Background()))
	check(t, mailer.Queue(app.Context(context.Background()), &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "S", Text: "x"}))
	if n := len(m.Transport().(*mailer.MemoryTransport).Sent()); n != 1 {
		t.Errorf("%d emails", n)
	}
}

func TestSendErrors(t *testing.T) {
	m := mailer.New(failing{queue.Permanent(errors.New("550 no"))}, mailer.DefaultFrom(mailer.Address{Address: "shop@example.com"}))
	err := m.Send(context.Background(), &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "S", Text: "x"})
	if err == nil || !queue.IsPermanent(err) || !strings.Contains(err.Error(), `"S" to a@example.com`) {
		t.Errorf("err = %v", err)
	}
	// A queued email that fails for good is a failed job.
	app := newApp(t, config.Map{"MAIL_DRIVER": "bad", "MAIL_FROM_ADDRESS": "shop@example.com", "QUEUE_DRIVER": "sync"}, nil)
	_, err = queue.ForApp(app)
	check(t, err)
	_, err = mailer.ForApp(app, mailer.Driver{Name: "bad", Open: func(*anetos.App, mailer.Config) (mailer.Transport, error) {
		return failing{queue.Permanent(errors.New("550 no"))}, nil
	}})
	check(t, err)
	err = mailer.Queue(app.Context(context.Background()), &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "S", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "550") {
		t.Errorf("Queue with the sync driver = %v", err)
	}
	// A driver that fails to open.
	app = newApp(t, config.Map{"MAIL_DRIVER": "broken"}, nil)
	if _, err := mailer.ForApp(app, mailer.Driver{Name: "broken", Open: func(*anetos.App, mailer.Config) (mailer.Transport, error) {
		return nil, errors.New("no token")
	}}); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Errorf("ForApp = %v", err)
	}
}

func TestLogTransport(t *testing.T) {
	var logs bytes.Buffer
	app := newApp(t, config.Map{"MAIL_FROM_ADDRESS": "shop@example.com"}, &logs) // MAIL_DRIVER defaults to log
	_, err := mailer.ForApp(app)
	check(t, err)
	check(t, mailer.Send(app.Context(context.Background()), &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}},
		Bcc: []mailer.Address{{Address: "b@example.com"}}, Subject: "Logged", HTML: html("<p>Your code is 1234</p>"),
		Attachments: []mailer.Attachment{{Filename: "x.txt", Data: []byte("x")}}}))
	for _, want := range []string{"not sent", "Logged", "a@example.com", "bcc=<b@example.com>", "Your code is 1234", "attachments=x.txt"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs lack %q:\n%s", want, logs.String())
		}
	}
	// In production, the log driver is a warning, and bodies stay out of
	// the log.
	logs.Reset()
	app = newApp(t, config.Map{"APP_ENV": "production", "MAIL_FROM_ADDRESS": "shop@example.com"}, &logs)
	_, err = mailer.ForApp(app)
	check(t, err)
	check(t, mailer.Send(app.Context(context.Background()), &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}},
		Subject: "Reset", HTML: html("<p>https://example.com/reset?token=secret</p>")}))
	if !strings.Contains(logs.String(), "MAIL_DRIVER is log in production") || !strings.Contains(logs.String(), "Reset") ||
		strings.Contains(logs.String(), "token=secret") {
		t.Errorf("logs:\n%s", logs.String())
	}
}

func TestPreview(t *testing.T) {
	m, _ := newMailer()
	h := mailer.Preview(func(r *http.Request) mailer.Mailable { return welcome{r.URL.Query().Get("name"), "a@example.com"} })
	serve := func(ctx context.Context, target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil).WithContext(ctx))
		return w
	}
	w := serve(mailer.WithMailer(context.Background(), m), "/?name=Ada")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<h1>Hi Ada</h1>") || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("preview: %d %q", w.Code, w.Body.String())
	}
	if w := serve(context.Background(), "/"); w.Code != http.StatusInternalServerError {
		t.Errorf("without a mailer: %d", w.Code)
	}
	text := mailer.Preview(func(*http.Request) mailer.Mailable {
		return &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "S", Text: "plain <b>"}
	})
	w = httptest.NewRecorder()
	text.ServeHTTP(w, httptest.NewRequest("GET", "/", nil).WithContext(mailer.WithMailer(context.Background(), m)))
	if w.Body.String() != "plain <b>" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("text preview: %q", w.Body.String())
	}
}

func TestLogger(t *testing.T) {
	var logs bytes.Buffer
	m := mailer.New(mailer.NewMemoryTransport(), mailer.DefaultFrom(mailer.Address{Address: "shop@example.com"}),
		mailer.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	check(t, m.Send(context.Background(), &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "S", Text: "x"}))
	if !strings.Contains(logs.String(), "mail sent") {
		t.Errorf("logs: %s", logs.String())
	}
}
