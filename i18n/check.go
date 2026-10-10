// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"

	"anetos.dev/anetos/cmd"
)

// checkCommand is locale:check.
func (tr *Translator) checkCommand(fs.FS) cmd.Command {
	return cmd.Command{
		Name:        "locale:check",
		Former:      []string{"lang:check"},
		Usage:       "[dir]",
		Description: "Check the translations: missing keys, placeholders, plural forms, and keys the source in dir (default .) uses",
		ManagesApp:  true, // reads files only: no database needed
		Run: func(_ context.Context, args *cmd.Args) error {
			fl := flag.NewFlagSet("locale:check", flag.ContinueOnError)
			if err := args.Parse(fl); err != nil {
				return err
			}
			dir := "."
			switch fl.NArg() {
			case 0:
			case 1:
				dir = fl.Arg(0)
			default:
				return cmd.Usagef("unexpected argument %q", fl.Arg(1))
			}
			used, err := sourceKeys(dir)
			if err != nil {
				return err
			}
			if n := tr.Check(args.Stdout, used); n > 0 {
				return fmt.Errorf("locale:check: %d problem(s)", n)
			}
			return nil
		},
	}
}

// Check writes a report of the catalogs' problems to w and returns how
// many there are: keys of the fallback locale's catalog missing from a
// supported locale, placeholders that differ from the fallback's, plural
// forms a language needs that a message lacks, lists of month and day
// names of the wrong length, and keys in used (keys the source uses)
// that no catalog defines; a used key ending in "*" is a prefix
// ("issues.status.*", from i18n.T(ctx, "issues.status."+s)), which some
// key of the catalogs must start with. Framework messages a locale
// doesn't translate are noted, not counted.
func (tr *Translator) Check(w io.Writer, used []string) int {
	problems := 0
	report := func(format string, args ...any) {
		problems++
		fmt.Fprintf(w, format+"\n", args...)
	}
	ref := tr.appChain(tr.fallback)
	refKeys := keysOf(ref)
	for _, tag := range tr.supported {
		locale := tag.String()
		own := tr.appChain(tag)
		isRef := tag == tr.fallback || (len(own) > 0 && len(ref) > 0 && own[0] == ref[0])
		if !isRef {
			ownKeys := keysOf(own)
			var missing []string
			for _, k := range refKeys {
				if !slices.Contains(ownKeys, k) {
					missing = append(missing, k)
				}
			}
			if len(missing) > 0 {
				report("%s: %d key(s) missing: %s", locale, len(missing), listOf(missing))
			}
			var unknown []string
			for _, k := range ownKeys {
				want, ok := find(ref, k)
				other := tr.fallback.String()
				if !ok {
					want, ok = find([]*catalog{tr.core}, k) // a framework message
					other = "the framework's English"
				}
				if !ok {
					if !optionalKeys[k] && !strings.HasPrefix(k, "validation.attributes.") && !strings.HasPrefix(k, "validation.values.") {
						unknown = append(unknown, k)
					}
					continue
				}
				got, _ := find(own, k)
				if a, b := placeholders(got), placeholders(want); !slices.Equal(a, b) {
					report("%s: %s has placeholders %s; %s has %s", locale, k, listOrNone(a), other, listOrNone(b))
				}
			}
			if len(unknown) > 0 { // a misspelled key, or one only this locale needs
				fmt.Fprintf(w, "%s: note: %d key(s) %s and the framework don't have: %s\n", locale, len(unknown), tr.fallback, listOf(unknown))
			}
			var untranslated []string
			for _, k := range slices.Sorted(maps.Keys(tr.core.msgs)) {
				if _, ok := find(own, k); !ok && !isEnglish(tag) {
					untranslated = append(untranslated, k)
				}
			}
			if len(untranslated) > 0 {
				fmt.Fprintf(w, "%s: note: %d of the framework's messages are in English: %s\n", locale, len(untranslated), listOf(untranslated))
			}
		}
		chain := own
		if isRef {
			chain = ref
		}
		for _, l := range formatLists {
			if m, ok := find(chain, l.key); ok && len(m.list) != l.n {
				report("%s: %s needs %d names, not %d", locale, l.key, l.n, len(m.list))
			}
		}
		needs := pluralFormsOf(tag)
		for _, k := range keysOf(chain) {
			m, _ := find(chain, k)
			if m.plural == nil {
				continue
			}
			var lacks, unused []string
			for _, f := range needs {
				if _, ok := m.plural[f]; !ok {
					lacks = append(lacks, formNames[f])
				}
			}
			for _, f := range slices.Sorted(maps.Keys(m.plural)) {
				if !slices.Contains(needs, f) {
					unused = append(unused, formNames[f])
				}
			}
			if len(lacks) > 0 {
				report("%s: %s lacks the plural forms %s", locale, k, strings.Join(lacks, ", "))
			}
			if len(unused) > 0 { // English "zero": never chosen, so never shown
				fmt.Fprintf(w, "%s: note: %s has plural forms the language doesn't use: %s\n", locale, k, strings.Join(unused, ", "))
			}
		}
	}
	var undefined []string
	allKeys := append(slices.Clone(refKeys), keysOf([]*catalog{tr.core})...) // the framework's messages too
	for _, k := range used {
		if prefix, ok := strings.CutSuffix(k, "*"); ok { // "issues.status." + s
			if !slices.ContainsFunc(allKeys, func(r string) bool { return strings.HasPrefix(r, prefix) }) &&
				!slices.Contains(undefined, k) {
				undefined = append(undefined, k)
			}
			continue
		}
		if m, _ := tr.lookup(tr.Default(), k); m == nil && !slices.Contains(undefined, k) {
			undefined = append(undefined, k)
		}
	}
	if len(undefined) > 0 {
		slices.Sort(undefined)
		report("%d key(s) used in the source but in no catalog: %s", len(undefined), listOf(undefined))
	}
	if problems == 0 {
		fmt.Fprintf(w, "locale:check: %s OK\n", strings.Join(tr.Supported(), ", "))
	}
	return problems
}

