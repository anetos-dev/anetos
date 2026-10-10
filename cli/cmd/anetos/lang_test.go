// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLangAdd(t *testing.T) {
	src := t.TempDir()
	writeFiles(t, src, map[string]string{
		"bn/framework.yaml": "validation: {required: \"{label} দিতে হবে।\"}\n",
		"bn/auth.yaml":      "auth: {login: {title: \"লগ ইন\"}}\n",
		"fr/framework.yaml": "validation: {required: \"obligatoire\"}\n",
		"fr/auth.yaml":      "auth: {}\n",
		"es/framework.yaml": "x: \"y\"\n",
		"README.md":         "not a locale",
		".github/x.yaml":    "",
	})
	app := t.TempDir()
	writeFiles(t, app, map[string]string{
		"go.mod":              "module example.com/app\n\ngo 1.26\n",
		"locales/en/app.yaml": "home: {title: \"Home\"}\n",
	})
	t.Chdir(app)
	langAdd := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := run(append([]string{"locale:add", "-from", src}, args...), &out, &errOut)
		return code, out.String() + errOut.String()
	}

	if code, out := langAdd(); code != 0 || !strings.Contains(out, "Available: bn, es, fr") {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, out := langAdd("BN"); code != 0 || !strings.Contains(out, "Wrote locales/bn/framework.yaml.") {
		t.Fatalf("add: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(app, "locales/bn/auth.yaml")); err == nil {
		t.Error("auth.yaml copied to an app without make:auth's pages")
	}

	// With make:auth's pages, auth.yaml too; the app's changes are kept.
	writeFiles(t, app, map[string]string{
		"locales/en/auth.yaml":      "auth: {login: {title: \"Log in\"}}\n",
		"locales/bn/framework.yaml": "validation: {required: \"mine\"}\n",
	})
	code, out := langAdd("bn")
	if code != 0 || !strings.Contains(out, "locales/bn/framework.yaml exists: kept") || !strings.Contains(out, "Wrote locales/bn/auth.yaml.") {
		t.Fatalf("again: %d %s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "locales/bn/framework.yaml")); !strings.Contains(string(b), "mine") {
		t.Error("the app's file was replaced")
	}
	if code, out := langAdd("-force", "bn"); code != 0 || !strings.Contains(out, "Wrote locales/bn/framework.yaml.") || !strings.Contains(out, "locales/bn/auth.yaml is up to date.") {
		t.Fatalf("-force: %d %s", code, out)
	}

	// Keys the app's catalogs for the locale define are left out: one
	// locale's catalogs can't define a key twice.
	writeFiles(t, src, map[string]string{
		"fr/framework.yaml": "# French\nvalidation:\n  required: \"obligatoire\"\n  email: \"e-mail\"\nposts: {one: \"{count} billet\", other: \"{count} billets\"}\n",
	})
	writeFiles(t, app, map[string]string{"locales/fr/mine.yaml": "validation:\n  required: \"à remplir\"\nposts: {one: \"a\", other: \"b\"}\n"})
	if code, out := langAdd("fr-CA"); code != 0 || !strings.Contains(out, "Wrote locales/fr/framework.yaml, without 2 key(s) your catalogs for fr define: posts, validation.required.") {
		t.Fatalf("overlap: %d %s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "locales/fr/framework.yaml")); strings.Contains(string(b), "obligatoire") || strings.Contains(string(b), "billet") || !strings.Contains(string(b), "email: \"e-mail\"") {
		t.Errorf("framework.yaml:\n%s", b)
	}

	// The alias, and an unknown locale (nothing written for the others).
	var o, e bytes.Buffer
	if code := run([]string{"add", "lang", "-from", src, "es", "de"}, &o, &e); code != 1 || !strings.Contains(e.String(), `no translations for "de": available: bn, es, fr`) {
		t.Errorf("unknown locale: %d %s", code, e.String())
	}
	if _, err := os.Stat(filepath.Join(app, "locales/es")); err == nil {
		t.Error("es written before the unknown locale was refused")
	}

	// An app without a locales folder.
	bare := t.TempDir()
	writeFiles(t, bare, map[string]string{"go.mod": "module example.com/bare\n\ngo 1.26\n"})
	t.Chdir(bare)
	if code, out := langAdd("bn"); code != 1 || !strings.Contains(out, "no locales folder") {
		t.Errorf("no locales folder: %d %s", code, out)
	}
}
