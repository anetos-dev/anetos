// SPDX-License-Identifier: Apache-2.0

// Package naming holds the naming rules the db package uses for columns
// and tables. `anetos gen` uses them too, so generated code always
// matches the runtime.
package naming

import (
	"strings"
	"unicode"
)

// Snake converts a Go name to snake_case: AuthorID → author_id,
// HTTPStatus → http_status, UserIDs → user_ids.
func Snake(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			// A plural "s" after an acronym (IDs, URLs) stays with it.
			pluralAcronym := nextLower && runes[i+1] == 's' && (i+2 == len(runes) || unicode.IsUpper(runes[i+2]))
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower && !pluralAcronym) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Plural returns the English plural of a snake_case table name, changing
// only its last word: category → categories, box → boxes, person →
// people. Use a TableName method for anything it gets wrong.
func Plural(s string) string {
	head, word := "", s
	if i := strings.LastIndexByte(s, '_'); i >= 0 {
		head, word = s[:i+1], s[i+1:]
	}
	if p, ok := irregular[word]; ok {
		return head + p
	}
	switch {
	case word == "" || strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") &&
		!strings.HasSuffix(word, "us") && !strings.HasSuffix(word, "is"):
		return s // already plural (or ends in s like "news")
	case strings.HasSuffix(word, "y") && len(word) > 1 && !strings.ContainsRune("aeiou", rune(word[len(word)-2])):
		return head + word[:len(word)-1] + "ies"
	case strings.HasSuffix(word, "is") && len(word) > 3: // axis → axes, analysis → analyses
		return head + word[:len(word)-2] + "es"
	case strings.HasSuffix(word, "ss"), strings.HasSuffix(word, "x"), strings.HasSuffix(word, "z"),
		strings.HasSuffix(word, "ch"), strings.HasSuffix(word, "sh"), strings.HasSuffix(word, "us"):
		return head + word + "es"
	}
	return head + word + "s"
}

var irregular = map[string]string{
	"person": "people", "man": "men", "woman": "women", "child": "children",
	"mouse": "mice", "goose": "geese", "foot": "feet", "tooth": "teeth",
	"datum": "data", "medium": "media", "criterion": "criteria",
	"analysis": "analyses", "index": "indices", "status": "statuses",
	"quiz": "quizzes", "leaf": "leaves", "life": "lives", "knife": "knives",
	"wife": "wives", "half": "halves", "info": "info", "equipment": "equipment",
	"news": "news", "series": "series", "species": "species", "sheep": "sheep",
	"fish": "fish", "deer": "deer", "data": "data", "metadata": "metadata",
	"alias": "aliases", "canvas": "canvases", "gas": "gases", "lens": "lenses",
	"hero": "heroes", "potato": "potatoes", "tomato": "tomatoes", "echo": "echoes",
	"matrix": "matrices", "vertex": "vertices", "appendix": "appendices",
	"axis": "axes", "this": "this",
}
