// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"fmt"
	"regexp"
)

// inertiaStack is what the generators know of an Inertia stack's
// framework (design D370): its files' names and the lines of its layout
// that make:auth and make:crud patch.
type inertiaStack struct {
	name    string // React: for people
	adapter string // @inertiajs/react: the client, which frontend/package.json depends on
	ext     string // .tsx: a component's file
	entry   string // src/app.tsx: the frontend's entry, which the root view loads
	ui      string // src/components/ui.tsx: the pages' components (a file, or a folder of them)
	// The layout (frontend/src/Layout.tsx): the comment make:auth puts
	// <AccountMenu /> in place of, the import it adds AccountMenu's
	// before (uiImport; both without the indentation a line has), and a
	// NavLink line, which make:crud adds one after.
	accountMarker *regexp.Regexp
	accountImport string
	uiImport      string
	navLinkLine   *regexp.Regexp
	navLink       func(route, key string) string
	// security is how the framework writes HTML unescaped, which the
	// security skill warns of.
	rawHTML string
}

// layout is the stack's layout, relative to the project.
func (s inertiaStack) layout() string { return "frontend/src/Layout" + s.ext }

// inertiaStacks are the Inertia stacks, by framework (InertiaStacks).
var inertiaStacks = map[string]inertiaStack{
	"react": {
		name: "React", adapter: "@inertiajs/react", ext: ".tsx", entry: "src/app.tsx", ui: "src/components/ui.tsx",
		accountMarker: regexp.MustCompile(`(?m)^([ \t]*)\{/\* The account's links \(go tool anetos make:auth adds them\)\. \*/\}[ \t]*(\r?)$`),
		accountImport: "import AccountMenu from './components/AccountMenu'",
		uiImport:      "import { Flash } from './components/ui'",
		navLinkLine:   regexp.MustCompile(`(?m)^([ \t]*)<NavLink route="[^"]*" label=\{t\('[^']*'\)\} />[ \t]*\r?\n`),
		navLink:       func(route, key string) string { return fmt.Sprintf("<NavLink route=%q label={t('%s')} />", route, key) },
		rawHTML:       "dangerouslySetInnerHTML",
	},
	"vue": {
		name: "Vue", adapter: "@inertiajs/vue3", ext: ".vue", entry: "src/app.ts", ui: "src/components/ui/",
		accountMarker: regexp.MustCompile(`(?m)^([ \t]*)<!-- The account's links \(go tool anetos make:auth adds them\)\. -->[ \t]*(\r?)$`),
		accountImport: "import AccountMenu from './components/AccountMenu.vue'",
		uiImport:      "import { Flash } from './components/ui'",
		navLinkLine:   regexp.MustCompile(`(?m)^([ \t]*)<NavLink route="[^"]*" :label="t\('[^']*'\)" />[ \t]*\r?\n`),
		navLink: func(route, key string) string {
			return fmt.Sprintf("<NavLink route=%q :label=\"t('%s')\" />", route, key)
		},
		rawHTML: "v-html",
	},
	"svelte": {
		name: "Svelte", adapter: "@inertiajs/svelte", ext: ".svelte", entry: "src/app.ts", ui: "src/components/ui/",
		accountMarker: regexp.MustCompile(`(?m)^([ \t]*)<!-- The account's links \(go tool anetos make:auth adds them\)\. -->[ \t]*(\r?)$`),
		accountImport: "import AccountMenu from './components/AccountMenu.svelte'",
		uiImport:      "import { Flash } from './components/ui'",
		navLinkLine:   regexp.MustCompile(`(?m)^([ \t]*)<NavLink route="[^"]*" label=\{t\('[^']*'\)\} />[ \t]*\r?\n`),
		navLink: func(route, key string) string {
			return fmt.Sprintf("<NavLink route=%q label={t('%s')} />", route, key)
		},
		rawHTML: "{@html}",
	},
}

// InertiaLayout is the layout of a project of an Inertia stack's
// framework (frontend/src/Layout.tsx for react); "" for another.
func InertiaLayout(framework string) string {
	if s, ok := inertiaStacks[framework]; ok {
		return s.layout()
	}
	return ""
}

// InertiaAccountImport is the line importing make:auth's AccountMenu in
// the layout of an Inertia stack's framework ([InertiaLayout]); "" for
// another.
func InertiaAccountImport(framework string) string { return inertiaStacks[framework].accountImport }
