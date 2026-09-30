// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"net/http"
	"strings"
	"time"
)

// jar keeps the cookies of the test site. Unlike net/http/cookiejar, it
// returns Secure cookies over the test's plain-HTTP requests and ignores
// Domain (there is one site), so apps with SESSION_SECURE, SESSION_DOMAIN
// or the __Host- prefix keep their sessions in tests. It honors Path,
// Max-Age and Expires.
type jar struct {
	cookies map[jarKey]jarCookie
}

type jarKey struct{ name, path string }

type jarCookie struct {
	c       *http.Cookie
	expires time.Time // zero: session cookie
}

func newJar() *jar { return &jar{cookies: map[jarKey]jarCookie{}} }

// set stores the cookies of a response.
func (j *jar) set(cs []*http.Cookie) {
	now := time.Now()
	for _, c := range cs {
		path := c.Path
		if path == "" || path[0] != '/' {
			path = "/"
		}
		k := jarKey{c.Name, path}
		var exp time.Time
		switch {
		case c.MaxAge < 0:
			delete(j.cookies, k)
			continue
		case c.MaxAge > 0:
			exp = now.Add(time.Duration(c.MaxAge) * time.Second)
		case !c.Expires.IsZero():
			if !c.Expires.After(now) {
				delete(j.cookies, k)
				continue
			}
			exp = c.Expires
		}
		j.cookies[k] = jarCookie{&http.Cookie{Name: c.Name, Value: c.Value, Path: path}, exp}
	}
}

// forPath returns the cookies a browser sends to path, longest path
// first; all of them for path "".
func (j *jar) forPath(path string) []*http.Cookie {
	now := time.Now()
	var out []*http.Cookie
	for k, jc := range j.cookies {
		if !jc.expires.IsZero() && !jc.expires.After(now) {
			delete(j.cookies, k)
			continue
		}
		if path == "" || pathMatch(path, k.path) {
			out = append(out, jc.c)
		}
	}
	// Longest path first, as browsers send them; then by name, for a
	// stable order.
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && less(out[k], out[k-1]); k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out
}

func less(a, b *http.Cookie) bool {
	if len(a.Path) != len(b.Path) {
		return len(a.Path) > len(b.Path)
	}
	return a.Name < b.Name
}

// pathMatch implements RFC 6265 §5.1.4.
func pathMatch(reqPath, cookiePath string) bool {
	if reqPath == cookiePath {
		return true
	}
	return strings.HasPrefix(reqPath, cookiePath) &&
		(strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/')
}
