// SPDX-License-Identifier: Apache-2.0

package mailer_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"
)

// fakeSMTP is an SMTP server for tests.
type fakeSMTP struct {
	t        *testing.T
	ln       net.Listener
	cert     tls.Certificate
	pool     *x509.CertPool
	implicit bool   // TLS from the start
	starttls bool   // offers STARTTLS
	mechs    string // AUTH mechanisms offered ("" for none)
	user     string // accepted credentials
	pass     string
	reject   string // recipient answered 550
	silent   bool   // never greets
	host     string // listens on host (default 127.0.0.1)
	utf8     bool   // offers SMTPUTF8
	code     int    // the reply to reject (default 550)
	dropAuth bool   // closes the connection on AUTH
	mu       sync.Mutex
	got      []received
}

type received struct {
	from   string
	rcpt   []string
	data   string
	tls    bool
	authed string
}

func newFakeSMTP(t *testing.T, f *fakeSMTP) *fakeSMTP {
	t.Helper()
	f.t = t
	f.cert, f.pool = selfSigned(t)
	if f.host == "" {
		f.host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", f.host+":0")
	check(t, err)
	if f.implicit {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{f.cert}})
	}
	f.ln = ln
	var wg sync.WaitGroup
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() { f.serve(conn) })
		}
	})
	return f
}

func (f *fakeSMTP) url(scheme, userinfo, query string) string {
	if userinfo != "" {
		userinfo += "@"
	}
	if query != "" {
		query = "?" + query
	}
	return scheme + "://" + userinfo + f.ln.Addr().String() + query
}

