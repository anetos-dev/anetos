// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

// message is a catalog entry: a text, a plural text, or a list (the
// names of months and days, for formatting).
type message struct {
	text   string
	plural map[plural.Form]string // with plural.Other, for a plural message
	list   []string
}

// pluralForms are the CLDR plural categories a plural message may use.
var pluralForms = map[string]plural.Form{
	"zero": plural.Zero, "one": plural.One, "two": plural.Two,
	"few": plural.Few, "many": plural.Many, "other": plural.Other,
}

// catalog is one locale's messages, by key.
type catalog struct {
	tag  language.Tag
	msgs map[string]*message
	from map[string]string // key → the file that defined it
}

func newCatalog(tag language.Tag) *catalog {
	return &catalog{tag: tag, msgs: map[string]*message{}, from: map[string]string{}}
}

// isCatalogFile reports whether name is a YAML file.
func isCatalogFile(name string) bool {
	ext := path.Ext(name)
	return ext == ".yaml" || ext == ".yml"
}

// parseLocale parses a locale name (a BCP 47 tag: en, bn, pt-BR).
func parseLocale(name string) (language.Tag, error) {
	tag, err := language.Parse(name)
	if err != nil || tag == language.Und {
		return language.Und, fmt.Errorf("%q is not a locale (BCP 47: en, bn, pt-BR)", name)
	}
	return tag, nil
}

// loadFS reads the catalogs in fsys into cats: <locale>.yaml (or .yml)
// files at its root, and the YAML files in <locale>/ folders (and their
// subfolders). Other files are ignored, so fsys can be a package's
// embedded files; a folder with YAML files is a locale's, and one whose
// name isn't a locale is an error.
func loadFS(fsys fs.FS, cats map[language.Tag]*catalog) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		var locale string
		var files []string
		switch {
		case e.IsDir():
			locale = name
			err := fs.WalkDir(fsys, name, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && isCatalogFile(p) {
					files = append(files, p)
				}
				return nil
			})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if len(files) == 0 {
				continue // not a locale's folder
			}
		case isCatalogFile(name):
			locale = strings.TrimSuffix(name, path.Ext(name))
			files = []string{name}
		default:
			continue
		}
		tag, err := parseLocale(locale)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		cat := cats[tag]
		if cat == nil {
			cat = newCatalog(tag)
			cats[tag] = cat
		}
		for _, f := range files {
			if err := cat.loadFile(fsys, f); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// loadFile adds the messages of one YAML file.
func (c *catalog) loadFile(fsys fs.FS, file string) error {
	data, err := fs.ReadFile(fsys, file)
	if err != nil {
		return err
	}
	var root any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	switch root.(type) {
	case nil: // empty, or only comments
		return nil
	case map[string]any, map[any]any:
	default:
		return fmt.Errorf("%s: a catalog is a map of keys to messages", file)
	}
	var errs []error
	c.add(file, "", root, &errs)
	return errors.Join(errs...)
}

// add adds node, the value of key (empty at the root), flattening nested
// maps into dotted keys.
func (c *catalog) add(file, key string, node any, errs *[]error) {
	switch v := node.(type) {
	case string:
		c.put(file, key, &message{text: v}, errs)
	case []any:
		list := make([]string, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				*errs = append(*errs, fmt.Errorf("%s: %s: item %d is not text; quote it", file, key, i+1))
				return
			}
			list[i] = s
		}
		c.put(file, key, &message{list: list}, errs)
	case map[string]any:
		if forms, ok := pluralMessage(v); ok {
			c.put(file, key, &message{plural: forms}, errs)
			return
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			sub := k
			if key != "" {
				sub = key + "." + k
			}
			c.add(file, sub, v[k], errs)
		}
	case map[any]any: // keys that aren't all strings (404:)
		m := make(map[string]any, len(v))
		for k, val := range v {
			switch k := k.(type) {
			case string:
				m[k] = val
			case int, int64, uint64:
				m[fmt.Sprint(k)] = val
			default:
				where := key
				if where == "" {
					where = "the top level"
				}
				*errs = append(*errs, fmt.Errorf("%s: %s has the key %v, which isn't text or a whole number; quote it", file, where, k))
			}
		}
		c.add(file, key, m, errs)
	case nil:
		if key != "" {
			*errs = append(*errs, fmt.Errorf("%s: %s has no value", file, key))
		}
	default:
		*errs = append(*errs, fmt.Errorf("%s: %s is %v, not text; quote it", file, key, v))
	}
}

func (c *catalog) put(file, key string, m *message, errs *[]error) {
	if key == "" {
		*errs = append(*errs, fmt.Errorf("%s: a message needs a key", file))
		return
	}
	if prev, ok := c.from[key]; ok {
		*errs = append(*errs, fmt.Errorf("%s: %s is also defined in %s", file, key, prev))
		return
	}
	c.msgs[key] = m
	c.from[key] = file
}

// pluralMessage returns the forms of a map whose keys are all plural
// categories, including "other".
func pluralMessage(m map[string]any) (map[plural.Form]string, bool) {
	if _, ok := m["other"]; !ok {
		return nil, false
	}
	forms := make(map[plural.Form]string, len(m))
	for k, v := range m {
		f, ok := pluralForms[k]
		s, isText := v.(string)
		if !ok || !isText {
			return nil, false
		}
		forms[f] = s
	}
	return forms, true
}
