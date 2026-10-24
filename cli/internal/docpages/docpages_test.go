// SPDX-License-Identifier: Apache-2.0

package docpages

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos/docs"
)

var site = fstest.MapFS{
	"README.md":                          {Data: []byte("---\ntitle: Anetos\n---\n\nThe home page.\n")},
	"guides/README.md":                   {Data: []byte("# Guides\n\nHow to do things.\n")},
	"guides/queues.md":                   {Data: []byte("---\ntitle: Queues\ngroup: Basics\n---\n\n# Queues\n\nDispatch a job.\n\n## Retry a failed job\n\n`queue:retry` retries a job that failed.\n\n```go\nretry retry retry\n```\n")},
	"guides/mail.md":                     {Data: []byte("# Send mail\n\nA job can send mail. Retry is automatic.\n")},
	"getting-started/tutorial/README.md": {Data: []byte("# Tutorial\n\nBuild a tracker.\n")},
	"reference/queues.md":                {Data: []byte("# Queue reference\n\nSettings.\n")},
}

func load(t *testing.T) []Page {
	t.Helper()
	pages, err := Load(site)
	if err != nil {
		t.Fatal(err)
	}
	return pages
}

func ids(pages []Page) string {
	var s []string
	for _, p := range pages {
		s = append(s, p.ID)
	}
	return strings.Join(s, ",")
}

func TestLoad(t *testing.T) {
	pages := load(t)
	if got := ids(pages); got != "getting-started/tutorial,guides,guides/mail,guides/queues,reference/queues" { // not the writers' README
		t.Errorf("IDs = %s", got)
	}
	for _, p := range pages {
		if p.ID == "guides/queues" && (p.Title != "Queues" || strings.Contains(p.Body, "group:")) {
			t.Errorf("guides/queues: title %q, body %q", p.Title, p.Body)
		}
		if p.ID == "guides/mail" && p.Title != "Send mail" {
			t.Errorf("guides/mail's title = %q", p.Title)
		}
	}
}

func TestFind(t *testing.T) {
	pages := load(t)
	for topic, want := range map[string]string{
		"guides/queues":    "guides/queues",
		"guides/queues.md": "guides/queues",
		"/Guides/Queues/":  "guides/queues",
		"queues":           "guides/queues,reference/queues", // ends with it: two
		"mail":             "guides/mail",
		"send":             "guides/mail", // the title
		"tutorial":         "getting-started/tutorial",
		"README":           "",
		"":                 "",
		"nothing":          "",
		"guides":           "guides",
		// Links from pages.
		"../guides/mail.md":                  "guides/mail",
		"./../../guides/queues.md#retry":     "guides/queues",
		"getting-started/tutorial/README.md": "getting-started/tutorial",
		"guides/README.md":                   "guides",
		"../README.md":                       "",
		`guides\mail.md`:                     "guides/mail",
	} {
		if got := ids(Find(pages, topic)); got != want {
			t.Errorf("Find(%q) = %q, want %q", topic, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	pages := load(t)
	results := Search(pages, "how to retry a job", 10)
	if len(results) < 2 || results[0].Page.ID != "guides/queues" {
		t.Fatalf("Search: %+v", results)
	}
	if got := results[0].Lines; len(got) == 0 || !strings.Contains(got[0], "queue:retry") && !strings.Contains(got[0], "Retry a failed job") {
		t.Errorf("lines = %q", got)
	}
	for _, l := range results[0].Lines {
		if strings.Contains(l, "retry retry") {
			t.Errorf("a code block's line: %q", l)
		}
	}
	if Search(pages, "the and of", 10) != nil {
		t.Error("stop words alone found pages")
	}
	if got := Search(pages, "job", 1); len(got) != 1 {
		t.Errorf("limit 1: %d results", len(got))
	}
	if got := Search(pages, "job", 0); len(got) != 2 {
		t.Errorf("limit 0 (all): %d results", len(got))
	}
	// Whole words, plurals aside: "ai" isn't in "mail", "queue" is "queues".
	if got := Search(pages, "ai", 0); got != nil {
		t.Errorf("ai: %+v", got)
	}
	if got := resultIDs(Search(pages, "queue", 0)); !slices.Equal(slices.Sorted(slices.Values(got)), []string{"guides/queues", "reference/queues"}) {
		t.Errorf("queue: %v", got)
	}
}

func TestShow(t *testing.T) {
	pages := load(t)
	for _, p := range pages {
		s := Show(p)
		if !strings.HasPrefix(s, "# ") || strings.HasPrefix(s, "# Queues\n\n# Queues") {
			t.Errorf("Show(%s) = %q", p.ID, s)
		}
	}
}

// TestRealDocs loads the documentation the tool embeds.
func TestRealDocs(t *testing.T) {
	pages, err := Load(docs.Site)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 100 {
		t.Fatalf("%d pages", len(pages))
	}
	seen := map[string]bool{}
	for _, p := range pages {
		if seen[p.ID] {
			t.Errorf("two pages are %q", p.ID)
		}
		seen[p.ID] = true
		if p.Title == "" || p.Title == p.ID && p.ID != "" {
			t.Errorf("%s has no title", p.File)
		}
	}
	if got := Find(pages, "queues"); len(got) == 0 {
		t.Error("no page for queues")
	}
	for query, want := range map[string]string{
		"retry a failed job":    "guides/queues",
		"ai":                    "ai",       // not email, not Tailwind
		"log in":                "accounts", // not the audit log
		"roles and permissions": "roles-and-permissions",
		"rate limit":            "guides/rate-limiting",
	} {
		r := Search(pages, query, 3)
		if !slices.ContainsFunc(r, func(r Result) bool { return strings.Contains(r.Page.ID, want) }) || strings.Contains(r[0].Page.ID, "audit") || strings.Contains(r[0].Page.ID, "mail") {
			t.Errorf("Search(%q): want %s first or close: %v", query, want, resultIDs(r))
		}
	}
}

func resultIDs(rs []Result) []string {
	var s []string
	for _, r := range rs {
		s = append(s, r.Page.ID)
	}
	return s
}
