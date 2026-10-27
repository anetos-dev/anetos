// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

// ViteVersion and TypeScriptVersion are the releases a new frontend's
// package.json asks for (at least; the lockfile npm writes pins them).
const (
	ViteVersion       = "8.3.5"
	TypeScriptVersion = "7.0.2"
)

// Languages are the values of AddVite's lang: TypeScript or JavaScript.
var Languages = []string{"ts", "js"}

// ViteResult is what [AddVite] did.
type ViteResult struct {
	// Created are the files it wrote, relative to the project.
	Created []string
	// Updated are the project's files it changed: main.go, routes/web.go,
	// views/layout.templ (an API project: .env, .env.example,
	// deploy/production.env.example), .gitignore, .dockerignore,
	// Dockerfile.
	Updated []string
	// Manual says what to add by hand, for the files it couldn't change
	// (an app's own edits moved what it looks for).
	Manual []string
	// Entry is the frontend's entry ("src/main.ts").
	Entry string
	// SPA reports that the frontend is an API project's single-page app,
	// served at / ([AddVite]).
	SPA bool
}

// ErrHasFrontend is AddVite's error for a project that has frontend/
// already.
var ErrHasFrontend = errors.New("the project has frontend/ already")

type viteData struct {
	Module, Title, Image, GoMinor  string
	TS                             bool
	Auth                           bool   // make:auth's routes: the SPA logs in
	Ext                            string // "ts" or "js"
	ViteVersion, TypeScriptVersion string
}

