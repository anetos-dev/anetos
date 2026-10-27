// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"context"
	"slices"
	"strings"
)

// Messages returns the app's messages (its catalogs', not the
// framework's) in ctx's locale whose keys are under prefix ("auth" for
// auth.login.title), keyed without it ("login.title"), or all of them,
// keyed as they are, for prefix "": for a frontend that translates its
// own pages with the app's catalogs (an Inertia app's t()). Each comes
// through the locale's fallbacks, as [T] finds it, so a key missing from
// the locale's catalog has its fallback's text. A text is a string; a
// plural message is an object of its forms ({"one": "{count} post",
// "other": "{count} posts"}), whose placeholders the frontend fills,
// picking the form by the page locale's plural rules (where [Plural]
// picks a fallback message's form by its own catalog's rules). A form
// isn't a key of its own, as it is for T ("posts.count.one"), and lists
// (the names of months) are left out. The map is new for each call.
func Messages(ctx context.Context, prefix string) map[string]any {
	tr := From(ctx)
	if prefix = strings.TrimSuffix(prefix, "."); prefix != "" {
		prefix += "."
	}
	out := map[string]any{}
	chain := tr.chain(Locale(ctx))
	// The fallbacks first, so the locale's own messages overwrite theirs.
	for _, cat := range slices.Backward(chain) {
		if cat == tr.core {
			continue // the framework's: validation's, the error pages'
		}
		for key, m := range cat.msgs {
			name, ok := strings.CutPrefix(key, prefix)
			if !ok || name == "" || m.list != nil {
				continue
			}
			if m.plural == nil {
				out[name] = m.text
				continue
			}
			forms := make(map[string]string, len(m.plural))
			for f, text := range m.plural {
				for n, pf := range pluralForms {
					if pf == f {
						forms[n] = text
					}
				}
			}
			out[name] = forms
		}
	}
	return out
}
