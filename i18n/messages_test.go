// SPDX-License-Identifier: Apache-2.0

package i18n_test

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos/i18n"
)

// Messages: a locale's messages under a prefix, without it, with the
// fallback's for the keys the locale lacks; plurals as their forms.
func TestMessages(t *testing.T) {
	files := fstest.MapFS{
		"en/app.yaml": {Data: []byte(`ui:
  home:
    title: "Home"
    welcome: "Welcome, {name}"
  posts:
    count:
      one: "{count} post"
      other: "{count} posts"
nav:
  home: "Home"
`)},
		"bn/app.yaml": {Data: []byte(`ui:
  home:
    title: "হোম"
`)},
	}
	tr, err := i18n.NewTranslator(i18n.Config{Locale: "en", Fallback: "en", Strategy: i18n.StrategyNone}, i18n.WithLocales(files))
	if err != nil {
		t.Fatal(err)
	}
	ctx := i18n.WithTranslator(context.Background(), tr)
	want := map[string]any{
		"home.title":   "Home",
		"home.welcome": "Welcome, {name}",
		"posts.count":  map[string]string{"one": "{count} post", "other": "{count} posts"},
	}
	if got := i18n.Messages(ctx, "ui"); !reflect.DeepEqual(got, want) {
		t.Errorf("en: %v", got)
	}
	want["home.title"] = "হোম"
	if got := i18n.Messages(i18n.WithLocale(ctx, "bn"), "ui."); !reflect.DeepEqual(got, want) {
		t.Errorf("bn: %v", got)
	}
	if got := i18n.Messages(ctx, "none"); len(got) != 0 {
		t.Errorf("none: %v", got)
	}
	// All of the app's, and none of the framework's (validation's).
	all := i18n.Messages(ctx, "")
	if all["nav.home"] != "Home" || all["ui.home.title"] != "Home" || len(all) != 4 {
		t.Errorf("all: %v", all)
	}
}
