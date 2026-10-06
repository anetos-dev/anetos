// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"fmt"
	"slices"

	"anetos.dev/anetos"
)

// checks are the doctor's checks of the HTTP_* settings.
func checks(cfg Config, env anetos.Environment) []anetos.Finding {
	var out []anetos.Finding
	trusted, _ := ParsePrefixes(cfg.TrustedProxies) // validated
	for _, p := range trusted {
		switch {
		case p.Bits() == 0:
			out = append(out, anetos.Finding{Severity: anetos.Problem, Message: fmt.Sprintf("HTTP_TRUSTED_PROXIES has %s: every client is trusted, so anyone can choose the IP address the app sees (rate limits, logs) with X-Forwarded-For; list your proxies' addresses", p)})
		case p.Addr().Is4() && p.Bits() < 8, p.Addr().Is6() && p.Bits() < 16:
			out = append(out, anetos.Finding{Severity: anetos.Warning, Message: fmt.Sprintf("HTTP_TRUSTED_PROXIES has %s, a very wide range: clients in it can choose the IP address the app sees; list your proxies' addresses", p)})
		}
	}
	if slices.Contains(cfg.CORS.Origins, "*") {
		out = append(out, anetos.Finding{Severity: anetos.Warning, Message: `HTTP_CORS_ORIGINS="*": scripts on any site can call the app and read its answers (without cookies); right for a public API, else list the origins`})
	}
	if env.Deployed() {
		if cfg.MaxBody == 0 {
			out = append(out, anetos.Finding{Severity: anetos.Warning, Message: "HTTP_MAX_BODY=0: request bodies have no size limit, so one client can fill memory or disk; set a limit (default 10MB)"})
		}
		if cfg.RequestTimeout == 0 {
			out = append(out, anetos.Finding{Severity: anetos.Warning, Message: "HTTP_REQUEST_TIMEOUT=0: requests have no deadline, so slow ones pile up; set one (default 30s)"})
		}
		if cfg.ReadHeaderTimeout == 0 {
			out = append(out, anetos.Finding{Severity: anetos.Warning, Message: "HTTP_READ_HEADER_TIMEOUT=0: clients may send headers as slowly as they like and hold connections open; set one (default 10s)"})
		}
	}
	return out
}

func (s *Server) addCheck(app *anetos.App) {
	app.AddCheck(anetos.Check{Name: "http", Run: func(context.Context) []anetos.Finding {
		return checks(s.cfg, app.Config().Env)
	}})
}
