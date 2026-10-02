// SPDX-License-Identifier: Apache-2.0

package anetostest_test

import (
	"net/http"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/web"
)

// askApp answers GET /ask?q=… with the model's answer.
func askApp(app *anetos.App) (*web.Server, error) {
	if _, err := ai.ForApp(app); err != nil {
		return nil, err
	}
	return askRoutes(app)
}

func askRoutes(app *anetos.App) (*web.Server, error) {
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	srv.Router().Get("/ask", func(c *web.Ctx) error {
		res, err := ai.Generate(c, c.Query("q"))
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, res.Text())
	})
	return srv, nil
}

func TestFakeAI(t *testing.T) {
	app := anetostest.New(t, askApp, anetostest.FakeAI(ai.FakeText("Paris.")), anetostest.FakeAI(ai.FakeText("Rome.")))
	app.AssertNotPrompted()
	app.Get("/ask?q=France").AssertOK().AssertSee("Paris.")
	app.Get("/ask?q=Italy").AssertOK().AssertSee("Rome.")
	app.AssertPrompted(func(r ai.Request) bool { return r.Prompt() == "Italy" })
	app.AssertPrompted(nil)
	if n := len(app.AI().Requests()); n != 2 {
		t.Errorf("%d requests", n)
	}
	// No reply left: the request fails.
	app.Get("/ask?q=Spain").AssertStatus(http.StatusInternalServerError)
	app.AI().Add(ai.FakeText("Madrid."))
	app.Get("/ask?q=Spain").AssertOK().AssertSee("Madrid.")
}

func TestFakeAIByDefault(t *testing.T) {
	// AI_PROVIDER=fake is forced: no model is called, even without FakeAI.
	t.Setenv("AI_PROVIDER", "anthropic")
	app := anetostest.New(t, askApp)
	app.AI().Add(ai.FakeText("Yes."))
	app.Get("/ask?q=Fake?").AssertOK().AssertSee("Yes.")
	if !strings.Contains(app.AI().Requests()[0].Prompt(), "Fake?") {
		t.Errorf("requests: %+v", app.AI().Requests())
	}
}

func TestAssertPromptedFails(t *testing.T) {
	ft := &fakeT{TB: t}
	app := anetostest.New(ft, askApp, anetostest.FakeAI(ai.FakeText("Paris.")))
	app.AssertPrompted(nil)
	app.Get("/ask?q=France")
	app.AssertNotPrompted()
	app.AssertPrompted(func(r ai.Request) bool { return r.Prompt() == "Italy" })
	if len(ft.errs) != 3 || !strings.Contains(ft.errs[2], `the last prompt was "France"`) {
		t.Errorf("errors: %q", ft.errs)
	}
}

func TestFakeAIWithoutClient(t *testing.T) {
	ft := &fakeT{TB: t}
	msg := fatalOf(func() { anetostest.New(ft, nil, anetostest.FakeAI(ai.FakeText("x"))) })
	if !strings.Contains(msg, "the app has no AI client") {
		t.Errorf("FakeAI without ai.ForApp: %q", msg)
	}
	msg = fatalOf(func() { anetostest.New(ft, nil).AI() })
	if !strings.Contains(msg, "isn't the fake") {
		t.Errorf("AI without ai.ForApp: %q", msg)
	}
}

// liveProvider stands in for a real provider.
type liveProvider struct{ *ai.Fake }

func TestFakeAIOverEnv(t *testing.T) {
	// A test that sets another provider with Env uses it, unless FakeAI
	// replaces it with the fake.
	live := liveProvider{ai.NewFake(ai.FakeText("live"))}
	liveApp := func(app *anetos.App) (*web.Server, error) {
		if _, err := ai.ForApp(app, ai.Driver{Name: "live", Open: func(*anetos.App, ai.Config) (ai.Provider, error) { return live, nil }}); err != nil {
			return nil, err
		}
		return askRoutes(app)
	}
	env := anetostest.Env(map[string]string{"AI_PROVIDER": "live"})
	app := anetostest.New(t, liveApp, env, anetostest.FakeAI(ai.FakeText("faked")))
	app.Get("/ask?q=x").AssertOK().AssertSee("faked")
	if len(live.Requests()) != 0 {
		t.Error("the live provider was called")
	}
	ft := &fakeT{TB: t}
	app = anetostest.New(ft, liveApp, env)
	if msg := fatalOf(func() { app.AI() }); !strings.Contains(msg, "isn't the fake") {
		t.Errorf("AI with a live provider: %q", msg)
	}
}

func TestFakeEmbeddingsByDefault(t *testing.T) {
	// The fake makes embeddings too, whatever AI_EMBEDDING_PROVIDER says.
	t.Setenv("AI_EMBEDDING_PROVIDER", "openai")
	t.Setenv("AI_EMBEDDING_MODEL", "text-embedding-3-small")
	app := anetostest.New(t, func(app *anetos.App) (*web.Server, error) {
		if _, err := ai.ForApp(app); err != nil {
			return nil, err
		}
		return web.NewServer(app)
	})
	vs, err := ai.Embed(app.Context(), 8, "cats", "dogs")
	if err != nil || len(vs) != 2 || len(vs[0]) != 8 {
		t.Fatalf("Embed = %v, %v", vs, err)
	}
	if got := app.AI().Embeddings(); len(got) != 1 || got[0].Model != "text-embedding-3-small" || len(got[0].Inputs) != 2 {
		t.Errorf("embedding requests: %+v", got)
	}
}
