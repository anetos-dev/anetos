// SPDX-License-Identifier: Apache-2.0

package mailer

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"maps"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"slices"
	"strings"
	"unicode/utf8"
)

// MIME returns the email as an RFC 5322 message, for SMTP and for API
// drivers that take raw messages: the headers (without Bcc), then the
// bodies, inline files and attachments as MIME parts. Line endings are
// CRLF. Call [Outgoing.Validate] first.
func (o *Outgoing) MIME() []byte {
	var b bytes.Buffer
	header := func(name, value string) {
		b.WriteString(name + ": " + value + "\r\n")
	}
	header("From", formatAddress(o.From))
	if len(o.To) > 0 {
		header("To", addressList(o.To))
	}
	if len(o.Cc) > 0 {
		header("Cc", addressList(o.Cc))
	}
	if len(o.ReplyTo) > 0 {
		header("Reply-To", addressList(o.ReplyTo))
	}
	header("Subject", encodeHeader("Subject", o.Subject))
	header("Date", o.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	header("Message-ID", "<"+o.MessageID+">")
	header("MIME-Version", "1.0")
	for _, k := range slices.Sorted(maps.Keys(o.Headers)) {
		name := textproto.CanonicalMIMEHeaderKey(k)
		header(name, encodeHeader(name, o.Headers[k]))
	}
	for _, k := range slices.Sorted(maps.Keys(o.Metadata)) {
		header("X-Metadata-"+k, encodeHeader("X-Metadata-"+k, o.Metadata[k]))
	}

	var inline, attached []Attachment
	for _, a := range o.Attachments {
		if a.ContentID != "" {
			inline = append(inline, a)
		} else {
			attached = append(attached, a)
		}
	}
	// mixed(related(alternative(text, html), inline…), attachments…),
	// leaving out the levels with one part.
	body := textPart("text/plain", o.Text)
	if o.HTML != "" {
		body = multi{"alternative", []part{body, textPart("text/html", o.HTML)}}
	}
	if len(inline) > 0 {
		parts := []part{body}
		for _, a := range inline {
			parts = append(parts, filePart(a))
		}
		body = multi{"related", parts}
	}
	if len(attached) > 0 {
		parts := []part{body}
		for _, a := range attached {
			parts = append(parts, filePart(a))
		}
		body = multi{"mixed", parts}
	}
	h, content := body.render()
	for _, k := range slices.Sorted(maps.Keys(h)) {
		header(k, h.Get(k))
	}
	b.WriteString("\r\n")
	b.Write(content)
	return b.Bytes()
}

// addressList formats addresses, one per line.
func addressList(as []Address) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = formatAddress(a)
	}
	return strings.Join(s, ",\r\n ")
}

// formatAddress formats a, a name that isn't plain ASCII as encoded
// words, one per line.
func formatAddress(a Address) string {
	if a.Name == "" || plainASCII(a.Name) {
		return a.String()
	}
	return strings.Join(qWords(a.Name), "\r\n ") + " <" + a.Address + ">"
}

// plainASCII reports whether s is printable ASCII that doesn't look like
// an encoded word, so it can go in a header as is.
func plainASCII(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return !strings.Contains(s, "=?")
}

// encodeHeader formats the value of the header name: plain ASCII folded
// at spaces to keep lines short, other text as RFC 2047 encoded words,
// one per line.
func encodeHeader(name, v string) string {
	if !plainASCII(v) {
		return strings.Join(qWords(v), "\r\n ")
	}
	var b strings.Builder
	n := len(name) + 2 // "Name: "
	for i, w := range strings.Split(v, " ") {
		if i > 0 {
			if n+1+len(w) > 78 && n > 1 {
				b.WriteString("\r\n")
				n = 0
			}
			b.WriteByte(' ')
			n++
		}
		b.WriteString(w)
		n += len(w)
	}
	return b.String()
}

// qWords encodes s as RFC 2047 "Q" encoded words of at most 75
// characters, not splitting characters.
func qWords(s string) []string {
	const head, tail = "=?utf-8?q?", "?="
	var words []string
	var w strings.Builder
	for _, r := range s {
		var enc string
		switch {
		case r == ' ':
			enc = "_"
		case r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!*+-/", r)):
			enc = string(r)
		default:
			var buf [utf8.UTFMax]byte
			for _, c := range buf[:utf8.EncodeRune(buf[:], r)] {
				enc += fmt.Sprintf("=%02X", c)
			}
		}
		if w.Len() > 0 && len(head)+w.Len()+len(enc)+len(tail) > 75 {
			words = append(words, head+w.String()+tail)
			w.Reset()
		}
		w.WriteString(enc)
	}
	if w.Len() > 0 || len(words) == 0 {
		words = append(words, head+w.String()+tail)
	}
	return words
}

// part is a MIME part: its headers and content.
type part interface {
	render() (textproto.MIMEHeader, []byte)
}

type leaf struct {
	header  textproto.MIMEHeader
	content []byte
}

func (l leaf) render() (textproto.MIMEHeader, []byte) { return l.header, l.content }

// textPart is text, quoted-printable.
func textPart(mediaType, s string) part {
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return leaf{textproto.MIMEHeader{
		"Content-Type":              {mediaType + "; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	}, b.Bytes()}
}

// filePart is an attachment or inline file, base64.
func filePart(a Attachment) part {
	disposition := "attachment"
	h := textproto.MIMEHeader{"Content-Transfer-Encoding": {"base64"}}
	if a.ContentID != "" {
		disposition = "inline"
		h.Set("Content-ID", "<"+a.ContentID+">")
	}
	mediaType, params, _ := mime.ParseMediaType(a.ContentType)
	params["name"] = a.Filename
	h.Set("Content-Type", mime.FormatMediaType(mediaType, params))
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.Filename}))
	enc := base64.StdEncoding.EncodeToString(a.Data)
	var b bytes.Buffer
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return leaf{h, b.Bytes()}
}

// multi is a multipart part.
type multi struct {
	subtype string
	parts   []part
}

func (m multi) render() (textproto.MIMEHeader, []byte) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, p := range m.parts {
		h, content := p.render()
		pw, _ := w.CreatePart(h)
		_, _ = pw.Write(content)
	}
	_ = w.Close()
	return textproto.MIMEHeader{"Content-Type": {fmt.Sprintf("multipart/%s; boundary=%q", m.subtype, w.Boundary())}}, b.Bytes()
}
