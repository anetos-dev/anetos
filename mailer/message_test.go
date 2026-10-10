// SPDX-License-Identifier: Apache-2.0

package mailer_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"reflect"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/view"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func html(s string) view.Component {
	return view.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	})
}

var date = time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)

func newMailer() (*mailer.Mailer, *mailer.MemoryTransport) {
	mem := mailer.NewMemoryTransport()
	m := mailer.NewWithTransport(mem, mailer.DefaultFrom(mailer.Address{Name: "Shop", Address: "shop@example.com"}), mailer.BaseURL("https://shop.example.com/"))
	mailer.SetNow(m, func() time.Time { return date })
	return m, mem
}

// welcome is a mailable of a struct.
type welcome struct{ Name, Email string }

func (w welcome) Build(ctx context.Context) (*mailer.Message, error) {
	link, err := mailer.URL(ctx, "/start")
	if err != nil {
		return nil, err
	}
	return &mailer.Message{
		To:      []mailer.Address{{Name: w.Name, Address: w.Email}},
		Subject: "Welcome, " + w.Name,
		HTML:    html(`<h1>Hi ` + w.Name + `</h1><p>Start <a href="` + link + `">here</a>.</p>`),
	}, nil
}

func TestRender(t *testing.T) {
	m, _ := newMailer()
	ctx := mailer.WithMailer(context.Background(), m)
	o, err := m.Render(ctx, welcome{"Zoë", "zoe@example.com"})
	check(t, err)
	if o.From.String() != `"Shop" <shop@example.com>` || o.Subject != "Welcome, Zoë" || !o.Date.Equal(date) {
		t.Errorf("rendered %+v", o)
	}
	if want := "Hi Zoë\n\nStart here (https://shop.example.com/start)."; o.Text != want {
		t.Errorf("text = %q, want %q", o.Text, want)
	}
	if !strings.HasSuffix(o.MessageID, "@example.com") || len(o.MessageID) != 32+len("@example.com") {
		t.Errorf("message ID %q", o.MessageID)
	}
	o2, err := m.Render(ctx, welcome{"Zoë", "zoe@example.com"})
	check(t, err)
	if o2.MessageID == o.MessageID {
		t.Error("two renders have one message ID")
	}

	// A text body of its own, an attachment's type from its name.
	o, err = m.Render(ctx, &mailer.Message{
		From: mailer.Address{Address: "billing@example.com"}, To: []mailer.Address{{Address: "a@example.com"}},
		Subject: "Invoice", HTML: html("<p>Hi</p>"), Text: "Hi, plain",
		Attachments: []mailer.Attachment{{Filename: "invoice.pdf", Data: []byte("%PDF")}, {Filename: "data.unknownext", Data: []byte{1}}},
	})
	check(t, err)
	if o.Text != "Hi, plain" || o.From.Address != "billing@example.com" ||
		o.Attachments[0].ContentType != "application/pdf" || o.Attachments[1].ContentType != "application/octet-stream" {
		t.Errorf("rendered %+v", o)
	}

	// It is JSON, for queued mail.
	b, err := json.Marshal(o)
	check(t, err)
	var back mailer.Outgoing
	check(t, json.Unmarshal(b, &back))
	if !reflect.DeepEqual(&back, o) {
		t.Errorf("JSON round trip:\n%+v\n%+v", back, *o)
	}
}

