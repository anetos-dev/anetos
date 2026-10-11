# Releasing Anetos

For maintainers. Contributors don't need this: CI checks what it
checks for them. Design D319 says why it works this way.

## The modules

The repository holds several Go modules. The **published** ones are the
core (`anetos.dev/anetos`), `cli`, `admin`, every `drivers/*` and every
`plugins/*`. They're released together, all with one version: v0.5.0
is `v0.5.0`, `cli/v0.5.0`, `admin/v0.5.0`, `drivers/sqlite/v0.5.0`… A
module that didn't change still gets the new tag. The examples and
`bench` are modules too, but they are never tagged.

Outside the repository, only a `go.mod`'s `require` lines count. Inside
it, the modules build against each other's code:

| Module | Inside the repository | Requires |
|---|---|---|
| Drivers, `admin`, plugins | A `replace` directive to the checkout, which consumers ignore | The last release of the modules it uses |
| `cli` | `cli/go.work`, whose `replace` reads the core from the checkout: `go install …@vX.Y.Z` refuses a module with a `replace` in its `go.mod`, but ignores a workspace | The last release of the core, or a newer published commit (below) |
| Examples, `bench` | `replace` directives | A placeholder (`v0.0.0-00010101000000-…`) or the last release |
| anetos-dev/locales | `replace` to a checkout of anetos beside it | The last release |

Every published module's `go.mod` retracts v0.1.0–v0.4.0, the tags from
before Anetos was announced. The sub-modules' tags among them can't be
installed: their `go.mod` requires the placeholder version.

### Between releases

- In `cli`, `make tidy` resolves the core packages `cli` imports from
  the version `cli/go.mod` requires (`go mod tidy` ignores `go.work`).
  When `cli` starts importing a core package newer than that version,
  tidy fails. Push the commit that adds the package, then raise the
  requirement to it, in `cli/`:
  `GOWORK=off go get anetos.dev/anetos@<commit> && go mod tidy`. Then
  `make tidy` updates the examples, which use `cli` as a tool.
- Dependabot leaves `anetos.dev/*` alone.
- CI's **release rehearsal** (`make release-rehearsal`, in the cli part
  on both Go versions) runs on every push:
  1. It exports `HEAD` (`git archive`; outside a git checkout, the
     working tree, leaving out what is usually untracked).
  2. It sets every published module to a made-up version, as
     `make release` would. The version is the release `version.go` is
     preparing, as a pre-release (`v0.5.0-rehearsal.<time>`), so the
     plugins' `Requires` ranges accept it.
  3. It zips each module, as the module proxy does, into a module proxy
     on disk.
  4. It `go install`s the tool from there, and runs `anetos new` for
     the web and the api stack, without `--replace`, then `make:auth`.
     In the web project it also runs `make:admin` and adds the postmark
     plugin.
  5. It builds, vets and tests both projects.

  It removes its versions from the module cache afterwards. Add
  `-keep` (`cd cli && go run ./internal/cmd/release rehearse -root ..
  -keep`) to keep everything for a look.

## Cutting a release

A tag is forever: the module proxy and the checksum database keep the
first content they see for a version. Never move or delete a pushed tag:
tag a new patch version instead.

1. **Before:**
   - CI is green on `main`, rehearsal included.
   - For a release (not a pre-release), `CHANGELOG.md`'s `Unreleased`
     becomes `## [X.Y.Z] - date`, and so does each driver's and
     plugin's CHANGELOG. A module without changes says so.
   - The upgrade guide is complete.
   - For v0.5.0 only: the install commands go back to `@latest`. Until
     v0.5.0, `@latest` finds `cli` v0.4.0, which can't be installed, so
     the README, the docs and the website name `@v0.5.0-rc.1`
     (`grep -rn 'cmd/anetos@v0' README.md docs/site ../website/content`).
2. **Prepare:** `make release VERSION=vX.Y.Z` (with the locales
   repository at `../locales`, or `LOCALES=…`).
   - It checks the CHANGELOG sections (not for a pre-release such as
     `v0.5.0-rc.1`).
   - It sets every requirement of a published module to the version,
     in every `go.mod` of the repository and in the locales' `go.mod`.
     It drops `cli/go.sum`'s lines for the core, whose new version has
     no checksum until it is published.
   - It prints the tags to create.
3. **Commit** "Release vX.Y.Z" in anetos, and "Release vX.Y.Z" in
   locales. CI stays green on it before the tags exist: the `replace`
   directives and `cli/go.work`'s read the modules from the checkout,
   go.mod files included, so nothing asks the proxy for the new
   version.
4. **Tag** the anetos commit with every tag `make release` printed
   (lightweight tags: `git tag v0.5.0`, `git tag cli/v0.5.0`…). Tag the
   locales commit with `vX.Y.Z` too.
5. **Push** the anetos commit, then its tags (`git push origin
   --tags`), then locales and its tag.
6. **Check** from a machine that reaches proxy.golang.org, in an empty
   directory:

   ```sh
   go list -m anetos.dev/anetos@vX.Y.Z anetos.dev/anetos/cli@vX.Y.Z
   GOBIN=$PWD/bin go install anetos.dev/anetos/cli/cmd/anetos@vX.Y.Z
   ./bin/anetos new demo && cd demo && go build ./... && go test ./...
   ```

   The proxy fetches a new tag on the first request, within a minute.
   Don't ask it for a version before its tag is pushed: it remembers
   the "not found" for a while.
7. **GitHub Release:** on the core's tag (`vX.Y.Z`) alone, one per
   version, titled with it. The notes: what the release is, the
   install command, the highlights, and links to the CHANGELOG and the
   upgrade guide at the tag. No files: `go install` builds the tool. A
   pre-release is marked "pre-release" and not "latest".
8. **After a release:** `develVersion` in `version.go` becomes the next
   `vX.Y+1.0-dev`, and the CHANGELOGs get a new `Unreleased` section.
   The published modules keep requiring the released version until the
   next release.

A pre-release (`vX.Y.Z-rc.N`) goes the same way, without step 1's
CHANGELOG change and step 8, both left for the release. `@latest` never picks a
pre-release while there is a release, and the go command reads
retractions from the latest release's `go.mod`, so a pre-release's
retractions show only from the next release.