// AddVite gives the project in root a frontend that Vite builds (design
// D341–D345): frontend/ (package.json, vite.config, src/main.ts or
// main.js, a go.mod that keeps the go command out of node_modules;
// tsconfig.json for TypeScript), and the app's side. lang is "ts" or
// "js".
//
// In a web project, the frontend is its pages' scripts and styles:
// vite.New in main.go's setup, the /build/ route in routes/web.go,
// @vite.Tags in the layout's head, the build output (public/build) and
// node_modules in .gitignore and .dockerignore, and a Node stage in the
// Dockerfile.
//
// In an API project, it is a single-page app, served at / (design D365):
// index.html and src/main (a login page, with make:auth's routes), built
// into public/spa, which public/public.go embeds; in main.go,
// typescript.Register (the client src/main uses) and vite.SPA as the
// router's fallback; Vite's address (http://localhost:5173) and APP_URL
// in HTTP_SPA_ORIGINS in .env and .env.example, and APP_URL in
// deploy/production.env.example's; .gitignore, .dockerignore and the
// Dockerfile as for a web project.
//
// It writes nothing in a project with frontend/; a file it can't change
// safely is left alone and named in Manual, with what to add. It doesn't
// install the packages, or write the types (go run . types).
func AddVite(root, lang string) (ViteResult, error) {
	var res ViteResult
	switch lang {
	case "ts", "js":
	default:
		return res, fmt.Errorf("the frontend's language must be one of %s", strings.Join(Languages, ", "))
	}
	if _, err := os.Stat(filepath.Join(root, "frontend")); err == nil {
		return res, ErrHasFrontend
	}
	spa := IsAPI(root)
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return res, err
	}
	module := modfile.ModulePath(mod)
	name := filepath.Base(root)
	if abs, err := filepath.Abs(root); err == nil {
		name = filepath.Base(abs)
	}
	d := viteData{Module: module, Title: title(name), Image: strings.ToLower(name), GoMinor: goMinor,
		TS: lang == "ts", Ext: lang, ViteVersion: ViteVersion, TypeScriptVersion: TypeScriptVersion}
	res.Entry, res.SPA = "src/main."+lang, spa
	if spa {
		_, err := os.Stat(filepath.Join(root, "routes", "auth.go"))
		d.Auth = err == nil
	}

	// Render everything first, so a template error writes nothing.
	type file struct {
		rel     string
		content []byte
	}
	var files []file
	add := func(dir string, skip func(rel string) bool) error {
		return fs.WalkDir(templates, dir, func(src string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return err
			}
			rel := strings.TrimSuffix(strings.TrimPrefix(src, dir+"/"), ".tmpl")
			if skip(rel) || rel == "tsconfig.json" && !d.TS {
				return nil
			}
			switch rel {
			case "vite.config", "src/main":
				rel += "." + lang
			}
			b, err := render(src, "", d)
			if err != nil {
				return err
			}
			to := path.Join("frontend", rel)
			if rel == "public.go" {
				to = "public/public.go"
			}
			files = append(files, file{to, b})
			return nil
		})
	}
	if spa {
		// package.json, tsconfig.json and go.mod as a web project's; the
		// rest the SPA's.
		err = add("templates/vite", func(rel string) bool {
			return rel != "package.json" && rel != "tsconfig.json" && rel != "go.mod"
		})
		if err == nil {
			err = add("templates/spa", func(rel string) bool {
				_, err := os.Stat(filepath.Join(root, "public", "public.go"))
				return rel == "public.go" && err == nil // the app's own: it must have Files
			})
		}
	} else {
		err = add("templates/vite", func(string) bool { return false })
	}
	if err != nil {
		return res, err
	}
	for _, f := range files {
		if err := writeNew(filepath.Join(root, filepath.FromSlash(f.rel)), f.content, 0o644); err != nil {
			for _, c := range res.Created {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(c)))
			}
			return ViteResult{}, err
		}
		res.Created = append(res.Created, f.rel)
	}

	var patches []vitePatch
	if spa {
		patches = spaPatches(root, module)
	} else {
		patches = []vitePatch{
			{"main.go", func(b []byte) ([]byte, bool) { return patchMainVite(b, module) },
				"main.go: in setup, before web.NewServer, call `if _, err := vite.New(app, public.Files); err != nil { return nil, err }` (imports anetos.dev/anetos/view/vite and " + module + "/public)", false},
			{"routes/web.go", patchRoutesVite,
				"routes/web.go: route the build, `r.Get(\"/build/{path...}\", web.WrapHandler(vite.Handler()))` (import anetos.dev/anetos/view/vite)", false},
			{"views/layout.templ", func(b []byte) ([]byte, bool) { return patchLayoutVite(b, res.Entry) },
				"views/layout.templ: in <head>, load the entry with `@vite.Tags(\"" + res.Entry + "\")` (import anetos.dev/anetos/view/vite), once main.go calls vite.New", false},
			{".gitignore", func(b []byte) ([]byte, bool) { return appendLines(b, gitignoreVite, "/public/build/") }, "", false},
			{".dockerignore", func(b []byte) ([]byte, bool) { return appendLines(b, dockerignoreVite, "/public/build/") }, "", false},
			{"Dockerfile", func(b []byte) ([]byte, bool) { return patchDockerfileVite(b, "public/build") },
				"Dockerfile: build the frontend in a Node stage (its package manager's install, then its build script, in frontend/), copy its public/build into the Go stage after `COPY . .`, and run `go tool anetos build --no-frontend` there", false},
		}
	}
	mainPatched := false
	for _, p := range patches {
		full := filepath.Join(root, filepath.FromSlash(p.file))
		b, err := os.ReadFile(full)
		if errors.Is(err, os.ErrNotExist) && (p.manual == "" || p.ifExists) {
			continue // no .gitignore: nothing to add to
		}
		var out []byte
		ok := false
		// The layout's tags need vite.New: without it, every page fails.
		if err == nil && (p.file != "views/layout.templ" || mainPatched) {
			out, ok = p.patch(lf(b)) // a Windows checkout's CRLF, kept
		}
		if !ok {
			if p.manual != "" {
				res.Manual = append(res.Manual, p.manual)
			}
			continue
		}
		if bytes.Equal(out, lf(b)) {
			continue // has it already
		}
		if err := os.WriteFile(full, crlf(out, b), 0o644); err != nil {
			return res, err
		}
		mainPatched = mainPatched || p.file == "main.go"
		res.Updated = append(res.Updated, p.file)
	}
	if spa {
		if b, err := os.ReadFile(filepath.Join(root, "public", "public.go")); err == nil &&
			!slices.Contains(res.Created, "public/public.go") && !bytes.Contains(b, []byte("Files embed.FS")) {
			res.Manual = append(res.Manual, "public/public.go: embed the folder, `//go:embed *` above `var Files embed.FS`, for vite.SPA in main.go")
		}
	}
	return res, nil
}

