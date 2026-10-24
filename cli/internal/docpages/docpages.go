// SPDX-License-Identifier: Apache-2.0

// Package docpages finds and searches the pages of Anetos's
// documentation (package anetos.dev/anetos/docs) for the anetos tool's
// docs command.
package docpages

import (
	"cmp"
	"fmt"
	"io/fs"
	"math"
	"path"
	"slices"
	"strings"
	"unicode"
)

// Page is one page of the documentation.
type Page struct {
	// ID is its path in docs/site without ".md", and a folder's
	// README.md is the folder: "guides/queues", "getting-started/tutorial",
	// "" for the home page. It's also its address on docs.anetos.dev.
	ID string
	// File is its path in docs/site: "guides/queues.md".
	File string
	// Title is its front matter's title, else its first heading.
	Title string
	// Body is the page without its front matter.
	Body string
}

// Load reads every page of site (docs.Site), but its README.md, a note
// for the docs' writers.
func Load(site fs.FS) ([]Page, error) {
	var pages []Page
	err := fs.WalkDir(site, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") || p == "README.md" {
			return err
		}
		data, err := fs.ReadFile(site, p)
		if err != nil {
			return err
		}
		pages = append(pages, parse(p, string(data)))
		return nil
	})
	slices.SortFunc(pages, func(a, b Page) int { return cmp.Compare(a.ID, b.ID) })
	return pages, err
}

func parse(file, text string) Page {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	p := Page{File: file, ID: strings.TrimSuffix(file, ".md")}
	if p.ID == "README" {
		p.ID = ""
	} else if base := path.Base(p.ID); base == "README" {
		p.ID = path.Dir(p.ID)
	}
	body := text
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if fm, after, ok := strings.Cut(rest, "\n---\n"); ok {
			body = strings.TrimLeft(after, "\n")
			for line := range strings.SplitSeq(fm, "\n") {
				if v, ok := strings.CutPrefix(line, "title:"); ok {
					p.Title = strings.Trim(strings.TrimSpace(v), `"'`)
				}
			}
		}
	}
	p.Body = body
	if p.Title == "" {
		for line := range strings.SplitSeq(body, "\n") {
			if t, ok := strings.CutPrefix(line, "# "); ok {
				p.Title = strings.TrimSpace(t)
				break
			}
		}
	}
	if p.Title == "" {
		p.Title = p.ID
	}
	return p
}

// Find returns the pages a topic names: the page whose ID it is, else
// those whose ID ends with it ("queues", "tutorial/part-2"), else those
// whose ID or title contains it, ignoring case. A topic can also be a
// link from a page: "../guides/mail.md", "upgrade/README.md".
func Find(pages []Page, topic string) []Page {
	topic = strings.ReplaceAll(strings.TrimSpace(topic), "\\", "/")
	topic, _, _ = strings.Cut(topic, "#")
	for {
		t := strings.TrimPrefix(strings.TrimPrefix(topic, "./"), "../")
		if t == topic {
			break
		}
		topic = t
	}
	topic = strings.Trim(topic, "/")
	if strings.HasSuffix(strings.ToLower(topic), ".md") {
		topic = topic[:len(topic)-3]
	}
	if low := strings.ToLower(topic); low == "readme" {
		topic = ""
	} else if strings.HasSuffix(low, "/readme") {
		topic = topic[:len(topic)-len("/readme")]
	}
	low := strings.ToLower(topic)
	for _, p := range pages {
		if strings.ToLower(p.ID) == low {
			return []Page{p}
		}
	}
	if low == "" {
		return nil
	}
	var suffix, contains []Page
	for _, p := range pages {
		id := strings.ToLower(p.ID)
		switch {
		case strings.HasSuffix(id, "/"+low):
			suffix = append(suffix, p)
		case strings.Contains(id, low), strings.Contains(strings.ToLower(p.Title), low):
			contains = append(contains, p)
		}
	}
	if len(suffix) > 0 {
		return suffix
	}
	return contains
}

// Result is a page that matches a search, with its best lines.
type Result struct {
	Page  Page
	Score int
	Lines []string // up to two lines with the most query words
}

// stop words are left out of searches.
var stop = map[string]bool{
	"a": true, "an": true, "and": true, "the": true, "to": true, "of": true, "in": true,
	"on": true, "for": true, "is": true, "it": true, "how": true, "do": true, "i": true,
	"my": true, "with": true, "or": true, "be": true, "can": true, "what": true, "by": true,
}

// words splits text into lower-case words of letters and digits (and
// ':' '_' '.', so command names and identifiers stay whole, with their
// parts too: "make:crud" is make:crud, make and crud), without stop
// words.
func words(text string) []string {
	var out []string
	for f := range strings.FieldsFuncSeq(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ':' && r != '_' && r != '.'
	}) {
		f = strings.Trim(f, ".:_")
		if len(f) < 2 || stop[f] {
			continue
		}
		out = append(out, f)
		if strings.ContainsAny(f, ":_.") {
			for part := range strings.FieldsFuncSeq(f, func(r rune) bool { return r == ':' || r == '_' || r == '.' }) {
				if len(part) > 1 && !stop[part] {
					out = append(out, part)
				}
			}
		}
	}
	return out
}

