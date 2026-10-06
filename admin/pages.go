// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var embedded embed.FS

// staticFS is the admin's own stylesheet and script.
var staticFS = mustSub(embedded, "static")

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// buttonData is a button and the CSRF token its form posts.
type buttonData struct {
	buttonView
	CSRF string
}

var funcs = template.FuncMap{
	"button": func(b buttonView, csrf string) buttonData { return buttonData{b, csrf} },
	"inputType": func(kind string) string {
		switch kind {
		case "datetime":
			return "datetime-local"
		case "email", "number", "date", "password":
			return kind
		}
		return "text"
	},
}

// pageNames are the admin's pages, each a template file defining
// "content" for the layout.
var pageNames = []string{"home", "list", "show", "form", "roles", "role", "roleform", "activity", "entry", "bulkop", "jobs", "job", "schedule"}

// parts are the templates of sections and the banner.
var parts = template.Must(template.New("parts").Funcs(funcs).ParseFS(templateFS, "templates/parts.html"))

// part renders one of the parts.
func part(name string, data any) (template.HTML, error) {
	var b bytes.Buffer
	if err := parts.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil //nolint:gosec // html/template's output
}

// parsePages parses each page with the layout.
func parsePages() (map[string]*template.Template, error) {
	layout, err := template.New("admin").Funcs(funcs).ParseFS(templateFS, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	out := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		t, err := template.Must(layout.Clone()).ParseFS(templateFS, "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		out[name] = t
	}
	return out, nil
}
