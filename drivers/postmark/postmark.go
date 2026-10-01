// SPDX-License-Identifier: Apache-2.0

// Package postmark sends the mailer package's emails with Postmark's API
// (https://postmarkapp.com):
//
//	m, err := mailer.ForApp(app, postmark.Driver())
//
// Settings: MAIL_DRIVER=postmark, MAIL_POSTMARK_TOKEN (a server API
// token; POSTMARK_API_TEST checks requests without sending), and
// MAIL_POSTMARK_STREAM (the message stream, default "outbound").
package postmark

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"
)

// Config is the transport's settings.
type Config struct {
	// Token is the Postmark server's API token. MAIL_POSTMARK_TOKEN.
	Token anetos.Secret `env:"MAIL_POSTMARK_TOKEN"`
	// Stream is the message stream emails are sent on.
	// MAIL_POSTMARK_STREAM, default outbound.
	Stream string `env:"MAIL_POSTMARK_STREAM" default:"outbound"`
}

// Driver is the transport's driver (MAIL_DRIVER=postmark).
func Driver(opts ...Option) mailer.Driver {
	return mailer.Driver{Name: "postmark", Open: func(app *anetos.App, _ mailer.Config) (mailer.Transport, error) {
		c, err := config.Get[Config](app.Source())
		if err != nil {
			return nil, err
		}
		if c.Token == "" {
			return nil, errors.New("MAIL_POSTMARK_TOKEN is required with MAIL_DRIVER=postmark")
		}
		return NewTransport(string(c.Token), append([]Option{Stream(c.Stream)}, opts...)...), nil
	}}
}

// Transport sends emails with Postmark's API.
type Transport struct {
	token, stream, baseURL string
	client                 *http.Client
}

// Option configures a [Transport].
type Option func(*Transport)

// Stream sets the message stream. Default "outbound".
func Stream(s string) Option { return func(t *Transport) { t.stream = s } }

// BaseURL sets the API's URL, for tests. Default https://api.postmarkapp.com.
func BaseURL(u string) Option { return func(t *Transport) { t.baseURL = strings.TrimSuffix(u, "/") } }

// HTTPClient sets the HTTP client. Default: one with a 30-second timeout.
func HTTPClient(c *http.Client) Option { return func(t *Transport) { t.client = c } }

// NewTransport returns a transport sending with the server token token.
func NewTransport(token string, opts ...Option) *Transport {
	t := &Transport{token: token, stream: "outbound", baseURL: "https://api.postmarkapp.com",
		client: &http.Client{Timeout: 30 * time.Second}}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// maxRecipients is Postmark's limit for To, Cc and Bcc together.
const maxRecipients = 50

// notAllowedToSend is Postmark's error code for an account that can't
// send for now (out of credits): retried, not failed.
const notAllowedToSend = 405

type email struct {
	From          string            `json:"From"`
	To            string            `json:"To,omitempty"`
	Cc            string            `json:"Cc,omitempty"`
	Bcc           string            `json:"Bcc,omitempty"`
	ReplyTo       string            `json:"ReplyTo,omitempty"`
	Subject       string            `json:"Subject"`
	Tag           string            `json:"Tag,omitempty"`
	HTMLBody      string            `json:"HtmlBody,omitempty"`
	TextBody      string            `json:"TextBody,omitempty"`
	Headers       []header          `json:"Headers,omitempty"`
	Metadata      map[string]string `json:"Metadata,omitempty"`
	Attachments   []attachment      `json:"Attachments,omitempty"`
	MessageStream string            `json:"MessageStream"`
}

type header struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

type attachment struct {
	Name        string `json:"Name"`
	Content     string `json:"Content"`
	ContentType string `json:"ContentType"`
	ContentID   string `json:"ContentID,omitempty"`
}

type response struct {
	ErrorCode int    `json:"ErrorCode"`
	Message   string `json:"Message"`
	MessageID string `json:"MessageID"`
}

// Error is an error response of Postmark's API.
type Error struct {
	// Status is the HTTP status.
	Status int
	// Code is Postmark's error code (300: invalid email request, 406:
	// inactive recipient, …).
	Code int
	// Message is Postmark's explanation.
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	return fmt.Sprintf("postmark: %d %s (error code %d)", e.Status, e.Message, e.Code)
}

// Send implements mailer.Transport. Postmark's refusals (422, 401, 403,
// 413) are permanent errors (queue.Permanent), except error code 405
// (the account can't send now: out of credits); rate limits (429) and
// server errors aren't either. Postmark gives the email a Message-ID of
// its own.
func (t *Transport) Send(ctx context.Context, m *mailer.Outgoing) error {
	if err := m.Validate(); err != nil {
		return queue.Permanent(err)
	}
	if n := len(m.Recipients()); n > maxRecipients {
		return queue.Permanent(fmt.Errorf("postmark: %d recipients; Postmark takes at most %d per email", n, maxRecipients))
	}
	e := email{From: m.From.String(), To: list(m.To), Cc: list(m.Cc), Bcc: list(m.Bcc), ReplyTo: list(m.ReplyTo),
		Subject: m.Subject, Tag: m.Tag, HTMLBody: m.HTML, TextBody: m.Text, Metadata: m.Metadata, MessageStream: t.stream}
	for _, k := range slices.Sorted(maps.Keys(m.Headers)) {
		e.Headers = append(e.Headers, header{k, m.Headers[k]})
	}
	for _, a := range m.Attachments {
		at := attachment{Name: a.Filename, Content: base64.StdEncoding.EncodeToString(a.Data), ContentType: a.ContentType}
		if a.ContentID != "" {
			at.ContentID = "cid:" + a.ContentID
		}
		e.Attachments = append(e.Attachments, at)
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/email", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", t.token)
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("postmark: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("postmark: read the response: %w", err)
	}
	var r response
	if json.Unmarshal(data, &r) != nil {
		r.Message = strings.TrimSpace(string(data))
	}
	if resp.StatusCode == http.StatusOK && r.ErrorCode == 0 {
		return nil
	}
	pe := &Error{Status: resp.StatusCode, Code: r.ErrorCode, Message: r.Message}
	switch {
	case resp.StatusCode == http.StatusUnprocessableEntity && r.ErrorCode == notAllowedToSend:
	case resp.StatusCode == http.StatusUnprocessableEntity, resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusRequestEntityTooLarge:
		return queue.Permanent(pe)
	}
	return pe
}

// list formats addresses as Postmark takes them: comma-separated.
func list(as []mailer.Address) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}