// vitePatch is a change AddVite makes to a file of the project, with
// what to add by hand when it can't ("" for a change it leaves out
// quietly: a missing .gitignore). A patch returning its input unchanged
// found the change made already.
type vitePatch struct {
	file     string
	patch    func([]byte) ([]byte, bool)
	manual   string
	ifExists bool // a missing file is left out quietly
}

// spaPatches are AddVite's changes to an API project, whose module is
// module.
func spaPatches(root, module string) []vitePatch {
	_, err := os.Stat(filepath.Join(root, "routes", "auth.go"))
	auth := err == nil
	return []vitePatch{
		{"main.go", func(b []byte) ([]byte, bool) { return patchMainSPA(b, module) },
			"main.go: in setup, after the routes, serve the SPA for the paths they don't take, `spa, err := vite.SPA(app, public.Files, vite.Except(\"/api/\"))` then `srv.Router().Fallback(spa)`, and write its client, `typescript.Register(app, srv, typescript.Config{Prefix: routes.OpenAPI.Prefix})` (imports anetos.dev/anetos/view/vite, anetos.dev/anetos/web/typescript and " + module + "/public)", false},
		{".gitignore", func(b []byte) ([]byte, bool) { return appendLines(b, gitignoreSPA, "/public/spa/") }, "", false},
		{".dockerignore", func(b []byte) ([]byte, bool) { return appendLines(b, dockerignoreSPA, "/public/spa/") }, "", false},
		{"Dockerfile", func(b []byte) ([]byte, bool) {
			if bytes.Contains(b, []byte("COPY --from=frontend /src/public/spa ")) {
				return b, true // built already
			}
			return patchDockerfileVite(b, "public/spa")
		},
			"Dockerfile: build the frontend in a Node stage (its package manager's install, then its build script, in frontend/), copy its public/spa into the Go stage after `COPY . .`, and run `go tool anetos build --no-frontend` there", false},
		{".env", patchEnvSPA, ".env: list " + spaDevOrigin + " (Vite's address) and APP_URL's origin in HTTP_SPA_ORIGINS, for the SPA to log in with the session", true},
		{".env.example", patchEnvSPA, ".env.example: list " + spaDevOrigin + " and APP_URL's origin in HTTP_SPA_ORIGINS", true},
		{"deploy/production.env.example", func(b []byte) ([]byte, bool) { return patchProductionEnvSPA(b, auth) },
			"deploy/production.env.example: list APP_URL's origin, where the app serves the SPA, in HTTP_SPA_ORIGINS", true},
	}
}

const gitignoreSPA = `
# The frontend's packages and build (anetos build makes it)
/frontend/node_modules/
/public/spa/
`

const dockerignoreSPA = `/frontend/node_modules/
/public/spa/
`

// spaDevOrigin is the SPA's address in development: Vite's, which its
// vite.config sets.
const spaDevOrigin = "http://localhost:5173"

var spaOriginsEmpty = regexp.MustCompile(`(?m)^HTTP_SPA_ORIGINS=[ \t]*$`)

