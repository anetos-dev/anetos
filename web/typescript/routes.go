// SPDX-License-Identifier: Apache-2.0

package typescript

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"anetos.dev/anetos/web"
)

// routeParam is a path parameter of a pattern: {name} or {name...}.
var routeParam = regexp.MustCompile(`\{([^}.]+)(\.\.\.)?\}`)

// routesFile is routes.ts: route(name, params, query), the path of each
// named route of r (not those of a Host router, which a path can't
// reach, nor those whose names start with one of exclude), with the
// names and their parameters typed. A pattern's {$} (the path ends
// there) is no parameter, and no part of the path.
func routesFile(r *web.Router, exclude []string) []byte {
	paths := map[string]string{}
	for _, rt := range r.Routes() {
		if rt.Name == "" || rt.Host != "" || slices.ContainsFunc(exclude, func(p string) bool { return strings.HasPrefix(rt.Name, p) }) {
			continue
		}
		if _, ok := paths[rt.Name]; !ok {
			paths[rt.Name] = strings.ReplaceAll(rt.Pattern, "{$}", "")
		}
	}
	names := slices.Sorted(mapsKeys(paths))
	var b bytes.Buffer
	b.WriteString(header)
	b.WriteString("/** The parameters of each named route's path. */\nexport interface RouteParams {\n")
	for _, name := range names {
		var params []string
		for _, m := range routeParam.FindAllStringSubmatch(paths[name], -1) {
			params = append(params, m[1]+": string | number")
		}
		fmt.Fprintf(&b, "  %s: {%s}\n", quoteTS(name), braced(params))
	}
	b.WriteString("}\n\n/** A named route's name. */\nexport type RouteName = keyof RouteParams\n\n")
	b.WriteString("/** The names of the routes whose paths have no parameters: links of a menu. */\n" +
		"export type PlainRouteName = { [N in RouteName]: keyof RouteParams[N] extends never ? N : never }[RouteName]\n\n")
	b.WriteString("const paths: Record<RouteName, string> = {\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %s: %s,\n", quoteTS(name), quoteTS(paths[name]))
	}
	b.WriteString("}\n\n")
	b.WriteString(routeTS)
	return b.Bytes()
}

// braced is params inside an object type's braces: " a: …; b: … " or "".
func braced(params []string) string {
	if len(params) == 0 {
		return ""
	}
	return " " + strings.Join(params, "; ") + " "
}

// routeTS is route() and its prefix.
const routeTS = `/** A query's values: undefined and null leave a name out, arrays repeat it. */
export type Query = Record<string, string | number | boolean | null | undefined | (string | number | boolean)[]>

type RouteArgs<N extends RouteName> = keyof RouteParams[N] extends never
  ? [params?: RouteParams[N], query?: Query]
  : [params: RouteParams[N], query?: Query]

let prefix = ''

/**
 * setRoutePrefix sets what route() puts before every path: the locale's
 * ("/bn") with APP_LOCALE_STRATEGY=prefix, "" otherwise.
 */
export function setRoutePrefix(p: string): void {
  prefix = p.replace(/\/+$/, '')
}

/**
 * route returns the path of a named route, with its parameters and a
 * query:
 *
 *	route('posts.show', { id: 3 })            // "/posts/3"
 *	route('posts.index', {}, { page: 2 })      // "/posts?page=2"
 */
export function route<N extends RouteName>(name: N, ...[params, query]: RouteArgs<N>): string {
  const values = (params ?? {}) as Record<string, string | number>
  const path = paths[name].replace(/\{([^}.]+)(\.\.\.)?\}/g, (_, key: string, rest?: string) => {
    const v = String(values[key] ?? '')
    return rest ? v.split('/').map(encodeURIComponent).join('/') : encodeURIComponent(v)
  })
  const q = new URLSearchParams()
  for (const [key, v] of Object.entries(query ?? {})) {
    if (v === undefined || v === null) continue
    for (const item of Array.isArray(v) ? v : [v]) q.append(key, String(item))
  }
  const s = q.toString()
  return prefix + path + (s ? '?' + s : '')
}
`
