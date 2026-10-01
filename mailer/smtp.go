// SPDX-License-Identifier: Apache-2.0

package mailer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"anetos.dev/anetos/queue"
)

// SMTPTransport sends emails to an SMTP server, one connection per email.
type SMTPTransport struct {
	host, addr string
	implicit   bool   // smtps: TLS from the start
	starttls   string // required, optional or none
	user, pass string
	auth       bool
	localName  string
	timeout    time.Duration
	rootCAs    *x509.CertPool // nil: the system's
}

// NewSMTPTransport returns a transport for the server of rawURL:
//
//   - smtp://user:password@smtp.example.com:587 upgrades the connection
//     with STARTTLS, which the server must offer, unless the host is
//     local (127.0.0.1, ::1, localhost: Mailpit, a relay on the
//     machine), where it is used if offered. Default port 587.
//   - smtps://user:password@smtp.example.com:465 uses TLS from the start.
//     Default port 465.
//
// The user and password, URL-encoded, are sent with AUTH PLAIN (or LOGIN
// if the server offers only that), only over TLS or to a local host.
// Query parameters: tls=none never upgrades (a relay on a private
// network that has no TLS, which can't take a password); timeout=30s
// bounds each email (default 30s); local_name=host is the name sent in
// EHLO (default: the machine's).
func NewSMTPTransport(rawURL string) (*SMTPTransport, error) {
	u, err := url.Parse(rawURL)
	if err != nil { // its message would show the password
		return nil, errors.New("invalid URL: use smtp://user:password@host:port, the user and password URL-encoded")
	}
	t := &SMTPTransport{host: u.Hostname(), timeout: 30 * time.Second}
	port := u.Port()
	switch u.Scheme {
	case "smtp":
		t.starttls = "required"
		if isLocal(t.host) {
			t.starttls = "optional"
		}
		if port == "" {
			port = "587"
		}
	case "smtps":
		t.implicit = true
		if port == "" {
			port = "465"
		}
	default:
		return nil, fmt.Errorf("the URL's scheme must be smtp or smtps, not %q", u.Scheme)
	}
	if t.host == "" || (u.Path != "" && u.Path != "/") || u.Fragment != "" {
		return nil, fmt.Errorf("%s://user:password@host:port has no host, or has a path", u.Scheme)
	}
	t.addr = net.JoinHostPort(t.host, port)
	if u.User != nil {
		t.auth = true
		t.user = u.User.Username()
		t.pass, _ = u.User.Password()
	}
	q := u.Query()
	for k := range q {
		if !slices.Contains([]string{"tls", "timeout", "local_name"}, k) {
			return nil, fmt.Errorf("unknown parameter %q (tls, timeout, local_name)", k)
		}
	}
	switch v := q.Get("tls"); v {
	case "":
	case "none":
		if t.implicit {
			return nil, errors.New("tls=none with smtps")
		}
		if t.auth && !isLocal(t.host) {
			return nil, errors.New("tls=none with a user and password: they would be sent in the clear (use TLS)")
		}
		t.starttls = "none"
	default:
		return nil, fmt.Errorf("tls=%q: the only value is none", v)
	}
	if v := q.Get("timeout"); v != "" {
		if t.timeout, err = time.ParseDuration(v); err != nil || t.timeout <= 0 {
			return nil, fmt.Errorf("timeout=%q must be a positive duration", v)
		}
	}
	t.localName = q.Get("local_name")
	if t.localName == "" {
		if t.localName, err = os.Hostname(); err != nil || t.localName == "" {
			t.localName = "localhost"
		}
	}
	if strings.ContainsAny(t.localName, " \r\n") {
		return nil, fmt.Errorf("invalid local_name %q", t.localName)
	}
	return t, nil
}