// patchEnvSPA lists Vite's address, and APP_URL's origin, where the
// built app serves the SPA, in HTTP_SPA_ORIGINS, when it is empty: the
// SPA logs in with the session.
func patchEnvSPA(src []byte) ([]byte, bool) {
	if listsOrigin(src, spaDevOrigin) {
		return src, true
	}
	if !spaOriginsEmpty.Match(src) {
		return nil, false
	}
	origins := spaDevOrigin
	if o := appOrigin(src); o != "" && o != spaDevOrigin {
		origins += "," + o
	}
	return spaOriginsEmpty.ReplaceAll(src, []byte("HTTP_SPA_ORIGINS="+origins)), true
}

var spaOriginsLine = regexp.MustCompile(`(?m)^HTTP_SPA_ORIGINS=(.*)$`)

// listsOrigin reports whether an env file's HTTP_SPA_ORIGINS lists origin.
func listsOrigin(src []byte, origin string) bool {
	m := spaOriginsLine.FindSubmatch(src)
	if m == nil {
		return false
	}
	for o := range strings.SplitSeq(string(m[1]), ",") {
		if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(o), "/"), origin) {
			return true
		}
	}
	return false
}

var (
	appURLLine        = regexp.MustCompile(`(?m)^APP_URL=["']?([^\s#"']+)["']?[ \t]*(?:#.*)?$`)
	clientURLTemplate = regexp.MustCompile(`(?m)^AUTH_CLIENT_URL=https://app\.example\.com[ \t]*$`)
)

// patchProductionEnvSPA lists APP_URL, where the app serves its SPA, in
// HTTP_SPA_ORIGINS; with make:auth, the account emails link there too
// (AUTH_CLIENT_URL), unless the file names another address already.
func patchProductionEnvSPA(src []byte, auth bool) ([]byte, bool) {
	origin := appOrigin(src)
	if origin == "" {
		return nil, false
	}
	if listsOrigin(src, origin) {
		return src, true
	}
	if !spaOriginsEmpty.Match(src) {
		return nil, false
	}
	out := spaOriginsEmpty.ReplaceAll(src, []byte("HTTP_SPA_ORIGINS="+origin))
	if auth {
		out = clientURLTemplate.ReplaceAll(out, []byte("AUTH_CLIENT_URL="+origin))
	}
	return out, true
}

