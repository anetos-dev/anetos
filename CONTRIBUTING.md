# Contributing to Anetos

Thank you for helping. This guide covers the framework's repository:
the core, the CLI, the drivers, the plugins, the examples and the docs
in `docs/site`. Translations go to
[anetos-dev/locales](https://github.com/anetos-dev/locales) (its README
says how), and the docs site's theme to
[anetos-dev/docs](https://github.com/anetos-dev/docs).

Everyone taking part follows the [code of conduct](https://github.com/anetos-dev/.github/blob/main/CODE_OF_CONDUCT.md).
Report a security vulnerability privately, as [SECURITY.md](SECURITY.md)
says, never in an issue. Ask questions in
[Discussions](https://github.com/anetos-dev/anetos/discussions).

## Before you start

- **A fix** (a bug, a typo, a broken example): send a pull request.
- **Anything bigger** (a feature, a new API, a change of behavior):
  open an issue first, and agree on the design there before writing
  the code. Anetos designs first: what an API is called and how it's
  shaped is the hard part, and the
  [design document](docs/design/design.md) records each decision. An
  agreed design saves you rewriting a finished pull request.

## Setting up

You need Go 1.26 or later and `make`. Then:

```sh
git clone https://github.com/anetos-dev/anetos && cd anetos
make test-short  # the fast tests
make test        # the tests, with the race detector
make check       # formatting, the docs and API checks, lint, the tests
make help        # the other targets
```

`make check` also needs [golangci-lint](https://golangci-lint.run) v2.
The race detector needs cgo (a C compiler) on Linux and Windows, and
the Makefile a Unix shell: on Windows, use WSL. CI also runs
`make vuln` (govulncheck).

The PostgreSQL, MySQL and Redis tests run when
`ANETOS_TEST_POSTGRES_URL`, `ANETOS_TEST_MYSQL_URL` and
`ANETOS_TEST_REDIS_URL` are set, and skip otherwise; CI runs them.
The repository has several Go modules (the core, `cli`, `admin`,
`drivers/*`, `plugins/*`, some examples), and the make targets run in
each.

For a docs page only, `make docs-check` checks the code blocks, the
sidebar group and the images. To preview the site, build it from
[anetos-dev/docs](https://github.com/anetos-dev/docs) next to this
checkout, as its README says.

## What a change includes

A pull request is ready when it meets the Definition of Done in the
[documentation guide](docs/contributing/documentation-guide.md) §2, which
the pull request template lists:

- tests, failure paths included, passing on the minimum Go version
  (`go.mod`) and the latest;
- a doc comment on every exported identifier you add or change;
- the user docs in `docs/site` (a guide, a concept page or the
  reference), with code taken from `examples/` or marked
  `// illustrative`;
- an entry in `CHANGELOG.md` under `Unreleased`, and for a breaking
  change, a section in the upgrade guide (`docs/site/upgrade/`);
- for the public API, the [API guidelines](docs/contributing/api-guidelines.md),
  and `make api-update` to update `api/*.txt`. A renamed or removed
  identifier stays one minor version, marked `Deprecated:`.

The words the docs and the API use are in the
[glossary](docs/site/concepts/glossary.md): use the word Go, Laravel or
Rails developers already know, and a new word only for a new idea.

## Commits and pull requests

- One change per pull request. Small ones are reviewed sooner.
- The commit message says what changed and why. Its first line is
  short, starting with the roadmap's work package when there is one
  (`M8c: …`).
- CI must be green. A benchmark regression needs a maintainer's
  `benchmark-ok` label and a reason.
- A maintainer reviews every pull request. Expect questions about names
  and docs as much as about code.

## License

Anetos is licensed under the [Apache License 2.0](LICENSE). What you
send is licensed under it too, as its section 5 says: there is no
contributor license agreement and no sign-off to add. You keep the
copyright of your work. Every new Go file starts with
`// SPDX-License-Identifier: Apache-2.0`. Don't copy code under an
incompatible license (GPL, for example).

## AI assistants

You're welcome to use them; the maintainer does. You answer for every
line you send as if you had written it yourself, so read and test it.
Say in the pull request when an assistant wrote much of the change.
Assistants follow the same rules as everyone, and
[`CLAUDE.md`](CLAUDE.md) gives them the repository's rules.

## How decisions are made

[GOVERNANCE.md](GOVERNANCE.md) says who decides, and how you can become
a maintainer.
