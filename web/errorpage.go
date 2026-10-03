// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"anetos.dev/anetos/i18n"
)

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// redactedHeaders are hidden on the debug error page.
var redactedHeaders = map[string]bool{
	"Authorization": true, "Cookie": true, "Proxy-Authorization": true, "X-Api-Key": true,
	"X-Auth-Token": true, "X-Csrf-Token": true, "X-Xsrf-Token": true, "X-Amz-Security-Token": true,
}

// sensitiveQuery matches query parameter names whose values are hidden on
// the debug error page.
func sensitiveQuery(name string) bool {
	n := strings.ToLower(name)
	for _, s := range []string{"token", "key", "secret", "password", "passwd", "signature", "sig", "code", "auth"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

func redactedURL(u *url.URL) string {
	q := u.Query()
	changed := false
	for k := range q {
		if sensitiveQuery(k) {
			q[k] = []string{"[redacted]"}
			changed = true
		}
	}
	if !changed {
		return u.String()
	}
	c := *u
	c.RawQuery = q.Encode()
	return c.String()
}

type errorPageData struct {
	Lang      string // the page's language
	Dir       string // its writing direction: ltr or rtl
	Problems  string // the heading of the field errors
	RequestID string // the request ID line
	P         *problem
	Debug     bool
	Method    string
	URL       string
	Route     string
	Headers   [][2]string
	Fields    []string
}

func renderErrorPage(c *Ctx, p *problem) {
	ctx := c.r.Context()
	d := errorPageData{P: p, Debug: c.router.core.debug, Lang: i18n.Locale(ctx), Dir: i18n.Dir(ctx), Problems: i18n.T(ctx, "http.problems")}
	if p.RequestID != "" {
		d.RequestID = i18n.T(ctx, "http.request_id", "id", p.RequestID)
	}
	d.Fields = sortedKeys(p.Errors)
	if d.Debug {
		d.Method = c.r.Method
		d.URL = redactedURL(c.r.URL)
		if c.route != nil {
			d.Route = c.route.method + " " + c.route.pattern
			if name := c.route.RouteName(); name != "" {
				d.Route += " (" + name + ")"
			}
		}
		for _, k := range sortedHeaderKeys(c.r.Header) {
			v := strings.Join(c.r.Header.Values(k), ", ")
			if redactedHeaders[k] {
				v = "[redacted]"
			}
			d.Headers = append(d.Headers, [2]string{k, v})
		}
	}

	var buf bytes.Buffer
	if err := errorPage.Execute(&buf, d); err != nil {
		http.Error(c.w, p.Title, p.Status)
		return
	}
	h := c.w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	c.w.WriteHeader(p.Status)
	if c.r.Method != http.MethodHead {
		_, _ = c.w.Write(buf.Bytes())
	}
}

func sortedHeaderKeys(h http.Header) []string {
	m := make(map[string]string, len(h))
	for k := range h {
		m[k] = ""
	}
	return sortedKeys(m)
}

var errorPage = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.P.Status}} {{.P.Title}}</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0; background: #f7f7f8; color: #1d1d1f; }
  main { max-width: 56rem; margin: 10vh auto; padding: 0 1.5rem; }
  h1 { font-size: 1.75rem; margin: 0 0 .5rem; }
  .status { color: #8a8a8e; font-weight: 600; }
  section { background: #fff; border: 1px solid #e3e3e6; border-radius: 8px; padding: 1rem 1.25rem; margin-top: 1.25rem; }
  h2 { font-size: 1rem; margin: 0 0 .5rem; }
  pre { overflow-x: auto; font-size: .85rem; background: #f2f2f4; padding: .75rem; border-radius: 6px; }
  table { border-collapse: collapse; width: 100%; font-size: .85rem; }
  td { border-top: 1px solid #eee; padding: .3rem .5rem; vertical-align: top; word-break: break-all; }
  td:first-child { font-weight: 600; white-space: nowrap; word-break: normal; }
  .debug { border-color: #f0b429; }
  @media (prefers-color-scheme: dark) {
    body { background: #161618; color: #eee; }
    section { background: #1f1f22; border-color: #333; }
    pre { background: #2a2a2e; }
    td { border-color: #333; }
  }
</style>
</head>
<body>
<main>
  <div class="status">{{.P.Status}}</div>
  <h1>{{.P.Title}}</h1>
  {{with .P.Detail}}<p>{{.}}</p>{{end}}
  {{if .Fields}}<section><h2>{{.Problems}}</h2><table>
    {{range .Fields}}<tr><td>{{.}}</td><td>{{index $.P.Errors .}}</td></tr>{{end}}
  </table></section>{{end}}
  {{with .RequestID}}<p class="status">{{.}}</p>{{end}}
  {{if .Debug}}{{with .P.Debug}}
  <section class="debug"><h2>Error (debug mode)</h2><pre>{{.Error}}</pre>
    {{if .Chain}}<h2>Cause chain</h2><pre>{{range .Chain}}{{.}}
{{end}}</pre>{{end}}
    {{with .Stack}}<h2>Stack trace</h2><pre>{{.}}</pre>{{end}}
  </section>{{end}}
  <section class="debug"><h2>Request</h2><table>
    <tr><td>Method</td><td>{{.Method}}</td></tr>
    <tr><td>URL</td><td>{{.URL}}</td></tr>
    {{with .Route}}<tr><td>Route</td><td>{{.}}</td></tr>{{end}}
    {{range .Headers}}<tr><td>{{index . 0}}</td><td>{{index . 1}}</td></tr>{{end}}
  </table>
  <p class="status">Shown because APP_DEBUG=true. Never enable debug mode in production.</p></section>
  {{end}}
</main>
</body>
</html>
`))
