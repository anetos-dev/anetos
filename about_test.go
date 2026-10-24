// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

type mailSettings struct {
	URL    string `env:"MAIL_SMTP_URL"`
	Token  string `env:"MAIL_API_TOKEN"`
	Driver string `env:"MAIL_DRIVER" default:"log"`
	From   string `env:"MAIL_FROM"`
	// Values with secrets inside, under names that don't say so.
	DB      string `env:"T_DB_URL"`
	KV      string `env:"T_PG"`
	Query   string `env:"T_QUERY_URL"`
	Bad     string `env:"T_BAD_URL"`
	User    string `env:"T_GIT_URL"`
	Redis   string `env:"T_REDIS_URL" default:"redis://:hunter2@localhost:6379"`
	Store   string `env:"T_STORE" was:"T_OLD_STORE"`
	Webhook string `env:"T_SLACK_WEBHOOK"`
}

type aboutMailer struct{}

type mailProvider struct{}

func (mailProvider) Name() string { return "mail" }
func (mailProvider) Register(app *anetos.App) error {
	var s mailSettings
	if err := config.Bind(app.Source(), &s); err != nil {
		return err
	}
	anetos.Provide(app, &aboutMailer{})
	return nil
}
func (mailProvider) Boot(context.Context, *anetos.App) error { return nil }

// about describes the app and the settings it read, secrets hidden.
func TestAbout(t *testing.T) {
	app := newApp(t, config.Map{"APP_ENV": "development", "APP_NAME": "shop", "APP_KEY": "base64:a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s=",
		"MAIL_SMTP_URL": "smtp://ann:hunter2@mail.example.com:587", "MAIL_API_TOKEN": "tok_123",
		"T_DB_URL": "ann:hunter2@tcp(db:3306)/shop", "T_PG": "host=db user=ann password=hunter2",
		"T_QUERY_URL": "postgres://db/shop?password=hunter2&sslmode=disable", "T_BAD_URL": "postgres://ann:hu%zznter2@db/shop",
		"T_GIT_URL": "https://hunter2@github.com/x", "T_OLD_STORE": "database",
		"T_SLACK_WEBHOOK": "https://hooks.slack.com/services/T/B/hunter2"})
	app.Use(mailProvider{})
	code, out, errOut := execute(t, app, "about", "--json")
	if code != 0 {
		t.Fatalf("about --json: %d %s", code, errOut)
	}
	var ab struct {
		Name, Env, Anetos, Go string
		Services              []string
		Settings              map[string]string
	}
	if err := json.Unmarshal([]byte(out), &ab); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if ab.Name != "shop" || ab.Env != "development" || ab.Anetos == "" || !strings.HasPrefix(ab.Go, "go") {
		t.Errorf("about: %+v", ab)
	}
	if !slices.Contains(ab.Services, "*anetos_test.aboutMailer") {
		t.Errorf("services: %v", ab.Services)
	}
	for key, want := range map[string]string{
		"APP_KEY":         "(set)",
		"MAIL_API_TOKEN":  "(set)",
		"MAIL_SMTP_URL":   "smtp://ann:xxxxx@mail.example.com:587",
		"MAIL_DRIVER":     "log (default)",
		"MAIL_FROM":       "(not set)",
		"APP_ENV":         "development",
		"T_DB_URL":        "ann:xxxxx@tcp(db:3306)/shop",
		"T_PG":            "host=db user=ann password=xxxxx",
		"T_QUERY_URL":     "postgres://db/shop?password=xxxxx&sslmode=disable",
		"T_BAD_URL":       "xxxxx",
		"T_GIT_URL":       "https://xxxxx@github.com/x",
		"T_REDIS_URL":     "redis://:xxxxx@localhost:6379 (default)",
		"T_STORE":         "database (from T_OLD_STORE)",
		"T_SLACK_WEBHOOK": "(set)",
	} {
		if got := ab.Settings[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, ok := ab.Settings["T_OLD_STORE"]; ok {
		t.Error("a former name is listed")
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "tok_123") || strings.Contains(out, "a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s=") {
		t.Errorf("a secret is shown:\n%s", out)
	}

	app = newApp(t, config.Map{"APP_ENV": "development"})
	app.Use(mailProvider{})
	code, out, _ = execute(t, app, "about")
	if code != 0 || !strings.HasPrefix(out, "anetos\n") || !strings.Contains(out, "MAIL_DRIVER") || strings.Contains(out, "MAIL_FROM") ||
		!strings.Contains(out, "more read, not set") {
		t.Errorf("about: %d\n%s", code, out)
	}

	code, out, _ = execute(t, newApp(t, config.Map{}), "help", "--json")
	var cmds []struct{ Name, Usage, Description string }
	if err := json.Unmarshal([]byte(out), &cmds); code != 0 || err != nil || !slices.ContainsFunc(cmds, func(c struct{ Name, Usage, Description string }) bool {
		return c.Name == "about" && c.Usage == "[--json]"
	}) || cmds[0].Name != "help" {
		t.Errorf("help --json: %d %v\n%s", code, err, out)
	}
	for _, args := range [][]string{{"help", "--json", "about"}, {"help", "about", "--json"}} {
		code, out, _ = execute(t, newApp(t, config.Map{}), args...)
		if code != 0 || !strings.Contains(out, `"name": "about"`) || strings.HasPrefix(out, "[") {
			t.Errorf("%v: %d\n%s", args, code, out)
		}
	}
}
