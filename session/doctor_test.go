// SPDX-License-Identifier: Apache-2.0

package session

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

func TestDoctorSession(t *testing.T) {
	const key = "base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	tests := []struct {
		env  config.Map
		want []string
	}{
		{config.Map{"APP_ENV": "production"}, []string{"ok session"}},
		{config.Map{"APP_ENV": "development"}, []string{"ok session"}}, // not Secure by default there
		{config.Map{"APP_ENV": "development", "SESSION_SECURE": "false"}, []string{"ok session"}},
		{config.Map{"APP_ENV": "production", "SESSION_SECURE": "false"}, []string{"problem session: SESSION_SECURE=false in production"}},
		{config.Map{"APP_ENV": "staging", "SESSION_SECURE": "false"}, []string{"problem session: SESSION_SECURE=false in staging"}},
		{config.Map{"APP_ENV": "production", "SESSION_SAME_SITE": "none"}, []string{"warning session: SESSION_SAME_SITE=none"}},
		{config.Map{"APP_ENV": "production", "SESSION_DOMAIN": "example.com"}, []string{"note session: SESSION_DOMAIN=example.com"}},
	}
	for _, tt := range tests {
		tt.env["APP_KEY"] = key
		app, err := anetos.New(anetos.WithSource(tt.env), anetos.WithLogger(slog.New(slog.DiscardHandler)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := New(app); err != nil {
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
