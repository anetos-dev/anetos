// SPDX-License-Identifier: Apache-2.0

// Package testmod writes the go.mod and go.sum of temporary modules that
// tests build against the core module of this repository.
package testmod

import (
	"os"
	"path/filepath"
	"strings"
)

// Files returns the go.mod and go.sum of a module named example.com/app
// that requires the core module at core (a directory, through a replace
// directive), with the core's own requirements listed, so that go
// commands run without changing them.
func Files(core string) (goMod, goSum string, err error) {
	mod, err := os.ReadFile(filepath.Join(core, "go.mod"))
	if err != nil {
		return "", "", err
	}
	sum, err := os.ReadFile(filepath.Join(core, "go.sum"))
	if err != nil && !os.IsNotExist(err) {
		return "", "", err
	}
	goVersion := "1.26"
	var reqs []string
	inBlock := false
	for line := range strings.SplitSeq(string(mod), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "go "):
			goVersion = strings.TrimPrefix(line, "go ")
		case line == "require (":
			inBlock = true
		case inBlock && line == ")":
			inBlock = false
		case inBlock && line != "":
			reqs = append(reqs, strings.TrimSpace(strings.TrimSuffix(line, "// indirect")))
		case strings.HasPrefix(line, "require ") && !strings.HasSuffix(line, "("):
			reqs = append(reqs, strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "require "), "// indirect")))
		}
	}
	var b strings.Builder
	b.WriteString("module example.com/app\n\ngo " + goVersion + "\n\nrequire anetos.dev/anetos v0.0.0\n\nrequire (\n")
	for _, r := range reqs {
		b.WriteString("\t" + r + " // indirect\n")
	}
	b.WriteString(")\n\nreplace anetos.dev/anetos => " + core + "\n")
	return b.String(), string(sum), nil
}