func (f *fakeSMTP) messages() []received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]received(nil), f.got...)
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if f.silent {
		_, _ = bufio.NewReader(conn).ReadString('\n')
		return
	}
	tp := textproto.NewConn(conn)
	r := received{tls: f.implicit}
	reply := func(s string) { _ = tp.PrintfLine("%s", s) }
	reply("220 fake ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			lines := []string{"fake"}
			if f.starttls && !r.tls {
				lines = append(lines, "STARTTLS")
			}
			if f.mechs != "" {
				lines = append(lines, "AUTH "+f.mechs)
			}
			if f.utf8 {
				lines = append(lines, "SMTPUTF8")
			}
			lines = append(lines, "8BITMIME")
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				reply("250" + sep + l)
			}
		case "STARTTLS":
			reply("220 go ahead")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{f.cert}})
			if tc.Handshake() != nil {
				return
			}
			conn, tp, r.tls = tc, textproto.NewConn(tc), true
			reply = func(s string) { _ = tp.PrintfLine("%s", s) }
		case "AUTH":
			if f.dropAuth {
				return
			}
			mech, initial, _ := strings.Cut(arg, " ")
			var user, pass string
			switch mech {
			case "PLAIN":
				b, _ := base64.StdEncoding.DecodeString(initial)
				parts := strings.Split(string(b), "\x00")
				if len(parts) == 3 {
					user, pass = parts[1], parts[2]
				}
			case "LOGIN":
				reply("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
				l, _ := tp.ReadLine()
				b, _ := base64.StdEncoding.DecodeString(l)
				user = string(b)
				reply("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
				l, _ = tp.ReadLine()
				b, _ = base64.StdEncoding.DecodeString(l)
				pass = string(b)
			}
			if user == f.user && pass == f.pass {
				r.authed = mech + " " + user
				reply("235 ok")
			} else {
				reply("535 bad credentials")
			}
		case "MAIL":
			r.from = strings.TrimSuffix(strings.TrimPrefix(strings.Fields(arg)[0], "FROM:<"), ">")
			reply("250 ok")
		case "RCPT":
			to := strings.TrimSuffix(strings.TrimPrefix(arg, "TO:<"), ">")
			if to == f.reject {
				if f.code == 0 {
					f.code = 550
				}
				reply(fmt.Sprintf("%d no", f.code))
				continue
			}
			r.rcpt = append(r.rcpt, to)
			reply("250 ok")
		case "DATA":
			reply("354 go ahead")
			b, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			r.data = string(b)
			f.mu.Lock()
			f.got = append(f.got, r)
			f.mu.Unlock()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

// selfSigned returns a certificate for 127.0.0.1 and a pool trusting it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	check(t, err)
	cert, err := x509.ParseCertificate(der)
	check(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func outgoing(t *testing.T) *mailer.Outgoing {
	t.Helper()
	m, _ := newMailer()
	o, err := m.Render(context.Background(), &mailer.Message{
		To: []mailer.Address{{Address: "a@example.com"}}, Bcc: []mailer.Address{{Address: "b@example.com"}},
		Subject: "Hello", HTML: html("<p>Hi</p>")})
	check(t, err)
	return o
}

func TestSMTP(t *testing.T) {
	ctx := context.Background()
	o := outgoing(t)
	for _, tt := range []struct {
		name       string
		server     *fakeSMTP
		scheme     string
		userinfo   string
		query      string
		requireTLS bool
		wantTLS    bool
		wantAuth   string
	}{
		{name: "plain local", server: &fakeSMTP{}, scheme: "smtp"},
		{name: "starttls", server: &fakeSMTP{starttls: true}, scheme: "smtp", wantTLS: true},
		{name: "starttls required", server: &fakeSMTP{starttls: true, mechs: "PLAIN LOGIN", user: "u@x", pass: "p:w/d"}, scheme: "smtp",
			userinfo: "u%40x:p%3Aw%2Fd", requireTLS: true, wantTLS: true, wantAuth: "PLAIN u@x"},
		{name: "tls=none", server: &fakeSMTP{starttls: true}, scheme: "smtp", query: "tls=none"},
		{name: "smtps login", server: &fakeSMTP{implicit: true, mechs: "LOGIN", user: "u", pass: "p"}, scheme: "smtps", userinfo: "u:p", wantTLS: true, wantAuth: "LOGIN u"},
		{name: "plain auth to a local host", server: &fakeSMTP{mechs: "PLAIN", user: "u", pass: "p"}, scheme: "smtp", userinfo: "u:p", wantAuth: "PLAIN u"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSMTP(t, tt.server)
			tr, err := mailer.NewSMTPTransport(f.url(tt.scheme, tt.userinfo, tt.query))
			check(t, err)
			mailer.SetRootCAs(tr, f.pool)
			if tt.requireTLS {
				mailer.RequireSTARTTLS(tr)
			}
			check(t, tr.Send(ctx, o))
			got := f.messages()
			if len(got) != 1 {
				t.Fatalf("%d messages", len(got))
			}
			r := got[0]
			if r.from != "shop@example.com" || strings.Join(r.rcpt, ",") != "a@example.com,b@example.com" || r.tls != tt.wantTLS || r.authed != tt.wantAuth {
				t.Errorf("received %+v", r)
			}
			if !strings.Contains(r.data, "Subject: Hello\n") || strings.Contains(r.data, "b@example.com") {
				t.Errorf("data:\n%s", r.data)
			}
		})
	}
}

func TestSMTPErrors(t *testing.T) {
	ctx := context.Background()
	o := outgoing(t)
	send := func(t *testing.T, f *fakeSMTP, rawURL string, setup ...func(*mailer.SMTPTransport)) error {
		t.Helper()
		tr, err := mailer.NewSMTPTransport(rawURL)
		check(t, err)
		mailer.SetRootCAs(tr, f.pool)
		for _, s := range setup {
			s(tr)
		}
		return tr.Send(ctx, o)
	}
	t.Run("rejected recipient", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{reject: "b@example.com"})
		err := send(t, f, f.url("smtp", "", ""))
		if err == nil || !queue.IsPermanent(err) || !strings.Contains(err.Error(), "550") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("bad credentials", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{starttls: true, mechs: "PLAIN", user: "u", pass: "p"})
		if err := send(t, f, f.url("smtp", "u:wrong", "")); err == nil || !queue.IsPermanent(err) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("STARTTLS missing", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{})
		if err := send(t, f, f.url("smtp", "", ""), mailer.RequireSTARTTLS); err == nil || !queue.IsPermanent(err) || !strings.Contains(err.Error(), "STARTTLS") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("untrusted certificate", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{starttls: true})
		if err := send(t, f, f.url("smtp", "", ""), func(tr *mailer.SMTPTransport) { mailer.SetRootCAs(tr, x509.NewCertPool()) }); err == nil {
			t.Error("no error")
		}
	})
	t.Run("password without TLS to a remote host", func(t *testing.T) {
		// To a host that isn't local (net/smtp counts only 127.0.0.1,
		// ::1 and localhost), STARTTLS is required, and tls=none with a
		// password is refused.
		f := newFakeSMTP(t, &fakeSMTP{host: "127.0.0.2", mechs: "PLAIN", user: "u", pass: "p"})
		if _, err := mailer.NewSMTPTransport(f.url("smtp", "u:p", "tls=none")); err == nil {
			t.Error("tls=none with a password to a remote host: no error")
		}
		err := send(t, f, f.url("smtp", "u:p", ""))
		if err == nil || !queue.IsPermanent(err) || !strings.Contains(err.Error(), "STARTTLS") || len(f.messages()) > 0 {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("full mailbox", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{reject: "b@example.com", code: 552})
		if err := send(t, f, f.url("smtp", "", "")); err == nil || queue.IsPermanent(err) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("UTF-8 address", func(t *testing.T) {
		m, _ := newMailer()
		u, err := m.Render(ctx, &mailer.Message{To: []mailer.Address{{Address: "zoë@example.com"}}, Subject: "S", Text: "x"})
		check(t, err)
		f := newFakeSMTP(t, &fakeSMTP{})
		tr, err := mailer.NewSMTPTransport(f.url("smtp", "", ""))
		check(t, err)
		if err := tr.Send(ctx, u); err == nil || !queue.IsPermanent(err) || !strings.Contains(err.Error(), "SMTPUTF8") {
			t.Errorf("without SMTPUTF8: %v", err)
		}
		f = newFakeSMTP(t, &fakeSMTP{utf8: true})
		tr, err = mailer.NewSMTPTransport(f.url("smtp", "", ""))
		check(t, err)
		check(t, tr.Send(ctx, u))
		if got := f.messages(); len(got) != 1 || got[0].rcpt[0] != "zoë@example.com" {
			t.Errorf("received %+v", got)
		}
	})
	t.Run("invalid message", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{})
		bad := *o
		bad.Headers = map[string]string{"X-A\r\nBcc": "x"}
		tr, err := mailer.NewSMTPTransport(f.url("smtp", "", ""))
		check(t, err)
		if err := tr.Send(ctx, &bad); err == nil || !queue.IsPermanent(err) || len(f.messages()) > 0 {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("connection dropped during AUTH", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{mechs: "PLAIN", dropAuth: true})
		if err := send(t, f, f.url("smtp", "u:p", "")); err == nil || queue.IsPermanent(err) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("UTF-8 Reply-To", func(t *testing.T) {
		m, _ := newMailer()
		u, err := m.Render(ctx, &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, ReplyTo: []mailer.Address{{Address: "zoë@example.com"}}, Subject: "S", Text: "x"})
		check(t, err)
		f := newFakeSMTP(t, &fakeSMTP{})
		tr, err := mailer.NewSMTPTransport(f.url("smtp", "", ""))
		check(t, err)
		if err := tr.Send(ctx, u); err == nil || !queue.IsPermanent(err) || len(f.messages()) > 0 {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no AUTH offered", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{})
		if err := send(t, f, f.url("smtp", "u:p", "")); err == nil || !queue.IsPermanent(err) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{silent: true})
		start := time.Now()
		err := send(t, f, f.url("smtp", "", "timeout=200ms"))
		if err == nil || queue.IsPermanent(err) || time.Since(start) > 5*time.Second {
			t.Errorf("err = %v after %s", err, time.Since(start))
		}
	})
	t.Run("canceled", func(t *testing.T) {
		f := newFakeSMTP(t, &fakeSMTP{silent: true})
		tr, err := mailer.NewSMTPTransport(f.url("smtp", "", ""))
		check(t, err)
		ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		if err := tr.Send(ctx, o); err == nil || !strings.Contains(err.Error(), "deadline") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		check(t, err)
		addr := ln.Addr().String()
		_ = ln.Close()
		tr, err := mailer.NewSMTPTransport("smtp://" + addr)
		check(t, err)
		if err := tr.Send(ctx, o); err == nil || queue.IsPermanent(err) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestSMTPURL(t *testing.T) {
	for _, u := range []string{"http://x", "smtp://", "smtp://host/path", "smtp://host?tls=yes", "smtps://host?tls=none",
		"smtp://host?timeout=-1s", "smtp://host?timeout=x", "smtp://host?bogus=1", "smtp://host?local_name=a%20b", "::",
		"smtp://u:p@relay.internal?tls=none"} {
		if _, err := mailer.NewSMTPTransport(u); err == nil {
			t.Errorf("%s: no error", u)
		}
	}
	// The password isn't in the error.
	if _, err := mailer.NewSMTPTransport("smtp://user:s3cret@host:58x7"); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("err = %v", err)
	}
	for _, u := range []string{"smtp://localhost", "smtps://user:pass@smtp.example.com", "smtp://[::1]:1025?timeout=5s&local_name=app.example.com"} {
		if _, err := mailer.NewSMTPTransport(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}
