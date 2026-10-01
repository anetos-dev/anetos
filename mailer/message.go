// SPDX-License-Identifier: Apache-2.0

package mailer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/mail"
	"path"
	"slices"
	"strings"
	"time"

	"anetos.dev/anetos/internal/htmltext"
	"anetos.dev/anetos/view"
)

// Address is an email address with an optional display name:
// mailer.Address{Name: "Ada Lovelace", Address: "ada@example.com"}.
type Address struct {
	// Name is the display name; it may be empty.
	Name string `json:"name,omitempty"`
	// Address is the email address, "ada@example.com".
	Address string `json:"address"`
}

// String formats a as in a header: "Ada Lovelace" <ada@example.com>, the
// name quoted, or encoded if it isn't ASCII.
func (a Address) String() string {
	return (&mail.Address{Name: a.Name, Address: a.Address}).String()
}

// Mailable is an email: a type that builds its [Message] from its fields
// and the context (which has the app's values: the database, …).
//
//	type Welcome struct{ User models.User }
//
//	func (w Welcome) Build(ctx context.Context) (*mailer.Message, error) {
//		return &mailer.Message{
//			To:      []mailer.Address{{Name: w.User.Name, Address: w.User.Email}},
//			Subject: "Welcome to Blog",
//			HTML:    views.WelcomeEmail(w.User),
//		}, nil
//	}
//
// A *Message is a Mailable too.
type Mailable interface {
	// Build returns the message to send; ctx is Send's (or Queue's).
	Build(ctx context.Context) (*Message, error)
}

// Message is an email to send.
type Message struct {
	// From is the sender. Default: MAIL_FROM_ADDRESS and MAIL_FROM_NAME.
	From Address
	// To, Cc and Bcc are the recipients: at least one in all. Bcc
	// recipients get the email without being listed in it.
	To, Cc, Bcc []Address
	// ReplyTo is where replies go, if not to From.
	ReplyTo []Address
	// Subject is the subject line.
	Subject string
	// HTML is the HTML body: a templ component (or any view.Component),
	// rendered with the context of Send. Use inline styles: many mail
	// clients ignore style sheets.
	HTML view.Component
	// Text is the plain-text body, as is (build it with fmt or
	// text/template: templ would HTML-escape it). Without it, the HTML
	// body's text is used: paragraphs, lists, and links followed by their
	// URL. At least one of HTML and Text is required.
	Text string
	// Attachments are files sent with the email.
	Attachments []Attachment
	// Headers are extra headers, such as List-Unsubscribe. They can't
	// replace the ones the message sets (From, To, Subject, Date, …).
	Headers map[string]string
	// Tag labels the email for API drivers that group emails by it
	// (Postmark); SMTP ignores it.
	Tag string
	// Metadata labels the email with pairs that API drivers keep with it
	// (Postmark); SMTP sends them as X-Metadata-<key> headers. Keys are
	// letters, digits, - and _.
	Metadata map[string]string
}

// Build implements [Mailable]: a message is its own.
func (m *Message) Build(context.Context) (*Message, error) { return m, nil }

// Attachment is a file sent with an email.
type Attachment struct {
	// Filename is the file's name, as the recipient sees it.
	Filename string `json:"filename"`
	// ContentType is the file's media type. Default: from the
	// filename's extension, or application/octet-stream.
	ContentType string `json:"content_type,omitempty"`
	// Data is the file's content.
	Data []byte `json:"data"`
	// ContentID makes the file inline, shown in the HTML body by
	// <img src="cid:ContentID">, rather than attached.
	ContentID string `json:"content_id,omitempty"`
}

