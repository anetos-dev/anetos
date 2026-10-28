---
title: Frontends
since: v0.6.0
group: "Web"
weight: 204
---

# Frontends

Anetos serves a frontend three ways: pages rendered on the server with
templ and htmx; pages written as React, Svelte or Vue components that
Inertia renders, with the Go side unchanged; and a JSON API with a
frontend of its own, a single-page app or a mobile app. This page is
how each works, what each keeps in Go, and how to choose.

## Three ways

**Server-rendered pages** (`anetos new`, the web stack). A handler
renders a templ component, which is Go: the compiler checks its
parameters, and it escapes what it prints. htmx swaps parts of a page
without a reload. Forms post, and an invalid one is redirected back
with its errors and what was typed, through the session. Node isn't
needed; `vite:install` adds a frontend for the pages' scripts and
styles when you want one. See [Server-rendered HTML](server-rendered-html.md).

**Pages as components** (`anetos new --stack=react`, `svelte` or
`vue`). The routes, handlers, sessions, validation and redirects are
the web stack's; a handler renders a page by naming its component and
giving its props, a Go struct. On the first visit the HTML holds the
page; Inertia's client renders it, then turns links and form posts into
requests the server answers with the next page's props, as JSON. The
props' and routes' TypeScript types are generated from the Go code. See
[Build pages in React](../guides/react.md), [in
Svelte](../guides/svelte.md), [in Vue](../guides/vue.md), and
[Inertia](../guides/inertia.md).

**An API and a frontend of its own** (`anetos new --stack=api`). The
handlers return typed results as JSON, described by an OpenAPI
document; a typed TypeScript client is generated from the same types.
The frontend is an app of its own: an SPA the binary serves at its
address (`vite:install`), or one in another repository or on a CDN, or
a framework with its own server, or a mobile app. A first-party SPA on
the same site logs in with the session cookie; other clients use
tokens. See [Build a single-page app](../guides/spa.md) and [Build an
API](../getting-started/build-an-api.md).

## What each keeps in Go

| | Server-rendered pages | Pages as components | An API and a frontend |
|---|---|---|---|
| Routes and URLs | Go | Go | Go for the API; the frontend's router for its pages |
| Rendering the HTML | The server (templ) | The browser (the component) | The browser, or the frontend's server |
| Data for a page | The handler passes it to the view | The handler's props | The frontend asks the API |
| Validation errors | Redirected back, in the session | Redirected back, in the `errors` prop | A `422` answer, by field |
| Login | Session cookie | Session cookie | Session cookie for a first-party SPA; tokens for other clients |
| CSRF | A token in forms and htmx's header | The `XSRF-TOKEN` cookie | The `XSRF-TOKEN` cookie for the SPA; none needed with tokens |
| Types shared with the frontend | Not needed: templ is Go | Props and routes, generated | The client, generated |
| Node | Not needed | To build | To build the SPA (or the frontend's own) |
| `make:auth`, `make:crud` | templ pages | The stack's components | JSON endpoints |
| Other clients (mobile apps, programs) | Add an API | Add an API | The same API |

The app is one binary: the templ views are compiled in, and a frontend
Vite builds is embedded in it, unless the frontend is served elsewhere
(another repository, a CDN, a framework's own server).

## Choosing

- **Mostly forms, lists and records**, or a team at home in Go and
  HTML: server-rendered pages. Fewest moving parts, no build step for
  the frontend, and links and forms that work without JavaScript, which
  htmx improves where you add it.
- **Pages with much client-side interaction** (editors, dashboards,
  drag and drop), or a team that knows React, Svelte or Vue: pages as
  components. The routes, logins and validation stay in Go, with no
  API to design or version.
- **Several clients** (an SPA and a mobile app, or other people's
  programs), or a frontend built and deployed by another team: an API.
  The API is a contract of its own, described by its OpenAPI document.

The choice isn't final. A web project can move its pages to
components one at a time ([Move htmx pages to
Inertia](../guides/migrate-htmx-to-inertia.md)): both kinds of pages
work in one app during the move. Moving to an API changes every page
and most handlers ([Move htmx pages to an API and an
SPA](../guides/migrate-htmx-to-spa.md)).

## Guarantees and trade-offs

- **Pages as components are rendered in the browser.** The first
  visit's HTML holds the page's data, not its markup: search engines
  that run scripts index it; those that don't see an empty page.
  Server-side rendering isn't supported yet. Server-rendered pages
  don't have this trade-off.
- **Node is a build tool, not a runtime.** The frontend is built before
  the binary (`anetos build`); the server needs neither Node nor
  `node_modules`.
- **The types follow the Go code.** Renaming a prop, a field of an API's
  result or a route changes the generated TypeScript, and the
  frontend's type check fails where it's used, before the browser
  runs it.
- **A first-party SPA's cookie needs one site.** The SPA and the API
  must be on the same site (one domain, or subdomains of one) for the
  session cookie; elsewhere, the SPA logs in with a token
  ([Protect your app in the browser](../guides/browser-security.md)).
- **An API project scaffolds one SPA's plumbing, not its pages.**
  `vite:install` writes a plain TypeScript SPA; `examples/spa` has one
  in React with the accounts' pages.

## Related

- [Server-rendered HTML](server-rendered-html.md)
- [Bundle JavaScript and CSS with Vite](../guides/vite.md)
- [Generate TypeScript types for the frontend](../guides/typescript.md)
- [HTTP request lifecycle](http-request-lifecycle.md)
