// SPDX-License-Identifier: Apache-2.0

// Package securitycheck has what the core's security:check command and
// web's route rules share: the guide's address of a rule, and the files
// of the app's code as reports show them.
package securitycheck

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
)

// Docs is the guide to the security check and its rules.
const Docs = "https://docs.anetos.dev/guides/security-check/"

// RuleDocs is the address of a framework rule's section of the guide
// (the heading "### `route.csrf`" has the anchor routecsrf).
func RuleDocs(rule string) string {
	return Docs + "#" + strings.ReplaceAll(rule, ".", "")
}

// MainModule is the path of the binary's main module, "" when unknown.
var MainModule = sync.OnceValue(func() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return bi.Main.Path
	}
	return ""
})

// InMainModule reports whether a function (runtime.Frame's Function:
// "example.com/shop/routes.Register", "main.setup") is the main
// module's.
func InMainModule(fn string) bool {
	if strings.HasPrefix(fn, "main.") {
		return true
	}
	m := MainModule()
	return m != "" && (strings.HasPrefix(fn, m+"/") || strings.HasPrefix(fn, m+"."))
}

// ModuleRoot is the folder of the go.mod around the working directory
// (the project's), else the working directory.
func ModuleRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for dir := wd; ; {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return wd
		}
		dir = parent
	}
}

// Rel makes a file of the app's code relative to the module's folder
// ([ModuleRoot]), with slashes: routes/web.go. A file -trimpath recorded
// as the main module's path ("example.com/shop/routes/web.go") loses
// the module's path. Another module's file stays as it is
// ("anetos.dev/anetos@v0.5.0/admin/panel.go", or its path in the module
// cache).
func Rel(f string) string {
	if f == "" {
		return ""
	}
	if filepath.IsAbs(f) {
		if r, err := filepath.Rel(ModuleRoot(), f); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(r)
		}
		return filepath.ToSlash(f)
	}
	if m := MainModule(); m != "" {
		if r, ok := strings.CutPrefix(f, m+"/"); ok {
			return r
		}
	}
	return filepath.ToSlash(f)
}

// RepoPrefix is the module's folder in its git repository, with a final
// slash ("services/shop/"), or "" at the repository's root or outside
// one: what makes a file Rel returns relative to the repository, as
// GitHub's code scanning reads SARIF.
func RepoPrefix() string {
	root := ModuleRoot()
	for dir := root; ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			r, err := filepath.Rel(dir, root)
			if err != nil || r == "." {
				return ""
			}
			return filepath.ToSlash(r) + "/"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
