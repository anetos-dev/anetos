// SPDX-License-Identifier: Apache-2.0

// Package secret masks secrets in values printed or logged: connection
// strings, URLs, settings.
package secret

import (
	"net/url"
	"regexp"
	"strings"
)

// masked replaces a secret.
const masked = "xxxxx"

// secretKey reports whether a DSN parameter holds a secret.
func secretKey(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "pass") || strings.Contains(k, "secret") || strings.Contains(k, "token") || strings.Contains(k, "key")
}

// MaskDSN masks the secrets of a connection string: the password of a URL
// ("postgres://u:p@h/db") or of MySQL's form ("u:p@tcp(h)/db"), and
// secret-looking parameters in a query string or in libpq's key=value form
// ("host=h password='p'"). If anything that looks like a password is left,
// it masks the whole string.
func MaskDSN(s string) string {
	if s == "" {
		return ""
	}
	var out string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return masked
		}
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), masked)
		}
		u.RawQuery = maskQuery(u.RawQuery)
		out = u.String()
	case keyValueDSN.MatchString(s):
		out = maskKeyValues(s)
	default: // MySQL's user:pass@proto(addr)/db?params, or a SQLite path?params
		base, query, hasQuery := strings.Cut(s, "?")
		if at := strings.LastIndex(base, "@"); at >= 0 {
			if user, _, ok := strings.Cut(base[:at], ":"); ok {
				base = user + ":" + masked + base[at:]
			}
		}
		out = base
		if hasQuery {
			out += "?" + maskQuery(query)
		}
	}
	// Belt and braces: if "pass" is still there other than in a masked
	// parameter's name, keep nothing.
	if strings.Contains(strings.ToLower(maskedParam.ReplaceAllString(out, "")), "pass") {
		return masked
	}
	return out
}

var (
	keyValueDSN = regexp.MustCompile(`^\s*[A-Za-z_]+\s*=`)                 // libpq's host=h user=u …
	maskedParam = regexp.MustCompile(`(?i)[a-z0-9_]*\s*=\s*xxxxx|:xxxxx@`) // what maskDSN wrote
)

// maskQuery masks the values of secret parameters, keeping the order and
// the rest of the query as written.
func maskQuery(q string) string {
	if q == "" {
		return ""
	}
	parts := strings.Split(q, "&")
	for i, p := range parts {
		k, _, ok := strings.Cut(p, "=")
		if uk, err := url.QueryUnescape(k); err == nil {
			k = uk
		}
		if ok && secretKey(k) {
			parts[i] = strings.SplitN(p, "=", 2)[0] + "=" + masked
		}
	}
	return strings.Join(parts, "&")
}

// maskKeyValues masks secret values in libpq's key=value form, where
// values may be quoted ('my secret') and "=" may have spaces around it.
func maskKeyValues(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		// Copy whitespace.
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
			b.WriteByte(s[i])
			i++
		}
		// Key.
		start := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		key := s[start:i]
		b.WriteString(key)
		// Spaces and "=".
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			b.WriteByte(s[i])
			i++
		}
		if i >= len(s) || s[i] != '=' {
			continue
		}
		b.WriteByte('=')
		i++
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			b.WriteByte(s[i])
			i++
		}
		// Value: quoted with backslash escapes, or up to whitespace.
		vstart := i
		if i < len(s) && s[i] == '\'' {
			i++
			for i < len(s) && s[i] != '\'' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
			i++ // closing quote
		} else {
			for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' {
				i++
			}
		}
		i = min(i, len(s))
		if secretKey(key) {
			b.WriteString(masked)
		} else {
			b.WriteString(s[vstart:i])
		}
	}
	return b.String()
}

// MaskValue masks what looks like a secret in a setting's value: what
// [MaskDSN] masks, and a URL's user name when there is no password
// ("https://TOKEN@host/"). A URL that doesn't parse is masked whole.
func MaskValue(s string) string {
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return masked
		}
		if u.User != nil {
			if _, ok := u.User.Password(); !ok && u.User.Username() != "" {
				u.User = url.User(masked)
				s = u.String()
			}
		}
	}
	return MaskDSN(s)
}