func TestRenderErrors(t *testing.T) {
	m, _ := newMailer()
	ctx := mailer.WithMailer(context.Background(), m)
	to := []mailer.Address{{Address: "a@example.com"}}
	body := html("<p>x</p>")
	failing := view.ComponentFunc(func(context.Context, io.Writer) error { return errors.New("template broke") })
	for name, msg := range map[string]*mailer.Message{
		"no recipient":         {Subject: "x", HTML: body},
		"no body":              {To: to, Subject: "x"},
		"bad address":          {To: []mailer.Address{{Address: "not an address"}}, Subject: "x", HTML: body},
		"address with a name":  {To: []mailer.Address{{Address: "Bob <b@example.com>"}}, Subject: "x", HTML: body},
		"newline in name":      {To: []mailer.Address{{Name: "a\r\nBcc: x@evil.com", Address: "a@example.com"}}, Subject: "x", HTML: body},
		"newline in subject":   {To: to, Subject: "x\r\nBcc: x@evil.com", HTML: body},
		"reserved header":      {To: to, Subject: "x", HTML: body, Headers: map[string]string{"Bcc": "x@evil.com"}},
		"bad header name":      {To: to, Subject: "x", HTML: body, Headers: map[string]string{"X Bad": "1"}},
		"newline in header":    {To: to, Subject: "x", HTML: body, Headers: map[string]string{"X-A": "1\nBcc: x"}},
		"bad metadata key":     {To: to, Subject: "x", HTML: body, Metadata: map[string]string{"a b": "1"}},
		"bad attachment name":  {To: to, Subject: "x", HTML: body, Attachments: []mailer.Attachment{{Filename: "../x", Data: []byte{1}}}},
		"bad content type":     {To: to, Subject: "x", HTML: body, Attachments: []mailer.Attachment{{Filename: "x", ContentType: "a b", Data: []byte{1}}}},
		"failing HTML":         {To: to, Subject: "x", HTML: failing},
		"bad reply-to address": {To: to, ReplyTo: []mailer.Address{{Address: "x"}}, Subject: "x", HTML: body},
		"long name":            {To: []mailer.Address{{Name: strings.Repeat("a", 301), Address: "a@example.com"}}, Subject: "x", HTML: body},
		"long word":            {To: to, Subject: strings.Repeat("a", 901), HTML: body},
		"long filename":        {To: to, Subject: "x", HTML: body, Attachments: []mailer.Attachment{{Filename: strings.Repeat("é", 128), Data: []byte{1}}}},
		"resent header":        {To: to, Subject: "x", HTML: body, Headers: map[string]string{"Resent-From": "x@example.com"}},
		"headers twice":        {To: to, Subject: "x", HTML: body, Headers: map[string]string{"X-A": "1", "x-a": "2"}},
		"long metadata key":    {To: to, Subject: "x", HTML: body, Metadata: map[string]string{strings.Repeat("k", 61): "1"}},
		"quoted local part":    {To: []mailer.Address{{Address: `"a b"@example.com`}}, Subject: "x", HTML: body},
		"long local part":      {To: []mailer.Address{{Address: strings.Repeat("a", 65) + "@example.com"}}, Subject: "x", HTML: body},
		"long address":         {To: []mailer.Address{{Address: "a@" + strings.Repeat("b", 250) + ".com"}}, Subject: "x", HTML: body},
		"long content type":    {To: to, Subject: "x", HTML: body, Attachments: []mailer.Attachment{{Filename: "x", ContentType: "text/plain; x-a=" + strings.Repeat("a", 200), Data: []byte{1}}}},
	} {
		if _, err := m.Render(ctx, msg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := m.Render(ctx, nil); err == nil {
		t.Error("nil mailable: no error")
	}
	noFrom := mailer.NewWithTransport(mailer.NewMemoryTransport())
	if _, err := noFrom.Render(ctx, &mailer.Message{To: to, Subject: "x", HTML: body}); err == nil || !strings.Contains(err.Error(), "MAIL_FROM_ADDRESS") {
		t.Errorf("no sender: %v", err)
	}
	// URL without APP_URL, or with a path that isn't absolute.
	if _, err := mailer.URL(mailer.WithMailer(ctx, noFrom), "/x"); err == nil {
		t.Error("URL without APP_URL: no error")
	}
	for _, p := range []string{"x", "//evil.com/x", ""} {
		if _, err := mailer.URL(ctx, p); err == nil {
			t.Errorf("URL(%q): no error", p)
		}
	}
	if _, err := mailer.URL(context.Background(), "/x"); !errors.Is(err, mailer.ErrNoMailer) {
		t.Errorf("URL without a mailer = %v", err)
	}
}

func TestMIME(t *testing.T) {
	m, _ := newMailer()
	ctx := mailer.WithMailer(context.Background(), m)
	long := strings.Repeat("A long line of text. ", 30)
	o, err := m.Render(ctx, &mailer.Message{
		To:       []mailer.Address{{Name: "Zoë Ünal", Address: "zoe@example.com"}, {Address: "b@example.com"}},
		Cc:       []mailer.Address{{Address: "c@example.com"}},
		Bcc:      []mailer.Address{{Address: "secret@example.com"}},
		ReplyTo:  []mailer.Address{{Address: "help@example.com"}},
		Subject:  "Votre reçu — commande n° 42, merci beaucoup pour votre confiance renouvelée",
		HTML:     html(`<p>` + long + `</p><img src="cid:logo">`),
		Headers:  map[string]string{"list-unsubscribe": "<https://shop.example.com/unsub>"},
		Metadata: map[string]string{"order": "42"},
		Attachments: []mailer.Attachment{
			{Filename: "logo.png", Data: []byte("PNG..."), ContentID: "logo"},
			{Filename: "reçu.pdf", Data: bytes.Repeat([]byte{0xff, 0x00}, 100)},
		},
	})
	check(t, err)
	raw := o.MIME()
	for i, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Errorf("line %d has %d characters", i, len(line))
		}
		if strings.ContainsAny(line, "\r\n") {
			t.Errorf("line %d has a bare line break", i)
		}
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	check(t, err)
	h := msg.Header
	var dec mime.WordDecoder
	subject, err := dec.DecodeHeader(h.Get("Subject"))
	check(t, err)
	to, err := h.AddressList("To")
	check(t, err)
	if subject != o.Subject || len(to) != 2 || to[0].Name != "Zoë Ünal" || h.Get("Bcc") != "" || h.Get("Cc") != "<c@example.com>" ||
		h.Get("Reply-To") != "<help@example.com>" || h.Get("Message-Id") != "<"+o.MessageID+">" ||
		h.Get("List-Unsubscribe") != "<https://shop.example.com/unsub>" || h.Get("X-Metadata-Order") != "42" ||
		h.Get("Date") != "Thu, 01 Oct 2026 09:30:00 +0000" {
		t.Errorf("headers: %v (subject %q)", h, subject)
	}
	if bytes.Contains(raw, []byte("secret@example.com")) {
		t.Error("the Bcc recipient is in the message")
	}

	// mixed(related(alternative(text, html), logo), receipt)
	mt, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	check(t, err)
	if mt != "multipart/mixed" {
		t.Fatalf("Content-Type %s", mt)
	}
	parts := readParts(t, msg.Body, params["boundary"])
	if len(parts) != 2 {
		t.Fatalf("%d parts", len(parts))
	}
	att := parts[1]
	if att.header.Get("Content-Disposition") != `attachment; filename*=utf-8''re%C3%A7u.pdf` {
		t.Errorf("attachment header %v", att.header)
	}
	if data, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(att.body), "\r\n", "")); !bytes.Equal(data, o.Attachments[1].Data) {
		t.Error("the attachment's data changed")
	}
	_, params, _ = mime.ParseMediaType(parts[0].header.Get("Content-Type"))
	related := readParts(t, bytes.NewReader(parts[0].body), params["boundary"])
	if len(related) != 2 || related[1].header.Get("Content-Id") != "<logo>" || !strings.HasPrefix(related[1].header.Get("Content-Disposition"), "inline") {
		t.Fatalf("related parts: %v", related)
	}
	_, params, _ = mime.ParseMediaType(related[0].header.Get("Content-Type"))
	alt := readParts(t, bytes.NewReader(related[0].body), params["boundary"])
	if len(alt) != 2 || alt[0].header.Get("Content-Type") != "text/plain; charset=utf-8" || alt[1].header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("alternative parts: %v", alt)
	}
	text, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(alt[0].body)))
	check(t, err)
	if strings.ReplaceAll(string(text), "\r\n", "\n") != o.Text {
		t.Errorf("text body %q, want %q", text, o.Text)
	}

	// Long values are folded, encoded words kept short: no line over 78
	// characters but the base64 and quoted-printable bodies' own.
	o, err = m.Render(ctx, &mailer.Message{
		From:        mailer.Address{Name: strings.Repeat("Zoë ", 60), Address: "zoe@example.com"},
		To:          []mailer.Address{{Name: strings.Repeat("Bob ", 70), Address: "b@example.com"}},
		Subject:     strings.Repeat("A long subject line ", 60),
		Headers:     map[string]string{"X-Literal": "=?utf-8?q?not_encoded?=", "X-Long": strings.Repeat("value ", 200)},
		HTML:        html("<p>x</p>"),
		Attachments: []mailer.Attachment{{Filename: strings.Repeat("é", 125) + ".pdf", Data: []byte{1}}},
	})
	check(t, err)
	raw = o.MIME()
	for i, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Errorf("line %d has %d characters", i, len(line))
		}
	}
	msg, err = mail.ReadMessage(bytes.NewReader(raw))
	check(t, err)
	from, err := msg.Header.AddressList("From")
	check(t, err)
	subject, err = dec.DecodeHeader(msg.Header.Get("Subject"))
	check(t, err)
	literal, err := dec.DecodeHeader(msg.Header.Get("X-Literal"))
	check(t, err)
	// (Header parsing drops trailing spaces.)
	if from[0].Name != o.From.Name || subject != strings.TrimSpace(o.Subject) || literal != "=?utf-8?q?not_encoded?=" ||
		msg.Header.Get("X-Long") != strings.TrimSpace(strings.Repeat("value ", 200)) {
		t.Errorf("headers: from %q, subject %q, literal %q, long %q", from[0].Name, subject, literal, msg.Header.Get("X-Long"))
	}

	// A long word is fine when the value is encoded anyway.
	_, err = m.Render(ctx, &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "ü " + strings.Repeat("x", 2000), Text: "x"})
	check(t, err)

	// A text-only message is one part.
	o, err = m.Render(ctx, &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "Plain", Text: "Just text"})
	check(t, err)
	msg, err = mail.ReadMessage(bytes.NewReader(o.MIME()))
	check(t, err)
	if msg.Header.Get("Content-Type") != "text/plain; charset=utf-8" || msg.Header.Get("Subject") != "Plain" {
		t.Errorf("headers: %v", msg.Header)
	}
}

func readParts(t *testing.T, r io.Reader, boundary string) []struct {
	header interface{ Get(string) string }
	body   []byte
} {
	t.Helper()
	var out []struct {
		header interface{ Get(string) string }
		body   []byte
	}
	mr := multipart.NewReader(r, boundary)
	for {
		p, err := mr.NextRawPart()
		if errors.Is(err, io.EOF) {
			return out
		}
		check(t, err)
		b, err := io.ReadAll(p)
		check(t, err)
		out = append(out, struct {
			header interface{ Get(string) string }
			body   []byte
		}{p.Header, b})
	}
}