// formatLists are the lists of names in a catalog's format section, and
// their lengths.
var formatLists = []struct {
	key string
	n   int
}{
	{"format.months", 12}, {"format.months_short", 12}, {"format.months_standalone", 12},
	{"format.days", 7}, {"format.days_short", 7}, {"format.periods", 2},
}

// optionalKeys are framework keys the English catalog leaves out.
var optionalKeys = map[string]bool{"format.numbering": true, "format.months_standalone": true}

// isEnglish reports whether tag's language is English (en, en-GB).
func isEnglish(tag language.Tag) bool {
	b, _ := tag.Base()
	en, _ := language.English.Base()
	return b == en
}

// appChain returns the app's catalogs for tag and its parents.
func (tr *Translator) appChain(tag language.Tag) []*catalog {
	var out []*catalog
	for t := tag; t != language.Und; t = t.Parent() {
		if c := tr.cats[t]; c != nil {
			out = append(out, c)
		}
	}
	return out
}

func keysOf(cats []*catalog) []string {
	set := map[string]bool{}
	for _, c := range cats {
		for k := range c.msgs {
			set[k] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

func find(cats []*catalog, key string) (*message, bool) {
	for _, c := range cats {
		if m := c.msgs[key]; m != nil {
			return m, true
		}
	}
	return nil, false
}

var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_.]*|[0-9]+)\}`)

// placeholders returns the sorted placeholder names of a message (all
// its forms).
func placeholders(m *message) []string {
	set := map[string]bool{}
	texts := []string{m.text}
	for _, t := range m.plural {
		texts = append(texts, t)
	}
	for _, t := range texts {
		for _, p := range placeholder.FindAllStringSubmatch(t, -1) {
			set[p[1]] = true
		}
	}
	if m.plural != nil {
		delete(set, "count") // a form may leave the number out ("one post")
	}
	return slices.Sorted(maps.Keys(set))
}

var formNames = map[plural.Form]string{
	plural.Zero: "zero", plural.One: "one", plural.Two: "two",
	plural.Few: "few", plural.Many: "many", plural.Other: "other",
}

// pluralFormsOf returns the plural forms tag's language uses for whole
// numbers.
func pluralFormsOf(tag language.Tag) []plural.Form {
	set := map[plural.Form]bool{}
	for n := range 1001 {
		set[plural.Cardinal.MatchPlural(tag, n, 0, 0, 0, 0)] = true
	}
	for _, n := range []int{10_000, 100_000, 1_000_000} {
		set[plural.Cardinal.MatchPlural(tag, n, 0, 0, 0, 0)] = true
	}
	return slices.Sorted(maps.Keys(set))
}

func listOf(keys []string) string {
	const shown = 10
	if len(keys) <= shown {
		return strings.Join(keys, ", ")
	}
	return strings.Join(keys[:shown], ", ") + fmt.Sprintf(" and %d more", len(keys)-shown)
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return "{" + strings.Join(names, "}, {") + "}"
}

// keyUse matches i18n.T(ctx, "key"…) and i18n.Plural(ctx, "key"…); the
// second group, a + after the key, makes it a prefix ("status." + s).
var keyUse = regexp.MustCompile(`i18n\.(?:T|Plural)\(\s*[A-Za-z_][\w.]*(?:\(\))?\s*,\s*"([^"\\]+)"(\s*\+)?`)

// sourceKeys returns the keys used literally in the .go and .templ files
// under dir, skipping hidden, vendor, node_modules and testdata folders.
// A key the source adds to ("issues.status." + s) is returned as a
// prefix: "issues.status.*".
func sourceKeys(dir string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == dir {
				return fs.SkipAll
			}
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".templ") {
			return nil
		}
		if strings.HasSuffix(name, "_templ.go") {
			return nil // generated from a .templ file read already
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range keyUse.FindAllSubmatch(data, -1) {
			k := string(m[1])
			if len(m[2]) > 0 {
				k += "*" // the start of keys made at run time
			}
			keys = append(keys, k)
		}
		return nil
	})
	return keys, err
}
