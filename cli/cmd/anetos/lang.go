// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"anetos.dev/anetos/cli/internal/scaffold"
)

// localesModule holds the translations locale:add copies, a folder per
// locale (github.com/anetos-dev/locales).
const localesModule = "anetos.dev/locales"

const localeAddUsage = `Usage: anetos locale:add [--from dir] [--version v] [--force] <locale>...

Copies the translations of Anetos's own messages for each locale (bn, fr,
es…) from ` + localesModule + ` into locales/<locale>/: framework.yaml
(validation messages, error pages, date and number formats) and, if the
app has make:auth's pages (locales/en/auth.yaml), auth.yaml. The files
are then the app's. Keys the app's catalogs for the locale already
define are left out, and files the app has are kept unless --force.
With no locale, it lists the locales available.
`

// localeAdd runs anetos locale:add.
func localeAdd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos locale:add", flag.ContinueOnError)
	from := fs.String("from", "", "a checkout of "+localesModule+" to copy from, instead of downloading it")
	version := fs.String("version", "latest", "the version of "+localesModule+" to download")
	force := fs.Bool("force", false, "replace files the app already has")
	wanted, code := parse(fs, args, stderr, localeAddUsage)
	if code >= 0 {
		return code
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "anetos locale:add:", err)
		return 1
	}
	var dest string
	if len(wanted) > 0 { // check the app before downloading
		wd, err := os.Getwd()
		if err != nil {
			return fail(err)
		}
		root, err := scaffold.FindRoot(wd)
		if err != nil {
			return fail(err)
		}
		dest = filepath.Join(root, "locales")
		if st, err := os.Stat(dest); err != nil || !st.IsDir() {
			return fail(errors.New("the app has no locales folder: see the Translations guide to add one"))
		}
	}
	src := *from
	if src == "" {
		dir, v, err := downloadModule(ctx, localesModule, *version)
		if err != nil {
			return fail(err)
		}
		src = dir
		fmt.Fprintf(stdout, "Using %s %s.\n", localesModule, v)
	}
	available, err := localeDirs(src)
	if err != nil {
		return fail(err)
	}
	if len(wanted) == 0 {
		fmt.Fprintf(stdout, "Available: %s\n", strings.Join(available, ", "))
		return 0
	}
	var locales []string
	for _, want := range wanted { // all of them, before writing anything
		locale, ok := matchLocale(available, want)
		if !ok {
			return fail(fmt.Errorf("no translations for %q: available: %s (contribute one at https://github.com/anetos-dev/locales)", want, strings.Join(available, ", ")))
		}
		if !slices.Contains(locales, locale) {
			locales = append(locales, locale)
		}
	}
	_, err = os.Stat(filepath.Join(dest, "en", "auth.yaml"))
	withAuth := err == nil
	for _, locale := range locales {
		files := []string{"framework.yaml"}
		if withAuth {
			files = append(files, "auth.yaml")
		}
		if err := os.MkdirAll(filepath.Join(dest, locale), 0o755); err != nil {
			return fail(err)
		}
		for _, name := range files {
			if err := addLocaleFile(stdout, src, dest, locale, name, *force); err != nil {
				return fail(err)
			}
		}
	}
	fmt.Fprintf(stdout, `Next:
  go run . locale:check   what your own text still needs in %s
Visitors can choose these languages now (unless APP_LOCALES lists the
supported ones): your own text shows in APP_FALLBACK_LOCALE until you
translate it.
`, strings.Join(locales, ", "))
	return 0
}

// addLocaleFile copies src/<locale>/<name> to dest/<locale>/<name>,
// leaving out the keys the app's other catalogs for the locale define
// (one locale's catalogs can't define a key twice).
func addLocaleFile(stdout io.Writer, src, dest, locale, name string, force bool) error {
	data, err := os.ReadFile(filepath.Join(src, locale, name))
	if err != nil {
		return err
	}
	target := filepath.Join(dest, locale, name)
	rel := filepath.ToSlash(filepath.Join("locales", locale, name))
	have, err := catalogKeys(dest, locale, target)
	if err != nil {
		return err
	}
	data, left, err := withoutKeys(data, have)
	if err != nil {
		return fmt.Errorf("%s/%s: %w", locale, name, err)
	}
	cur, err := os.ReadFile(target)
	switch {
	case err == nil && bytes.Equal(cur, data):
		fmt.Fprintf(stdout, "%s is up to date.\n", rel)
		return nil
	case err == nil && !force:
		fmt.Fprintf(stdout, "%s exists: kept (--force replaces it).\n", rel)
		return nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return err
	}
	if len(left) > 0 {
		slices.Sort(left)
		shown := left
		more := ""
		if len(left) > 10 {
			shown, more = left[:10], fmt.Sprintf(" and %d more", len(left)-10)
		}
		fmt.Fprintf(stdout, "Wrote %s, without %d key(s) your catalogs for %s define: %s%s.\n", rel, len(left), locale, strings.Join(shown, ", "), more)
	} else {
		fmt.Fprintf(stdout, "Wrote %s.\n", rel)
	}
	return nil
}

