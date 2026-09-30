// SPDX-License-Identifier: Apache-2.0

// Command docsnippets checks that code blocks in docs/site that claim to be
// copied from an example match that region exactly. A claim is a paragraph
// right after the block that starts with "(Copied from [`examples/x`](…),
// region `name`.)" or, for a file already named on the page, "(Region
// `name`.)". The example is examples/x/main.go, or the file itself when the
// link names a .go file (examples/x/migrations.go).
// Indentation common to every line is ignored on both sides, so a region
// inside a function body can be shown unindented.
// See docs/contributing/documentation-guide.md §7. Run it with `make docs-check`.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	exampleRef = regexp.MustCompile("examples/([A-Za-z0-9_./-]+?)(?:/main\\.go)?[`)]")
	regionRef  = regexp.MustCompile("[Rr]egion\\s+`([A-Za-z0-9_-]+)`")
	regionDef  = regexp.MustCompile(`(?s)// region: ([A-Za-z0-9_-]+)\n(.*?)\n[ \t]*// endregion`)
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	var problems []string
	checked := 0
	err := filepath.WalkDir(filepath.Join(root, "docs", "site"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		p, n := checkDoc(root, path, string(data))
		problems = append(problems, p...)
		checked += n
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Printf("docsnippets: %d snippet(s) match their example regions\n", checked)
}

func checkDoc(root, path, doc string) (problems []string, checked int) {
	lines := strings.Split(doc, "\n")
	var lastBlock, lastExample string
	inBlock := false
	var block []string
	regionsCache := map[string]map[string]string{}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case !inBlock && strings.HasPrefix(trimmed, "```go"):
			inBlock, block = true, nil
			continue
		case inBlock && strings.HasPrefix(trimmed, "```"):
			inBlock, lastBlock = false, strings.Join(block, "\n")
			continue
		case inBlock:
			block = append(block, line)
			continue
		}
		if m := exampleRef.FindStringSubmatch(line); m != nil {
			lastExample = m[1]
		}
		if !strings.HasPrefix(trimmed, "(Copied from") && !strings.HasPrefix(trimmed, "(Region") {
			continue
		}
		// The claim may wrap: join the paragraph's lines.
		var para strings.Builder
		para.WriteString(line)
		for j := i + 1; j < len(lines) && strings.TrimSpace(lines[j]) != ""; j++ {
			para.WriteString(" " + strings.TrimSpace(lines[j]))
		}
		if m := exampleRef.FindStringSubmatch(para.String()); m != nil {
			lastExample = m[1]
		}
		m := regionRef.FindStringSubmatch(para.String())
		if m == nil {
			problems = append(problems, fmt.Sprintf("%s:%d: copy claim without a region name", path, i+1))
			continue
		}
		if lastExample == "" {
			problems = append(problems, fmt.Sprintf("%s:%d: region %q referenced but no examples/ link before it", path, i+1, m[1]))
			continue
		}
		regions, ok := regionsCache[lastExample]
		if !ok {
			file := filepath.Join(root, "examples", lastExample)
			if !strings.HasSuffix(lastExample, ".go") {
				file = filepath.Join(file, "main.go")
			}
			src, err := os.ReadFile(file)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s:%d: %v", path, i+1, err))
				continue
			}
			regions = map[string]string{}
			for _, r := range regionDef.FindAllStringSubmatch(string(src), -1) {
				regions[r[1]] = dedent(strings.TrimRight(r[2], "\n"))
			}
			regionsCache[lastExample] = regions
		}
		want, ok := regions[m[1]]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s:%d: examples/%s has no region %q", path, i+1, lastExample, m[1]))
		case dedent(strings.TrimRight(lastBlock, "\n")) != want:
			problems = append(problems, fmt.Sprintf("%s:%d: code block differs from examples/%s region %q", path, i+1, lastExample, m[1]))
		default:
			checked++
		}
	}
	return problems, checked
}

// dedent removes the indentation common to all non-blank lines, so a region
// inside a function body can be shown without its leading tabs.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	prefix := ""
	first := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		switch {
		case first:
			prefix, first = indent, false
		default:
			for !strings.HasPrefix(indent, prefix) {
				prefix = prefix[:len(prefix)-1]
			}
		}
	}
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, prefix)
	}
	return strings.Join(lines, "\n")
}