// appOrigin is the origin of an env file's APP_URL, or "".
func appOrigin(src []byte) string {
	m := appURLLine.FindSubmatch(src)
	if m == nil {
		return ""
	}
	u, err := url.Parse(string(m[1]))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

var openAPIRegister = regexp.MustCompile(`(?m)^\tif err := openapi\.Register\(app, srv, routes\.OpenAPI\); err != nil \{\n\t\treturn nil, err\n\t\}\n`)

// patchMainSPA adds, after openapi.Register in setup, typescript.Register
// (the client the SPA calls the API with) and vite.SPA as the router's
// fallback, and their imports.
func patchMainSPA(src []byte, module string) ([]byte, bool) {
	s := string(src)
	if strings.Contains(s, "vite.SPA(") {
		return src, true // served already
	}
	loc := openAPIRegister.FindStringIndex(s)
	if loc == nil || len(openAPIRegister.FindAllString(s, -1)) != 1 {
		return nil, false
	}
	block := ""
	imports := []string{`"anetos.dev/anetos/view/vite"`, `"` + module + `/public"`}
	if !strings.Contains(s, "typescript.Register(") {
		block = "\t// `go run . types`: the client of the API in frontend/src/types, which\n" +
			"\t// the SPA calls it with (anetos dev updates it).\n" +
			"\tif err := typescript.Register(app, srv, typescript.Config{Prefix: routes.OpenAPI.Prefix}); err != nil {\n\t\treturn nil, err\n\t}\n"
		imports = append(imports, `"anetos.dev/anetos/web/typescript"`)
	}
	block += "\t// The single-page app (frontend/), which Vite builds into public/spa:\n" +
		"\t// at /, for the paths the routes don't take, but /api/'s. In\n" +
		"\t// development, open Vite's address instead (anetos dev runs it).\n" +
		"\tspa, err := vite.SPA(app, public.Files, vite.Except(\"/api/\"))\n\tif err != nil {\n\t\treturn nil, err\n\t}\n" +
		"\tsrv.Router().Fallback(spa)\n"
	s = s[:loc[1]] + block + s[loc[1]:]
	s, ok := addImports(s, imports...)
	if !ok {
		return nil, false
	}
	out, err := format.Source([]byte(s))
	if err != nil {
		return nil, false
	}
	return out, true
}

const gitignoreVite = `
# The frontend's packages and build (anetos build makes it)
/frontend/node_modules/
/public/build/
`

const dockerignoreVite = `/frontend/node_modules/
/public/build/
`

// appendLines appends lines to a file that doesn't have marker (one of
// them) yet.
func appendLines(b []byte, lines, marker string) ([]byte, bool) {
	if bytes.Contains(b, []byte(marker)) {
		return nil, false
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	return append(b, lines...), true
}

var newServerLine = regexp.MustCompile(`(?m)^(\t)srv, err := web\.NewServer\(app\)\n`)

// patchMainVite adds vite.New to setup, before web.NewServer, and its
// imports.
func patchMainVite(src []byte, module string) ([]byte, bool) {
	s := string(src)
	loc := newServerLine.FindStringIndex(s)
	if loc == nil || strings.Contains(s, "vite.New(") || len(newServerLine.FindAllString(s, -1)) != 1 {
		return nil, false
	}
	block := "\t// The frontend Vite builds (frontend/, into public/build): @vite.Tags\n" +
		"\t// in views/layout.templ, and /build/ in routes/web.go.\n" +
		"\tif _, err := vite.New(app, public.Files); err != nil {\n\t\treturn nil, err\n\t}\n"
	s = s[:loc[0]] + block + s[loc[0]:]
	s, ok := addImports(s, `"anetos.dev/anetos/view/vite"`, `"`+module+`/public"`)
	if !ok {
		return nil, false
	}
	out, err := format.Source([]byte(s))
	if err != nil {
		return nil, false
	}
	return out, true
}

var assetsRoute = regexp.MustCompile(`(?m)^([ \t]*)r\.Get\("/assets/\{path\.\.\.\}"[^\n]*\n`)

// patchRoutesVite routes /build/ after /assets/ in routes/web.go.
func patchRoutesVite(src []byte) ([]byte, bool) {
	s := string(src)
	loc := assetsRoute.FindStringSubmatchIndex(s)
	if loc == nil || strings.Contains(s, "vite.Handler()") {
		return nil, false
	}
	indent := s[loc[2]:loc[3]]
	s = s[:loc[1]] + indent + "r.Get(\"/build/{path...}\", web.WrapHandler(vite.Handler()))\n" + s[loc[1]:]
	s, ok := addImports(s, `"anetos.dev/anetos/view/vite"`)
	if !ok {
		return nil, false
	}
	out, err := format.Source([]byte(s))
	if err != nil {
		return nil, false
	}
	return out, true
}

var uiHeadLine = regexp.MustCompile(`(?m)^([ \t]*)@ui\.Head\(\)[ \t]*\n`)

// patchLayoutVite loads the entry after @ui.Head() (the CSS framework's
// stylesheet), so the frontend's styles come later and win.
func patchLayoutVite(src []byte, entry string) ([]byte, bool) {
	s := string(src)
	loc := uiHeadLine.FindStringSubmatchIndex(s)
	if loc == nil || strings.Contains(s, "vite.Tags(") || len(uiHeadLine.FindAllString(s, -1)) != 1 {
		return nil, false
	}
	indent := s[loc[2]:loc[3]]
	s = s[:loc[1]] + indent + "@vite.Tags(\"" + entry + "\")\n" + s[loc[1]:]
	return addTemplImport(s, `"anetos.dev/anetos/view/vite"`)
}

// addImports adds Go imports to the first import block, beside the
// anetos.dev ones (or the module's own, for a path of the module), for
// gofmt to sort.
func addImports(s string, imports ...string) (string, bool) {
	start := strings.Index(s, "import (\n")
	if start < 0 {
		return s, false
	}
	end := strings.Index(s[start:], "\n)\n")
	if end < 0 {
		return s, false
	}
	end += start
	block := s[start+len("import (\n") : end+1]
	lines := strings.SplitAfter(block, "\n")
	for _, imp := range imports {
		if strings.Contains(block, imp) {
			continue
		}
		// Insert after the last line of the group whose first path segment
		// matches: anetos.dev/… for the framework's, the module's for its own.
		prefix := `"anetos.dev/`
		if !strings.HasPrefix(imp, prefix) {
			prefix = imp[:strings.Index(imp[1:], "/")+2]
		}
		at := -1
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), prefix) {
				at = i
			}
		}
		if at < 0 {
			return s, false
		}
		lines = append(lines[:at+1], append([]string{"\t" + imp + "\n"}, lines[at+1:]...)...)
	}
	return s[:start+len("import (\n")] + strings.Join(lines, "") + s[end+1:], true
}