// catalogKeys returns the keys the app's catalogs for locale define
// (locales/<locale>.yaml and the YAML files under locales/<locale>/),
// except the file skip.
func catalogKeys(dest, locale, skip string) (map[string]bool, error) {
	keys := map[string]bool{}
	var files []string
	for _, ext := range []string{".yaml", ".yml"} {
		if p := filepath.Join(dest, locale+ext); fileExists(p) {
			files = append(files, p)
		}
	}
	err := filepath.WalkDir(filepath.Join(dest, locale), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if ext := filepath.Ext(p); !d.IsDir() && (ext == ".yaml" || ext == ".yml") && p != skip {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if len(doc.Content) > 0 {
			flattenKeys(doc.Content[0], "", keys)
		}
	}
	return keys, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// pluralForms are the CLDR plural categories: a map of them (with
// "other") is one plural message, not a group of keys.
var pluralForms = []string{"zero", "one", "two", "few", "many", "other"}

func isPlural(n *yaml.Node) bool {
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return false
	}
	other := false
	for i := 0; i < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if !slices.Contains(pluralForms, k) || n.Content[i+1].Kind != yaml.ScalarNode {
			return false
		}
		other = other || k == "other"
	}
	return other
}

func joinKey(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// flattenKeys adds the dotted keys of the messages under n to keys.
func flattenKeys(n *yaml.Node, prefix string, keys map[string]bool) {
	if n.Kind != yaml.MappingNode || isPlural(n) {
		if prefix != "" {
			keys[prefix] = true
		}
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		flattenKeys(n.Content[i+1], joinKey(prefix, n.Content[i].Value), keys)
	}
}

// withoutKeys returns a catalog without the messages whose keys are in
// drop, and those keys; data unchanged when there are none.
func withoutKeys(data []byte, drop map[string]bool) ([]byte, []string, error) {
	if len(drop) == 0 {
		return data, nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 {
		return data, nil, nil
	}
	var dropped []string
	var prune func(n *yaml.Node, prefix string)
	prune = func(n *yaml.Node, prefix string) {
		var kept []*yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			key := joinKey(prefix, k.Value)
			if drop[key] {
				dropped = append(dropped, key)
				continue
			}
			if v.Kind == yaml.MappingNode && !isPlural(v) {
				prune(v, key)
				if len(v.Content) == 0 {
					continue
				}
			}
			kept = append(kept, k, v)
		}
		n.Content = kept
	}
	prune(doc.Content[0], "")
	if len(dropped) == 0 {
		return data, nil, nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, nil, err
	}
	return out.Bytes(), dropped, nil
}

// matchLocale finds want among the available folders, ignoring case;
// a regional locale (bn-BD) gets its language's (bn) when the region has
// none.
func matchLocale(available []string, want string) (string, bool) {
	for w := want; w != ""; {
		if i := slices.IndexFunc(available, func(l string) bool { return strings.EqualFold(l, w) }); i >= 0 {
			return available[i], true
		}
		cut := strings.LastIndexAny(w, "-_")
		if cut < 0 {
			break
		}
		w = w[:cut]
	}
	return "", false
}

// localeDirs returns the locale folders of a checkout of the locales
// module: those with a framework.yaml (symbolic links followed).
func localeDirs(src string) ([]string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if fileExists(filepath.Join(src, e.Name(), "framework.yaml")) {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s has no locale folders (folders with a framework.yaml)", src)
	}
	return out, nil
}

// downloadModule downloads mod@version into the module cache and returns
// its folder and version. It runs outside any module and workspace, so
// the app's go.mod stays as it is.
func downloadModule(ctx context.Context, mod, version string) (dir, v string, err error) {
	tmp, err := os.MkdirTemp("", "anetos-lang-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	var out, errOut bytes.Buffer
	c := exec.CommandContext(ctx, "go", "mod", "download", "-json", mod+"@"+version)
	c.Dir = tmp
	c.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Run(); err != nil {
		var info struct{ Error string }
		if json.Unmarshal(out.Bytes(), &info) == nil && info.Error != "" {
			return "", "", fmt.Errorf("downloading %s@%s: %s (with no network, use --from)", mod, version, info.Error)
		}
		return "", "", fmt.Errorf("downloading %s@%s: %w\n%s", mod, version, err, strings.TrimSpace(errOut.String()))
	}
	var info struct{ Dir, Version string }
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		return "", "", fmt.Errorf("downloading %s: %w", mod, err)
	}
	return info.Dir, info.Version, nil
}
