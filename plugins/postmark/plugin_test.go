// SPDX-License-Identifier: Apache-2.0

package postmark_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/ext"
	"anetos.dev/anetos/plugins/postmark"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/web"
)

// region: test-setup
func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	if _, err := migrate.New(app, nil); err != nil {
		return nil, err
	}
	if _, err := queue.New(app); err != nil {
		return nil, err
	}
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	return srv, ext.Load(app, []ext.Plugin{postmark.Plugin()})
}

// endregion

var env = anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync", "POSTMARK_WEBHOOK_USER": "pm", "POSTMARK_WEBHOOK_PASSWORD": "s3cret"})

func webhook(t *testing.T, app *anetostest.App, user, pass, body string) *anetostest.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/postmark/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	return app.Do(req)
}

func run(t *testing.T, app *anetostest.App, name string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	for _, c := range app.Commands() {
		if c.Name == name {
			err := c.Run(app.Context(), &cmd.Args{Name: name, Args: args, Stdout: &out, Stderr: &out})
			return out.String(), err
		}
	}
	t.Fatalf("no command %s", name)
	return "", nil
}

func TestWebhooks(t *testing.T) {
	app := anetostest.New(t, setup, env)
	ctx := app.Context()

	webhook(t, app, "pm", "wrong", `{"RecordType":"Bounce","Email":"a@example.com","Inactive":true}`).AssertStatus(http.StatusUnauthorized)
	webhook(t, app, "", "", `{}`).AssertStatus(http.StatusUnauthorized)
	webhook(t, app, "pm", "s3cret", `not json`).AssertStatus(http.StatusBadRequest)

	webhook(t, app, "pm", "s3cret", `{"RecordType":"Bounce","Email":"Ada@Example.com","Inactive":true,"Type":"HardBounce","MessageID":"m1"}`).AssertNoContent()
	webhook(t, app, "pm", "s3cret", `{"RecordType":"Bounce","Email":"soft@example.com","Inactive":false,"Type":"SoftBounce"}`).AssertNoContent()
	webhook(t, app, "pm", "s3cret", `{"RecordType":"SpamComplaint","Email":"spam@example.com","Inactive":true}`).AssertNoContent()
	webhook(t, app, "pm", "s3cret", `{"RecordType":"Delivery","Recipient":"x@example.com"}`).AssertNoContent()
	for email, want := range map[string]bool{"ada@example.com": true, "soft@example.com": false, "spam@example.com": true, "x@example.com": false} {
		if got, err := postmark.Suppressed(ctx, email); err != nil || got != want {
			t.Errorf("Suppressed(%q) = %v, %v", email, got, err)
		}
	}
	if got, err := postmark.Suppressed(ctx, " ADA@example.com "); err != nil || !got {
		t.Errorf("Suppressed with other case and spaces = %v, %v", got, err)
	}
	// A late soft bounce doesn't lift a complaint's suppression.
	webhook(t, app, "pm", "s3cret", `{"RecordType":"Bounce","Email":"spam@example.com","Inactive":false,"Type":"Transient"}`).AssertNoContent()
	if got, _ := postmark.Suppressed(ctx, "spam@example.com"); !got {
		t.Error("a soft bounce lifted a suppression")
	}
	// Addresses that can't be one are ignored; huge bodies refused.
	webhook(t, app, "pm", "s3cret", `{"RecordType":"Bounce","Email":"`+strings.Repeat("a", 250)+`@example.com","Inactive":true}`).AssertNoContent()
	webhook(t, app, "pm", "s3cret", `{"RecordType":"Bounce","Email":"  ","Inactive":true}`).AssertNoContent()
	webhook(t, app, "pm", "s3cret", `{"RecordType":"Bounce","Email":"`+strings.Repeat("a", 2<<20)+`"}`).AssertStatus(http.StatusRequestEntityTooLarge)
	if out, _ := run(t, app, "postmark:suppressions"); strings.Count(out, "\n") != 3 { // the header, ada and spam
		t.Errorf("postmark:suppressions:\n%s", out)
	}
	// A subscription change suppresses, then reactivates.
	webhook(t, app, "pm", "s3cret", `{"RecordType":"SubscriptionChange","Recipient":"news@example.com","SuppressSending":true,"SuppressionReason":"ManualSuppression"}`).AssertNoContent()
	if got, _ := postmark.Suppressed(ctx, "news@example.com"); !got {
		t.Error("an unsubscribe didn't suppress")
	}
	webhook(t, app, "pm", "s3cret", `{"RecordType":"SubscriptionChange","Recipient":"news@example.com","SuppressSending":false}`).AssertNoContent()
	if got, _ := postmark.Suppressed(ctx, "news@example.com"); got {
		t.Error("a resubscribe didn't reactivate")
	}

	out, err := run(t, app, "postmark:suppressions")
	if err != nil || !strings.Contains(out, "ada@example.com") || !strings.Contains(out, "HardBounce") || strings.Contains(out, "soft@") {
		t.Errorf("postmark:suppressions: %v\n%s", err, out)
	}
	if _, err := run(t, app, "postmark:unsuppress", " ADA@example.com "); err != nil {
		t.Error(err)
	}
	if got, _ := postmark.Suppressed(ctx, "ada@example.com"); got {
		t.Error("unsuppress didn't remove the address")
	}
	if _, err := run(t, app, "postmark:unsuppress", "nobody@example.com"); err == nil {
		t.Error("unsuppress of an address not in the list: no error")
	}
	if _, err := run(t, app, "postmark:unsuppress"); err == nil {
		t.Error("unsuppress without an address: no error")
	}
	out, _ = run(t, app, "plugin:list")
	if !strings.Contains(out, "postmark") || !strings.Contains(out, "/postmark") || !strings.Contains(out, "config, migrations, commands, jobs, routes") {
		t.Errorf("plugin:list:\n%s", out)
	}
}

// Without credentials, the webhook refuses everything.
func TestWebhookWithoutCredentials(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))
	webhook(t, app, "", "", `{"RecordType":"Bounce","Email":"a@example.com","Inactive":true}`).AssertStatus(http.StatusUnauthorized)
	if out, _ := run(t, app, "postmark:suppressions"); out != "No suppressed addresses.\n" {
		t.Errorf("postmark:suppressions = %q", out)
	}
}
