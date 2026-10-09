// SPDX-License-Identifier: Apache-2.0

// Command docnav checks the front matter that orders the docs site's
// sidebar (documentation guide §5.1): every page of getting-started,
// guides, concepts and reference, but the folders' README.md files, has
// a group and a weight; the weights of a folder's pages are distinct,
// and each group's are a run that no other group's interleave with, so
// that the site (anetos-dev/docs's sync) shows the groups in order, as
// folders of the sidebar. A group's name mustn't make the URL of a page
// of its folder, nor a subfolder's. Pages in subfolders (the tutorial's
// parts) have a weight and no group. It also checks the images: every
// image a page shows (![alt](path), relative to the page) exists in
// docs/site and has an alt text, and every file of docs/site/images is
// shown by a page. Run it with make docs-check.
package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// sections are the folders of docs/site whose pages are grouped.
var sections = []string{"getting-started", "guides", "concepts", "reference"}

type page struct {
	file   string
	group  string
	weight int
}

func main() {
	root := "docs/site"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	n := 0
	for _, sec := range sections {
		files, err := filepath.Glob(filepath.Join(root, sec, "*.md"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "docnav:", err)
			os.Exit(2)
		}
		var pages []page
		names := map[string]bool{}
		for _, f := range files {
			if filepath.Base(f) == "README.md" {
				continue
			}
			n++
			names[strings.TrimSuffix(filepath.Base(f), ".md")] = true
			fm, err := frontMatter(f)
			if err != nil {
				report("%s: %v", f, err)
				continue
			}
			p := page{file: f, group: fm["group"]}
			if p.group == "" {
				report("%s: no group: in its front matter (documentation guide §5.1)", f)
			}
			w, err := strconv.Atoi(fm["weight"])
			if err != nil {
				report("%s: no weight: (a whole number) in its front matter", f)
				continue
			}
			p.weight = w
			pages = append(pages, p)
		}
		// Subfolders' pages: a weight each.
		sub, _ := filepath.Glob(filepath.Join(root, sec, "*", "*.md"))
		for _, f := range sub {
			names[filepath.Base(filepath.Dir(f))] = true // a group can't take a subfolder's URL either
			fm, err := frontMatter(f)
			if err == nil {
				_, err = strconv.Atoi(fm["weight"])
			}
			if err != nil {
				report("%s: no weight: (a whole number) in its front matter", f)
			} else if fm["group"] != "" {
				report("%s: a page of a subfolder has no group: its folder is its group", f)
			}
		}
		slices.SortFunc(pages, func(a, b page) int { return a.weight - b.weight })
		seen := map[string]bool{}
		for i, p := range pages {
			if i > 0 && pages[i-1].weight == p.weight {
				report("%s: weight %d is %s's too", p.file, p.weight, pages[i-1].file)
			}
			if i > 0 && pages[i-1].group != p.group {
				if seen[p.group] {
					report("%s: group %q has pages on both sides of group %q's: give its pages weights next to each other", p.file, p.group, pages[i-1].group)
				}
			}
			seen[p.group] = true
			if slug := slug(p.group); names[slug] && !seen["slug:"+slug] {
				seen["slug:"+slug] = true
				report("%s: group %q would take the URL of %s/%s: rename the group", p.file, p.group, sec, slug)
			}
		}
	}
	images := checkImages(root, report)
	for _, p := range problems {
		fmt.Println(p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Printf("docnav: %d pages have their group and weight; %d images are shown\n", n, images)
}

var image = regexp.MustCompile(`!\[([^\]]*)\]\(\s*(<[^>]*>|[^)\s]+)`)

// checkImages reports the images pages show that don't exist (or lie
// outside root) or have no alt text, and the files of root's images
// folder that no page shows; it returns how many images are shown.
// Images in fenced code blocks don't count.
func checkImages(root string, report func(string, ...any)) int {
	shown := map[string]bool{}
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if name := d.Name(); strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
			return nil // .DS_Store, an editor's files
		}
		if filepath.Ext(p) != ".md" {
			if rel, _ := filepath.Rel(root, p); strings.HasPrefix(filepath.ToSlash(rel), "images/") {
				files = append(files, p)
			}
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range image.FindAllStringSubmatch(outsideFences(string(b)), -1) {
			alt, dest := m[1], strings.Trim(m[2], "<>")
			if strings.Contains(dest, "://") {
				continue // elsewhere
			}
			if strings.HasPrefix(dest, "/") {
				report("%s: shows %s: give its path relative to the page (documentation guide §5.1)", p, dest)
				continue
			}
			if i := strings.IndexAny(dest, "#?"); i >= 0 {
				dest = dest[:i]
			}
			if u, err := url.PathUnescape(dest); err == nil {
				dest = u
			}
			target := filepath.Join(filepath.Dir(p), filepath.FromSlash(dest))
			if rel, err := filepath.Rel(root, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				report("%s: shows %s, which is outside %s", p, dest, root)
				continue
			}
			if _, err := os.Stat(target); err != nil {
				report("%s: shows %s, which doesn't exist", p, dest)
			}
			if strings.TrimSpace(alt) == "" {
				report("%s: %s has no alt text (documentation guide §5.1)", p, dest)
			}
			shown[target] = true
		}
		return nil
	})
	if err != nil {
		report("%s: %v", root, err)
	}
	for _, f := range files {
		if !shown[f] {
			report("%s: no page shows it as ![alt](relative path): show it or remove it", f)
		}
	}
	return len(shown)
}

// outsideFences returns the lines of a page that aren't in a fenced
// code block (``` or ~~~).
func outsideFences(page string) string {
	var b strings.Builder
	fence := ""
	for line := range strings.Lines(page) {
		t := strings.TrimLeft(line, " ")
		switch {
		case fence == "" && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")):
			fence = t[:3]
		case fence != "" && strings.HasPrefix(t, fence):
			fence = ""
		case fence == "":
			b.WriteString(line)
		}
	}
	return b.String()
}

// frontMatter reads the "key: value" lines of a page's front matter.
func frontMatter(file string) (map[string]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || sc.Text() != "---" {
		return nil, fmt.Errorf("no front matter")
	}
	fm := map[string]string{}
	for sc.Scan() {
		l := sc.Text()
		if l == "---" {
			return fm, nil
		}
		if k, v, ok := strings.Cut(l, ":"); ok {
			fm[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return nil, fmt.Errorf("front matter without its closing ---")
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// slug is a group's folder on the site: "Accounts and security" →
// accounts-and-security. anetos-dev/docs's sync makes the same.
func slug(group string) string {
	return strings.Trim(nonWord.ReplaceAllString(strings.ToLower(group), "-"), "-")
}