// isLocal reports whether host is this machine, as net/smtp sees it.
func isLocal(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// Send implements [Transport]: it connects, sends m and quits. Replies
// in the 500s (a rejected recipient, refused credentials; but not 552 to
// a recipient, which RFC 5321 says to treat as temporary) and errors in
// the message or the settings are permanent (queue.Permanent). One
// rejected recipient fails the whole email.
func (t *SMTPTransport) Send(ctx context.Context, m *Outgoing) error {
	if err := m.Validate(); err != nil {
		return queue.Permanent(err)
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	err := t.send(ctx, m)
	if err != nil {
		// The connection's deadline is the context's: when it passes, the
		// context may not say so yet.
		cause := ctx.Err()
		if d, ok := ctx.Deadline(); cause == nil && ok && !time.Now().Before(d) {
			cause = context.DeadlineExceeded
		}
		if cause != nil && !errors.Is(err, cause) {
			err = fmt.Errorf("%w (%w)", cause, err)
		}
	}
	var te *textproto.Error
	var rcpt rcptError
	if errors.As(err, &te) && te.Code >= 500 {
		if te.Code == 552 && errors.As(err, &rcpt) {
			return err // a full mailbox: RFC 5321 says to retry
		}
		return queue.Permanent(err)
	}
	return err
}

// rcptError is a recipient's refusal.
type rcptError struct{ error }

func (e rcptError) Unwrap() error { return e.error }

func (t *SMTPTransport) send(ctx context.Context, m *Outgoing) error {
	tlsConfig := &tls.Config{ServerName: t.host, MinVersion: tls.VersionTLS12, RootCAs: t.rootCAs}
	var conn net.Conn
	var err error
	if t.implicit {
		conn, err = (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", t.addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", t.addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", t.addr, err)
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	c, err := smtp.NewClient(conn, t.host)
	if err != nil {
		return fmt.Errorf("greeting: %w", err)
	}
	defer c.Close()
	if err := c.Hello(t.localName); err != nil {
		return fmt.Errorf("EHLO: %w", err)
	}
	if !t.implicit && t.starttls != "none" {
		ok, _ := c.Extension("STARTTLS")
		switch {
		case ok:
			if err := c.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		case t.starttls == "required":
			return queue.Permanent(fmt.Errorf("%s doesn't offer STARTTLS: use smtps://, or tls=none for a server without TLS on a private network", t.addr))
		}
	}
	if t.auth {
		ok, mechs := c.Extension("AUTH")
		var a smtp.Auth
		switch {
		case !ok:
			return queue.Permanent(fmt.Errorf("%s doesn't offer AUTH, but MAIL_SMTP_URL has a user", t.addr))
		case slices.Contains(strings.Fields(mechs), "PLAIN"):
			a = smtp.PlainAuth("", t.user, t.pass, t.host)
		case slices.Contains(strings.Fields(mechs), "LOGIN"):
			a = loginAuth{user: t.user, pass: t.pass, host: t.host}
		default:
			return queue.Permanent(fmt.Errorf("%s offers AUTH %s; PLAIN and LOGIN are supported", t.addr, mechs))
		}
		if err := c.Auth(a); err != nil {
			if msg := err.Error(); msg == "unencrypted connection" || msg == "wrong host name" {
				err = queue.Permanent(err) // net/smtp (or loginAuth) refused to send the password
			}
			return fmt.Errorf("AUTH: %w", err)
		}
	}
	if ok, _ := c.Extension("SMTPUTF8"); !ok {
		addrs := append([]string{m.From.Address}, m.Recipients()...)
		for _, r := range m.ReplyTo {
			addrs = append(addrs, r.Address)
		}
		for _, a := range addrs {
			if !isASCII(a) {
				return queue.Permanent(fmt.Errorf("%s doesn't offer SMTPUTF8, needed for the address %s", t.addr, a))
			}
		}
	}
	if err := c.Mail(m.From.Address); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, r := range m.Recipients() {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("RCPT TO <%s>: %w", r, rcptError{err})
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(m.MIME()); err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	_ = c.Quit() // the server has the email
	return nil
}

// loginAuth is AUTH LOGIN, for servers without PLAIN.
type loginAuth struct{ user, pass, host string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocal(server.Name) {
		return "", nil, errors.New("unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("unexpected AUTH LOGIN challenge %q", fromServer)
}
