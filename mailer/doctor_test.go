// SPDX-License-Identifier: Apache-2.0

package mailer_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/mailer"
)

func TestDoctorMail(t *testing.T) {
	tests := []struct {
		env  config.Map
		want string
	}{
		{config.Map{"APP_ENV": "development"}, "ok mail"},
		{config.Map{"APP_ENV": "production"}, "warning mail: MAIL_DRIVER=log in production: emails aren't sent"},
		{config.Map{"APP_ENV": "staging", "MAIL_DRIVER": "memory"}, "warning mail: MAIL_DRIVER=memory in staging"},
		{config.Map{"APP_ENV": "production", "MAIL_DRIVER": "smtp", "MAIL_SMTP_URL": "smtp://mail.example.com"}, "warning mail: MAIL_FROM_ADDRESS isn't set"},
		{config.Map{"APP_ENV": "production", "MAIL_DRIVER": "smtp", "MAIL_FROM_ADDRESS": "a@example.com", "MAIL_SMTP_URL": "smtp://mail.example.com"}, "ok mail"},
		{config.Map{"APP_ENV": "production", "MAIL_DRIVER": "smtp", "MAIL_FROM_ADDRESS": "a@example.com", "MAIL_SMTP_URL": "smtp://10.0.0.5:25?tls=none"}, "warning mail: MAIL_SMTP_URL has tls=none for 10.0.0.5"},
		{config.Map{"APP_ENV": "production", "MAIL_DRIVER": "smtp", "MAIL_FROM_ADDRESS": "a@example.com", "MAIL_SMTP_URL": "smtp://localhost:25?tls=none"}, "ok mail"},
	}
	for _, tt := range tests {
		app, err := anetos.New(anetos.WithSource(tt.env), anetos.WithLogger(slog.New(slog.DiscardHandler)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mailer.ForApp(app); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		app.ExecuteArgs(context.Background(), []string{"doctor"}, &out, &out)
		if got := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(got, tt.want) {
			t.Errorf("%v: no %q in:\n%s", tt.env, tt.want, out.String())
		}
	}
}