// stem folds a word's plural into its singular, roughly ("queues" is
// queue, "queries" query), so either finds the other.
func stem(w string) string {
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		return w[:len(w)-1]
	}
	return w
}

// tokens counts text's words, stemmed.
func tokens(text string) map[string]int {
	m := map[string]int{}
	for _, w := range words(text) {
		m[stem(w)]++
	}
	return m
}

// Search ranks the pages by the query's words, BM25-style: a word few
// pages have counts more than one most pages have, and a long page
// doesn't win by length alone. Words match whole ("ai" isn't in
// "email"), plurals aside. A word in the title or the page's path
// counts most, then one in a heading; a page with every word, or with
// the query's words in a row ("log in"), ranks above one without. It
// returns at most limit results (every one when limit < 1).
func Search(pages []Page, query string, limit int) []Result {
	var terms []string
	for _, w := range words(query) {
		terms = append(terms, stem(w))
	}
	terms = slices.Compact(slices.Sorted(slices.Values(terms)))
	if len(terms) == 0 || len(pages) == 0 {
		return nil
	}
	phrase := ""
	if f := strings.Fields(strings.ToLower(query)); len(f) > 1 {
		phrase = strings.Join(f, " ")
	}
	type doc struct {
		title, body map[string]int
		text        string // lower case, for the phrase
		length      int
	}
	docs := make([]doc, len(pages))
	total := 0
	df := map[string]int{}
	for i, p := range pages {
		d := doc{title: tokens(p.Title + " " + p.ID), body: tokens(p.Body)}
		if phrase != "" {
			d.text = strings.Join(strings.Fields(strings.ToLower(p.Body)), " ")
		}
		for _, n := range d.body {
			d.length += n
		}
		d.length++
		total += d.length
		for _, t := range terms {
			if d.title[t] > 0 || d.body[t] > 0 {
				df[t]++
			}
		}
		docs[i] = d
	}
	avg := float64(total) / float64(len(pages))
	const k1, b = 1.2, 0.75
	saturate := func(tf float64, length int) float64 {
		return tf * (k1 + 1) / (tf + k1*(1-b+b*float64(length)/avg))
	}
	var results []Result
	for i, p := range pages {
		d := docs[i]
		score, found, best := 0.0, 0, 0.0
		for _, t := range terms {
			if df[t] == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(pages))-float64(df[t])+0.5)/(float64(df[t])+0.5))
			best = max(best, idf)
			s := 0.0
			if tf := float64(d.body[t]); tf > 0 {
				s = idf * saturate(tf, d.length)
			}
			if d.title[t] > 0 {
				s += 3 * idf
			}
			if s > 0 {
				found++
			}
			score += s
		}
		if score == 0 {
			continue
		}
		if found == len(terms) {
			score *= 2
		}
		if phrase != "" {
			if n := phraseCount(d.text, phrase); n > 0 {
				score += 6 * best * saturate(float64(n), d.length)
			}
		}
		type line struct {
			text string
			hits int
		}
		var lines []line
		inCode := false
		for raw := range strings.SplitSeq(p.Body, "\n") {
			trim := strings.TrimSpace(raw)
			if strings.HasPrefix(trim, "```") {
				inCode = !inCode
				continue
			}
			if inCode || trim == "" {
				continue
			}
			lt := tokens(raw)
			hits := 0
			for _, t := range terms {
				if lt[t] > 0 {
					hits++
					if strings.HasPrefix(trim, "#") {
						score += 0.5
					}
				}
			}
			if hits > 0 {
				lines = append(lines, line{trim, hits})
			}
		}
		slices.SortStableFunc(lines, func(a, b line) int { return b.hits - a.hits })
		r := Result{Page: p, Score: int(score * 100)}
		for _, l := range lines[:min(2, len(lines))] {
			r.Lines = append(r.Lines, clip(l.text, 160))
		}
		results = append(results, r)
	}
	slices.SortStableFunc(results, func(a, b Result) int {
		return cmp.Or(b.Score-a.Score, cmp.Compare(a.Page.ID, b.Page.ID))
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

// phraseCount counts phrase in text as whole words.
func phraseCount(text, phrase string) int {
	n := 0
	for i := 0; ; {
		j := strings.Index(text[i:], phrase)
		if j < 0 {
			return n
		}
		j += i
		end := j + len(phrase)
		if (j == 0 || !isWord(text[j-1])) && (end == len(text) || !isWord(text[end])) {
			n++
		}
		i = j + 1
	}
}

func isWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c >= 0x80
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Show is the text the docs command prints for a page: its title as a
// heading (when the page's own text has none) and its text.
func Show(p Page) string {
	body := p.Body
	if !strings.HasPrefix(body, "# ") {
		body = fmt.Sprintf("# %s\n\n%s", p.Title, body)
	}
	return strings.TrimRight(body, "\n") + "\n"
}
