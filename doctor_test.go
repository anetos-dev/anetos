// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

var goodKey = "base64:" + strings.Repeat("A", 43)

func doctor(t *testing.T, app *anetos.App, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := app.ExecuteArgs(context.Background(), append([]string{"doctor"}, args...), &out, &errOut)
	return code, out.String() + errOut.String()
}

func TestDoctorClean(t *testing.T) {
	app := newApp(t, config.Map{"APP_ENV": "production", "APP_KEY": goodKey, "APP_URL": "https://blog.acme.io", "APP_NAME": "blog"})
	code, out := doctor(t, app)
	if code != 0 || !strings.Contains(out, "Checking blog (APP_ENV=production).") || !strings.Contains(out, "ok  app") ||
		!strings.Contains(out, "0 problems, 0 warnings.") || strings.Contains(out, "deployment settings") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestDoctorAppSettings(t *testing.T) {
	tests := []struct {
		env  config.Map
		code int
		want []string
	}{
		{config.Map{"APP_ENV": "staging", "APP_DEBUG": "true"}, 0, []string{
			"warning  app: APP_DEBUG=true in staging", "warning  app: APP_KEY isn't set", "warning  app: APP_URL isn't set", "0 problems, 3 warnings."}},
		{config.Map{"APP_ENV": "production", "APP_KEY": goodKey, "APP_URL": "http://example.com"}, 1, []string{
			"problem  app: APP_URL http://example.com is http", "1 problem, 0 warnings."}},
		{config.Map{"APP_ENV": "staging", "APP_KEY": goodKey, "APP_URL": "http://example.com"}, 0, []string{"warning  app: APP_URL http://example.com is http"}},
		{config.Map{"APP_ENV": "production", "APP_KEY": goodKey, "APP_URL": "https://example.com"}, 0, []string{"warning  app: APP_URL https://example.com is an example's"}},
		{config.Map{"APP_ENV": "production", "APP_KEY": goodKey, "APP_URL": "http://localhost:8080"}, 0, []string{"ok  app"}},
		{config.Map{"APP_ENV": "development"}, 0, []string{"ok  app", "deployment settings apply to production and staging"}},
	}
	for _, tt := range tests {
		code, out := doctor(t, newApp(t, tt.env))
		if code != tt.code {
			t.Errorf("%v: exit %d, want %d:\n%s", tt.env, code, tt.code, out)
		}
		for _, w := range tt.want {
			if !strings.Contains(out, w) {
				t.Errorf("%v: no %q in:\n%s", tt.env, w, out)
			}
		}
	}
}

func TestDoctorChecks(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{"APP_ENV": "production", "APP_KEY": goodKey, "APP_URL": "https://blog.acme.io"})
	app.Use(&testProvider{name: "p", rec: rec})
	app.AddCheck(anetos.Check{Name: "billing", Run: func(context.Context) []anetos.Finding {
		return []anetos.Finding{{Severity: anetos.Warning, Message: "test mode"}, {Severity: anetos.Note, Message: "fyi"}}
	}})
	app.AddCheck(anetos.Check{Name: "late", Booted: true, Run: func(ctx context.Context) []anetos.Finding {
		rec.add("check:late")
		return nil
	}})
	app.AddCheck(anetos.Check{Name: "broken", Run: func(context.Context) []anetos.Finding { panic("oops") }})

	code, out := doctor(t, app)
	for _, w := range []string{"warning  billing: test mode", "note     billing: fyi", "problem  broken: the check panicked: oops", "ok       late", "1 problem, 1 warning, 1 note."} {
		if !strings.Contains(out, w) {
			t.Errorf("no %q in:\n%s", w, out)
		}
	}
	if code != 1 || strings.Index(out, "broken") > strings.Index(out, "late") {
		t.Errorf("exit %d, or booted checks not last:\n%s", code, out)
	}
	if got := rec.String(); got != "register:p,boot:p,check:late,close:p" {
		t.Errorf("events %s, want the app booted for the booted check, then closed", got)
	}
}

func TestDoctorStrict(t *testing.T) {
	app := func() *anetos.App { // an app runs one command
		app := newApp(t, config.Map{"APP_ENV": "staging", "APP_KEY": goodKey, "APP_URL": "https://blog.acme.io"})
		app.AddCheck(anetos.Check{Name: "x", Run: func(context.Context) []anetos.Finding {
			return []anetos.Finding{{Severity: anetos.Warning, Message: "hm"}}
		}})
		return app
	}
	if code, out := doctor(t, app()); code != 0 {
		t.Errorf("without --strict: exit %d:\n%s", code, out)
	}
	if code, out := doctor(t, app(), "--strict"); code != 1 || !strings.Contains(out, "0 problems, 1 warning") {
		t.Errorf("--strict: exit %d:\n%s", code, out)
	}
	if code, _ := doctor(t, app(), "extra"); code != 2 {
		t.Errorf("an argument: exit %d, want 2", code)
	}
}

func TestDoctorBootFails(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{"APP_ENV": "production", "APP_KEY": goodKey, "APP_URL": "https://blog.acme.io"})
	app.Use(&testProvider{name: "db", rec: rec, bootErr: errors.New("connection refused")})
	app.AddCheck(anetos.Check{Name: "migrations", Booted: true, Run: func(context.Context) []anetos.Finding {
		t.Error("a booted check ran without a booted app")
		return nil
	}})
	code, out := doctor(t, app)
	if code != 1 || !strings.Contains(out, "ok       app") ||
		!strings.Contains(out, "problem  boot: the app doesn't boot, so 1 check(s) didn't run:") || !strings.Contains(out, "connection refused") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestAddCheckPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("AddCheck without Run didn't panic")
		}
	}()
	newApp(t, config.Map{}).AddCheck(anetos.Check{Name: "x"})
}

func TestSeverityString(t *testing.T) {
	if anetos.Note.String() != "note" || anetos.Problem.String() != "problem" || anetos.Severity(9).String() != "Severity(9)" {
		t.Error("Severity.String")
	}
}

// A check a provider adds while the app boots (a plugin's) runs too,
// and an app booted before doctor (a test's) stays open.
func TestDoctorLateChecksAndBootedApp(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{"APP_ENV": "production", "APP_KEY": goodKey, "APP_URL": "https://blog.acme.io"})
	app.Use(&testProvider{name: "plugin", rec: rec, boot: func(_ context.Context, a *anetos.App) error {
		a.AddCheck(anetos.Check{Name: "plugin", Run: func(context.Context) []anetos.Finding {
			return []anetos.Finding{{Severity: anetos.Problem, Message: "late"}}
		}})
		return nil
	}})
	app.AddCheck(anetos.Check{Name: "panics", Booted: true, Run: func(context.Context) []anetos.Finding { panic("boom") }})
	if err := app.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, out := doctor(t, app)
	if code != 1 || !strings.Contains(out, "problem  plugin: late") || !strings.Contains(out, "problem  panics: the check panicked: boom") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if strings.Contains(rec.String(), "close:") {
		t.Errorf("doctor closed an app it didn't boot: %s", rec.String())
	}
	if err := app.Close(); err != nil || rec.String() != "register:plugin,boot:plugin,close:plugin" {
		t.Errorf("Close: %v, events %s", err, rec.String())
	}
}
