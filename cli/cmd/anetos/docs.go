// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"anetos.dev/anetos/cli/internal/docpages"
	"anetos.dev/anetos/docs"
)

const docsUsage = `Usage: anetos docs [<page>] [--search <words>]

Prints Anetos's documentation, as Markdown, at the version of Anetos the
project uses (the pages are built into this tool): no network needed.

  anetos docs                     list the pages
  anetos docs queues              print a page: its path (guides/queues),
                                  the end of it (queues) or a word of its title
  anetos docs --search "retry a job"  the pages that best match, with their lines

A page's links to other pages (../guides/mail.md) are pages this command
prints too: anetos docs guides/mail.
`

// docsCmd runs anetos docs.
func docsCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos docs", flag.ContinueOnError)
	search := fs.Bool("search", false, "search the pages for the words given")
	limit := fs.Int("n", 5, "with --search: the most results to show")
	pos, code := parse(fs, args, stderr, docsUsage)
	if code >= 0 {
		return code
	}
	if *limit < 1 {
		fmt.Fprintln(stderr, "anetos docs: -n is how many results to show: 1 or more")
		return 2
	}
	pages, err := docpages.Load(docs.Site)
	if err != nil {
		fmt.Fprintln(stderr, "anetos docs:", err)
		return 1
	}
	switch {
	case *search:
		query := strings.Join(pos, " ")
		if strings.TrimSpace(query) == "" {
			fmt.Fprintln(stderr, "anetos docs: --search needs words to look for")
			return 2
		}
		results := docpages.Search(pages, query, *limit)
		if len(results) == 0 {
			fmt.Fprintf(stdout, "No page mentions %q. List the pages: anetos docs\n", query)
			return 1
		}
		for _, r := range results {
			fmt.Fprintf(stdout, "%s  (anetos docs %s)\n", r.Page.Title, r.Page.ID)
			for _, l := range r.Lines {
				fmt.Fprintf(stdout, "    %s\n", l)
			}
		}
		return 0
	case len(pos) == 0:
		fmt.Fprintf(stdout, "The documentation of Anetos %s (%d pages). Print one: anetos docs <page>\n", docsVersion(), len(pages))
		width := 0
		for _, p := range pages {
			width = max(width, len(p.ID))
		}
		section := "-"
		for _, p := range pages {
			if s, _, _ := strings.Cut(p.ID, "/"); s != section {
				section = s
				fmt.Fprintln(stdout)
			}
			fmt.Fprintf(stdout, "%-*s  %s\n", width, p.ID, p.Title)
		}
		return 0
	case len(pos) > 1:
		fmt.Fprintln(stderr, "anetos docs: one page at a time (or --search for words)")
		return 2
	}
	found := docpages.Find(pages, pos[0])
	switch len(found) {
	case 0:
		fmt.Fprintf(stderr, "anetos docs: no page %q; search for it: anetos docs --search %q\n", pos[0], pos[0])
		return 1
	case 1:
		fmt.Fprint(stdout, docpages.Show(found[0]))
		return 0
	}
	fmt.Fprintf(stderr, "anetos docs: %q could be:\n", pos[0])
	for _, p := range found {
		fmt.Fprintf(stderr, "  anetos docs %-40s %s\n", p.ID, p.Title)
	}
	return 2
}

// docsVersion is the version of the core module this tool was built
// with: the project's, in a project.
func docsVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "anetos.dev/anetos" {
				if d.Replace != nil && d.Replace.Version == "" {
					return d.Version + " (a local checkout)"
				}
				return d.Version
			}
		}
	}
	return "(devel)"
}
