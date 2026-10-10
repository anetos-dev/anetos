# CLAUDE.md

Instructions for AI assistants working in this repository.

## Project

Anetos (Greek άνετος, "at ease"; module `anetos.dev/anetos`, GitHub org
`anetos-dev`) is an open-source, batteries-included Go web
framework. Read these before making changes:

- `docs/planning/roadmap.md`: milestones and work packages (WP IDs like F5, B7)
- `docs/design/design.md`: principles, architecture, decision log
- `docs/contributing/documentation-guide.md`: documentation rules (mandatory)

Minimum Go version: 1.26 (`go.mod`). Code must build and pass tests on the
minimum and the latest Go release (design D18).

## Non-negotiable rules

1. **Docs ship with code.** Every behaviour change updates godoc, user docs
   (`docs/site/`), examples and `CHANGELOG.md` in the same change; a breaking
   change also gets a section in the upgrade guide
   (`docs/site/upgrade/v0.N.md`, the version that ships it). Follow the
   Definition of Done in the documentation guide §2.
2. **Design principles** (design.md §2) win arguments: stdlib-compatible,
   explicit over magic, typed, no per-request reflection, small core with
   heavy dependencies in driver modules.
3. **No package name may shadow a standard library package** (design.md §5).
4. **Design changes** update design.md (section + decision log §24), plus an
   ADR for significant decisions.
5. Don't add third-party dependencies to the core module without stating
   why; prefer driver/plugin modules.
6. Code in docs compiles (from `examples/`) or is marked `// illustrative`.
   Never document APIs that don't exist.
7. Reference the WP ID in commit messages and CHANGELOG entries.
8. The project is licensed under Apache-2.0. Every Go file starts with
   `// SPDX-License-Identifier: Apache-2.0` on its first line (generated
   files, starting with `// Code generated … DO NOT EDIT.`, are exempt, and
   so is `examples/tutorial`, a reader's project as `anetos new` writes it). Don't copy code
   from sources with incompatible licenses (e.g. GPL) into the repo.

## Commands

```bash
make check     # everything CI runs: gofmt, SPDX headers, doc snippets, generated code, vet, lint, race tests, allocation budgets
make docs-check  # doc code blocks match examples/ regions; pages have a sidebar group and weight
make api-docs  # every exported identifier (fields, interface methods too) has a doc comment
make test      # go test -race ./...
make lint      # golangci-lint (v2 config in .golangci.yml)
make cover     # coverage summary
make bench     # benchmarks (bench/ compares with net/http, chi, Gin, Echo; results in docs/benchmarks/)
make bench-check    # allocation budgets of requests and queries (bench/budget_test.go)
make bench-compare  # benchmarks vs main (BASE=…), run in turns; fails on a regression
make vuln      # govulncheck
make help      # list targets
```

The repository has several Go modules (the core, `drivers/*`, the `cli`
developer tool, and examples that need drivers); make targets run in each. Driver modules
depend on the core through a `replace ../..` directive. The PostgreSQL,
MySQL and Redis conformance tests run when `ANETOS_TEST_POSTGRES_URL`,
`ANETOS_TEST_MYSQL_URL` and `ANETOS_TEST_REDIS_URL` are set (CI sets
them); otherwise they skip.

Run `make check` before every commit.

Generated files in the examples (`make gen-check` checks the models' code):

- After changing a model in `examples/bookmarks`, `examples/database`,
  `examples/forms`, `examples/saas`, `examples/tracker` or
  `examples/tutorial`, or a `.templ` file in `examples/forms`,
  `examples/queue`, `examples/saas`, `examples/tracker` or
  `examples/tutorial`, run `go generate ./...` there.
- After changing an API route, or the types its handler takes or answers,
  in `examples/bookmarks` or `examples/tracker`, or `web/openapi`'s output,
  update `openapi.json` there (its test fails otherwise). The examples have
  no `.env`: `APP_ENV=development APP_KEY=base64:$(head -c32 /dev/urandom | base64) go run . openapi`.
  `web/openapi`'s own golden files: `go test ./web/openapi -update`.
- After changing the generator's fixtures, run
  `go run ./cmd/anetos generate ./internal/modelgen/internal/...` in `cli/`.

`cli/` tests create and build a project with `anetos new` (and download
templ if the module cache lacks it); `go test -short` skips them. When
changing the project templates in `cli/internal/scaffold/templates`, run
those tests. Two examples are checked against the templates, file by file:
`examples/tutorial` (the web stack; `TestTutorialProject`) and
`examples/bookmarks` (the API stack, `make:auth` and `make:crud`;
`TestAPITutorialProject`, whose `undo` list reverts the API tutorial's
edits before comparing). Change the example, and the tutorial's page if
its code blocks change, with the template. `scaffold.TemplVersion` must
match the templ the examples use (`TestTemplVersion`): a dependency update
raising templ raises it too.

The allocation budgets (`bench/budget_test.go`, a module of its own) are
checked on the minimum and the latest Go in CI, and the counts differ
between Go releases: after changing a request's path (routing, binding,
middleware, sessions), run `go test -run TestBudgets .` in `bench/` on both.