// Outgoing is a rendered email, what transports send: a [Message] with
// its sender, bodies and ID filled in. It is JSON, for queued mail.
type Outgoing struct {
	// MessageID is the Message-ID header's value, without the angle
	// brackets: unique, and kept if the email is sent again.
	MessageID string `json:"message_id"`
	// Date is when the email is sent: when it was rendered, or, for
	// queued mail, when the job sends it.
	Date time.Time `json:"date"`
	// From is the sender: the message's, or the mailer's default.
	From Address `json:"from"`
	// To is the main recipients.
	To []Address `json:"to,omitempty"`
	// Cc is the copied recipients.
	Cc []Address `json:"cc,omitempty"`
	// Bcc is the hidden recipients: in the SMTP envelope, not the
	// message.
	Bcc []Address `json:"bcc,omitempty"`
	// ReplyTo is where replies go.
	ReplyTo []Address `json:"reply_to,omitempty"`
	// Subject is the subject line.
	Subject string `json:"subject"`
	// HTML is the HTML body; it may be empty.
	HTML string `json:"html,omitempty"`
	// Text is the plain-text body.
	Text string `json:"text"`
	// Attachments are as in [Message], with their content types set.
	Attachments []Attachment `json:"attachments,omitempty"`
	// Headers are the message's extra headers.
	Headers map[string]string `json:"headers,omitempty"`
	// Tag is the message's tag.
	Tag string `json:"tag,omitempty"`
	// Metadata is the message's metadata.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Recipients returns the addresses of To, Cc and Bcc: the envelope's
// recipients.
func (o *Outgoing) Recipients() []string {
	var out []string
	for _, list := range [][]Address{o.To, o.Cc, o.Bcc} {
		for _, a := range list {
			out = append(out, a.Address)
		}
	}
	return out
}

// reserved are the headers a message sets itself.
var reserved = map[string]bool{"from": true, "to": true, "cc": true, "bcc": true, "reply-to": true, "subject": true,
	"date": true, "message-id": true, "mime-version": true, "content-type": true, "content-transfer-encoding": true,
	"content-disposition": true, "content-id": true, "sender": true, "return-path": true, "received": true,
	"dkim-signature": true}

// Limits that keep every header line within RFC 5322's 998 characters.
const (
	maxName     = 300 // a display name
	maxFilename = 255 // an attachment's name, in bytes
	maxWord     = 900 // a run of characters without a space, in a header value
)

// render builds and renders mailable with ctx.
func (m *Mailer) render(ctx context.Context, mailable Mailable) (*Outgoing, error) {
	if mailable == nil {
		return nil, errors.New("mailer: nil mailable")
	}
	msg, err := mailable.Build(ctx)
	if err != nil {
		return nil, fmt.Errorf("mailer: build %T: %w", mailable, err)
	}
	if msg == nil {
		return nil, fmt.Errorf("mailer: %T built no message", mailable)
	}
	o := &Outgoing{From: msg.From, To: msg.To, Cc: msg.Cc, Bcc: msg.Bcc, ReplyTo: msg.ReplyTo, Subject: msg.Subject,
		Headers: msg.Headers, Tag: msg.Tag, Metadata: msg.Metadata, Date: m.now().UTC().Truncate(time.Second)}
	if o.From.Address == "" {
		o.From = m.from
	}
	if o.From.Address == "" {
		return nil, errors.New("mailer: the message has no sender: set MAIL_FROM_ADDRESS, or Message.From")
	}
	if msg.HTML == nil && msg.Text == "" {
		return nil, fmt.Errorf("mailer: %T built a message without a body (HTML or Text)", mailable)
	}
	if msg.HTML != nil {
		if o.HTML, err = view.String(ctx, msg.HTML); err != nil {
			return nil, fmt.Errorf("mailer: render the HTML body: %w", err)
		}
	}
	o.Text = msg.Text
	if o.Text == "" {
		o.Text = htmltext.Convert(o.HTML)
	}
	for _, a := range msg.Attachments {
		if a.ContentType == "" {
			a.ContentType = mime.TypeByExtension(path.Ext(a.Filename))
			if a.ContentType == "" {
				a.ContentType = "application/octet-stream"
			}
		}
		o.Attachments = append(o.Attachments, a)
	}
	o.MessageID = newMessageID(o.From.Address)
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return o, nil
}

// newMessageID returns a unique Message-ID at the sender's domain (or
// localhost, if the domain isn't ASCII).
func newMessageID(from string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := "localhost"
	if _, d, ok := strings.Cut(from, "@"); ok && d != "" && isASCII(d) {
		domain = d
	}
	return hex.EncodeToString(b) + "@" + domain
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Validate checks that the email can be sent: a sender and a recipient,
// valid addresses, no line breaks in the subject, names and headers, and
// no header line over the length limit. Transports check it again.
func (o *Outgoing) Validate() error {
	var errs []error
	check := func(field string, a Address) {
		if err := checkAddress(a); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", field, err))
		}
	}
	check("From", o.From)
	for _, l := range []struct {
		field string
		list  []Address
	}{{"To", o.To}, {"Cc", o.Cc}, {"Bcc", o.Bcc}, {"Reply-To", o.ReplyTo}} {
		for _, a := range l.list {
			check(l.field, a)
		}
	}
	if len(o.To)+len(o.Cc)+len(o.Bcc) == 0 {
		errs = append(errs, errors.New("no recipient (To, Cc or Bcc)"))
	}
	if err := checkValue("the subject", o.Subject); err != nil {
		errs = append(errs, err)
	}
	if strings.ContainsAny(o.MessageID, "<>\r\n \t") || !strings.Contains(o.MessageID, "@") || !isASCII(o.MessageID) {
		errs = append(errs, fmt.Errorf("invalid message ID %q", o.MessageID))
	}
	seen := map[string]bool{}
	for _, k := range slices.Sorted(maps.Keys(o.Headers)) {
		lower := strings.ToLower(k)
		switch {
		case !headerName(k):
			errs = append(errs, fmt.Errorf("invalid header name %q", k))
		case reserved[lower] || strings.HasPrefix(lower, "resent-") || strings.HasPrefix(lower, "x-metadata-"):
			errs = append(errs, fmt.Errorf("the header %s is set by the message", k))
		case seen[lower]:
			errs = append(errs, fmt.Errorf("the header %s is there twice", k))
		}
		seen[lower] = true
		if err := checkValue("the header "+k, o.Headers[k]); err != nil {
			errs = append(errs, err)
		}
	}
	seen = map[string]bool{}
	for _, k := range slices.Sorted(maps.Keys(o.Metadata)) {
		if !headerName(k) || len(k) > 60 {
			errs = append(errs, fmt.Errorf("invalid metadata key %q: use up to 60 letters, digits, - and _", k))
		} else if seen[strings.ToLower(k)] {
			errs = append(errs, fmt.Errorf("the metadata key %s is there twice", k))
		}
		seen[strings.ToLower(k)] = true
		if err := checkValue("the metadata "+k, o.Metadata[k]); err != nil {
			errs = append(errs, err)
		}
	}
	for _, a := range o.Attachments {
		if a.Filename == "" || len(a.Filename) > maxFilename || strings.ContainsAny(a.Filename, "\x00\r\n\"/\\") {
			errs = append(errs, fmt.Errorf("invalid attachment name %q (up to %d bytes, without / \\ \" or line breaks)", a.Filename, maxFilename))
		}
		if _, _, err := mime.ParseMediaType(a.ContentType); err != nil || len(a.ContentType) > 200 {
			errs = append(errs, fmt.Errorf("attachment %s: content type %q: %w", a.Filename, a.ContentType, err))
		}
		if strings.ContainsAny(a.ContentID, "<>\r\n \t\"") || !isASCII(a.ContentID) || len(a.ContentID) > 200 {
			errs = append(errs, fmt.Errorf("attachment %s: invalid content ID %q", a.Filename, a.ContentID))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("mailer: invalid message: %w", err)
	}
	return nil
}

// checkValue checks a header value: no line breaks or NUL, and no run
// of characters without a space too long to fold.
func checkValue(what, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("%s has a line break", what)
	}
	if !plainASCII(v) {
		return nil // encoded, in short words
	}
	for w := range strings.FieldsSeq(v) {
		if len(w) > maxWord {
			return fmt.Errorf("%s has a word over %d characters", what, maxWord)
		}
	}
	return nil
}

// checkAddress checks a's address and name.
func checkAddress(a Address) error {
	if strings.ContainsAny(a.Name, "\r\n\x00") || len(a.Name) > maxName {
		return fmt.Errorf("the name %q has a line break, or is over %d bytes", a.Name, maxName)
	}
	p, err := mail.ParseAddress(a.Address)
	local, _, _ := strings.Cut(a.Address, "@")
	if err != nil || p.Address != a.Address || p.Name != "" || len(a.Address) > 254 || len(local) > 64 {
		return fmt.Errorf("%q isn't an email address (at most 64 bytes before the @, 254 in all)", a.Address)
	}
	return nil
}

// headerName reports whether s is a header name of letters, digits, - and _.
func headerName(s string) bool {
	if s == "" || len(s) > 76 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
