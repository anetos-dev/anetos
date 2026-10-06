// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"anetos.dev/anetos/config"
	"anetos.dev/anetos/web"
)

func TestDoctorHTTP(t *testing.T) {
	tests := []struct {
		env  config.Map
		want []string
	}{
		{config.Map{"APP_ENV": "production"}, []string{"ok http"}},
		{config.Map{"APP_ENV": "production", "HTTP_TRUSTED_PROXIES": "10.0.0.0/8,0.0.0.0/0"}, []string{"problem http: HTTP_TRUSTED_PROXIES has 0.0.0.0/0: every client is trusted"}},
		{config.Map{"APP_ENV": "development", "HTTP_TRUSTED_PROXIES": "::/0"}, []string{"problem http: HTTP_TRUSTED_PROXIES has ::/0"}},
		{config.Map{"APP_ENV": "production", "HTTP_TRUSTED_PROXIES": "4.0.0.0/6"}, []string{"warning http: HTTP_TRUSTED_PROXIES has 4.0.0.0/6, a very wide range"}},
		{config.Map{"APP_ENV": "production", "HTTP_CORS_ORIGINS": "*"}, []string{`warning http: HTTP_CORS_ORIGINS="*"`}},
		{config.Map{"APP_ENV": "staging", "HTTP_MAX_BODY": "0", "HTTP_REQUEST_TIMEOUT": "0", "HTTP_READ_HEADER_TIMEOUT": "0"}, []string{
			"warning http: HTTP_MAX_BODY=0", "warning http: HTTP_REQUEST_TIMEOUT=0", "warning http: HTTP_READ_HEADER_TIMEOUT=0"}},
		{config.Map{"APP_ENV": "development", "HTTP_MAX_BODY": "0"}, []string{"ok http"}},
	}
	for _, tt := range tests {
		app := newApp(t, tt.env)
		if _, err := web.NewServer(app); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		app.ExecuteArgs(context.Background(), []string{"doctor"}, &out, &out)
		got := strings.Join(strings.Fields(out.String()), " ")
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("%v: no %q in:\n%s", tt.env, w, out.String())
			}
		}
	}
}
