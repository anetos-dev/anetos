// SPDX-License-Identifier: Apache-2.0

// Command i18n shows Anetos's translations: catalogs in locales/ (English
// and Bangla), translated and pluralized messages, dates, prices and
// relative times in the visitor's language, the visitor's locale
// (?locale=, the cookie, the browser's languages), a language switcher,
// and validation messages in the visitor's language.
//
//	go run ./examples/i18n            # http://localhost:8080
//	LOCALE_URL=prefix go run ./examples/i18n   # /bn/ for Bangla
//	go run ./examples/i18n lang:check # what Bangla is missing
package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/i18n/locales"
)

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute()
}

// region: setup
// setup loads the catalogs and adds the routes. The HTTP server finds
// each request's locale with the app's translator.
func setup(app *anetos.App) (*web.Server, error) {
	// locales.FS embeds locales/: en.yaml and bn/*.yaml.
	if _, err := i18n.New(app, locales.FS); err != nil {
		return nil, err
	}
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	r := srv.Router()
	r.Get("/", home).Name("home")
	r.Post("/signup", web.H(signUp)).Name("signup")
	return srv, nil
}

// endregion

// region: home
// home greets the visitor in their language: ?name= and ?plants= fill
// the messages. Dates, prices and times follow the language too.
func home(c *web.Ctx) error {
	name := c.Query("name")
	if name == "" {
		name = "Ada"
	}
	plants, _ := strconv.Atoi(c.Query("plants"))
	now := anetos.Now(c)
	return c.Render(http.StatusOK, view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, `<!doctype html><html lang="%s" dir="%s"><title>%s</title><h1>%s</h1><p>%s</p><p>%s</p><p>%s</p><p>%s</p>%s`,
			i18n.Locale(ctx), i18n.Dir(ctx),
			html.EscapeString(i18n.T(ctx, "app.title")),
			html.EscapeString(i18n.T(ctx, "home.welcome", "name", name)),
			html.EscapeString(i18n.Plural(ctx, "home.plants", plants)),
			html.EscapeString(i18n.T(ctx, "home.today", "date", i18n.Date(ctx, now, i18n.Full))),
			html.EscapeString(i18n.T(ctx, "home.price", "price", i18n.Currency(ctx, 1250, "BDT"))),
			html.EscapeString(i18n.T(ctx, "home.watered", "when", i18n.Ago(ctx, now.Add(-3*time.Hour)))),
			switcher(ctx))
		return err
	}))
}

// switcher links to this page in each language the app supports, by the
// language's own name.
func switcher(ctx context.Context) string {
	var b strings.Builder
	b.WriteString("<nav>" + html.EscapeString(i18n.T(ctx, "nav.language")) + ":")
	for _, l := range i18n.From(ctx).Supported() {
		fmt.Fprintf(&b, ` <a href="%s" hreflang="%s">%s</a>`, html.EscapeString(web.LocaleURL(ctx, l)), l, html.EscapeString(i18n.LanguageName(ctx, l)))
	}
	b.WriteString("</nav>")
	return b.String()
}

// endregion

// region: signup
// SignUp is the input of POST /signup. Its messages come from the
// catalogs: validation.required, and the labels in validation.attributes.
type SignUp struct {
	Name       string `json:"name" validate:"required|max:100"`
	Email      string `json:"email" validate:"required|email"`
	PlantCount int    `json:"plant_count" validate:"min:1"`
}

func signUp(c *web.Ctx, in SignUp) (map[string]string, error) {
	return map[string]string{"message": i18n.T(c, "signup.done", "name", in.Name)}, nil
}

// endregion
