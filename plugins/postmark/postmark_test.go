// SPDX-License-Identifier: Apache-2.0

package postmark_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/plugins/postmark"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/view"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// api is a fake Postmark API answering status and body.
func api(t *testing.T, status int, body string) (*httptest.Server, *map[string]any, *http.Header) {
	t.Helper()
	var got map[string]any
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/email" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		headers = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		check(t, json.Unmarshal(b, &got))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got, &headers
}

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "APP_NAME": "Shop", "MAIL_DRIVER": "postmark", "MAIL_FROM_ADDRESS": "shop@example.com"}
	maps.Copy(src, env)
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func text(s string) view.Component {
	return view.ComponentFunc(func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, s); return err })
}

func TestSend(t *testing.T) {
	srv, got, headers := api(t, 200, `{"To":"a@example.com","SubmittedAt":"2026-10-01T09:30:00Z","MessageID":"b7bc2f4a","ErrorCode":0,"Message":"OK"}`)
	app := newApp(t, config.Map{"MAIL_POSTMARK_TOKEN": "server-token", "MAIL_POSTMARK_STREAM": "receipts"})
	_, err := mailer.ForApp(app, postmark.Driver(postmark.BaseURL(srv.URL)))
	check(t, err)
	err = mailer.Send(app.Context(context.Background()), &mailer.Message{
		To:          []mailer.Address{{Name: "Zoë", Address: "zoe@example.com"}, {Address: "b@example.com"}},
		Bcc:         []mailer.Address{{Address: "audit@example.com"}},
		ReplyTo:     []mailer.Address{{Address: "help@example.com"}},
		Subject:     "Your receipt",
		HTML:        text(`<p>Thanks</p><img src="cid:logo">`),
		Headers:     map[string]string{"List-Unsubscribe": "<https://shop.example.com/u>"},
		Tag:         "receipt",
		Metadata:    map[string]string{"order": "42"},
		Attachments: []mailer.Attachment{{Filename: "logo.png", Data: []byte("PNG"), ContentID: "logo"}, {Filename: "r.pdf", Data: []byte("%PDF")}},
	})
	check(t, err)
	if headers.Get("X-Postmark-Server-Token") != "server-token" || headers.Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", *headers)
	}
	g := *got
	want := map[string]any{"From": `"Shop" <shop@example.com>`, "To": `=?utf-8?q?Zo=C3=AB?= <zoe@example.com>, <b@example.com>`,
		"Bcc": "<audit@example.com>", "ReplyTo": "<help@example.com>", "Subject": "Your receipt", "Tag": "receipt",
		"HtmlBody": `<p>Thanks</p><img src="cid:logo">`, "TextBody": "Thanks", "MessageStream": "receipts"}
	for k, v := range want {
		if g[k] != v {
			t.Errorf("%s = %#v, want %#v", k, g[k], v)
		}
	}
	if _, ok := g["Cc"]; ok {
		t.Error("an empty Cc was sent")
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	check(t, enc.Encode(map[string]any{"Headers": g["Headers"], "Metadata": g["Metadata"], "Attachments": g["Attachments"]}))
	if b := strings.TrimSpace(buf.String()); b != `{"Attachments":[{"Content":"UE5H","ContentID":"cid:logo","ContentType":"image/png","Name":"logo.png"},{"Content":"JVBERg==","ContentType":"application/pdf","Name":"r.pdf"}],"Headers":[{"Name":"List-Unsubscribe","Value":"<https://shop.example.com/u>"}],"Metadata":{"order":"42"}}` {
		t.Errorf("got %s", b)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	m := mailer.New(mailer.NewMemoryTransport(), mailer.DefaultFrom(mailer.Address{Address: "shop@example.com"}))
	o, err := m.Render(ctx, &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "S", Text: "x"})
	check(t, err)
	for _, tt := range []struct {
		status    int
		body      string
		permanent bool
		code      int
	}{
		{422, `{"ErrorCode":300,"Message":"Invalid 'To' address: 'x'."}`, true, 300},
		{422, `{"ErrorCode":406,"Message":"You tried to send to recipient(s) that have been marked as inactive."}`, true, 406},
		{422, `{"ErrorCode":405,"Message":"Not allowed to send: you have run out of credits."}`, false, 405},
		{401, `{"ErrorCode":10,"Message":"No Account or Server API tokens were supplied in the HTTP headers."}`, true, 10},
		{429, `{"ErrorCode":0,"Message":"Rate limit exceeded"}`, false, 0},
		{500, `Internal Server Error`, false, 0},
		{200, `{"ErrorCode":402,"Message":"Invalid JSON"}`, false, 402},
	} {
		srv, _, _ := api(t, tt.status, tt.body)
		err := postmark.NewTransport("t", postmark.BaseURL(srv.URL)).Send(ctx, o)
		var pe *postmark.Error
		if !errors.As(err, &pe) || pe.Status != tt.status || pe.Code != tt.code || queue.IsPermanent(err) != tt.permanent {
			t.Errorf("%d %s: err = %v (permanent %v)", tt.status, tt.body, err, queue.IsPermanent(err))
		}
	}
	// Too many recipients.
	many := *o
	for range 50 {
		many.Bcc = append(many.Bcc, mailer.Address{Address: "x@example.com"})
	}
	if err := postmark.NewTransport("t").Send(ctx, &many); err == nil || !queue.IsPermanent(err) {
		t.Errorf("51 recipients: %v", err)
	}
	// Unreachable.
	srv, _, _ := api(t, 200, "{}")
	srv.Close()
	if err := postmark.NewTransport("t", postmark.BaseURL(srv.URL), postmark.HTTPClient(http.DefaultClient)).Send(ctx, o); err == nil || queue.IsPermanent(err) {
		t.Errorf("unreachable: %v", err)
	}
	// The token is required.
	if _, err := mailer.ForApp(newApp(t, nil), postmark.Driver()); err == nil || !strings.Contains(err.Error(), "MAIL_POSTMARK_TOKEN") {
		t.Errorf("ForApp without a token = %v", err)
	}
}