// addTemplImport adds an import to a templ file's import block, sorted
// among the anetos.dev ones.
func addTemplImport(s, imp string) ([]byte, bool) {
	out, ok := addImports(s, imp)
	if !ok {
		return nil, false
	}
	// templ files aren't gofmt's: sort the group by hand.
	start := strings.Index(out, "import (\n") + len("import (\n")
	end := start + strings.Index(out[start:], "\n)\n")
	lines := strings.Split(out[start:end], "\n")
	groupStart := -1
	for i := 0; i <= len(lines); i++ {
		in := i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), `"anetos.dev/`)
		switch {
		case in && groupStart < 0:
			groupStart = i
		case !in && groupStart >= 0:
			sortLines(lines[groupStart:i])
			groupStart = -1
		}
	}
	return []byte(out[:start] + strings.Join(lines, "\n") + out[end:]), true
}

func sortLines(l []string) {
	for i := 1; i < len(l); i++ {
		for j := i; j > 0 && strings.TrimSpace(l[j]) < strings.TrimSpace(l[j-1]); j-- {
			l[j], l[j-1] = l[j-1], l[j]
		}
	}
}

const dockerFrontendStage = `# The frontend (frontend/): built by Vite into %s, which the
# Go stage embeds. The packages first: in BuildKit's cache. With pnpm,
# yarn or bun, copy its lockfile and install with it instead.
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
RUN npm run build

`

var (
	golangStage = regexp.MustCompile(`(?m)^FROM golang:[^\n]*\n`)
	copyAll     = regexp.MustCompile(`(?m)^COPY \. \.\n`)
	anetosBuild = regexp.MustCompile(`go tool anetos build -o `)
)

// patchDockerfileVite adds the Node stage, copies its build (out, under
// the project) into the Go stage, and builds the binary without building
// the frontend again.
func patchDockerfileVite(src []byte, out string) ([]byte, bool) {
	s := string(src)
	if strings.Contains(s, "AS frontend") || len(golangStage.FindAllString(s, -1)) != 1 ||
		len(copyAll.FindAllString(s, -1)) != 1 || len(anetosBuild.FindAllString(s, -1)) != 1 {
		return nil, false
	}
	loc := copyAll.FindStringIndex(s)
	s = s[:loc[1]] + "COPY --from=frontend /src/" + out + " ./" + out + "\n" + s[loc[1]:]
	s = anetosBuild.ReplaceAllString(s, "go tool anetos build --no-frontend -o ")
	loc = golangStage.FindStringIndex(s)
	s = s[:loc[0]] + fmt.Sprintf(dockerFrontendStage, out) + s[loc[0]:]
	return []byte(s), true
}
