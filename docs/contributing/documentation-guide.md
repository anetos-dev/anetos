# Anetos — Documentation Guide

> **Name.** Write *Anetos* in prose and `anetos` for the command, the
> module (`anetos.dev/anetos`) and packages. Never "AnetOS", "aNETos" or a
> short form such as "anet" (design D185).

| | |
|---|---|
| **Status** | Active; applies from the first commit |
| **Owner** | Samiul Hoque |
| **Last updated** | 2026-09-29 |
| **Related** | [Roadmap](../planning/roadmap.md) · [Design document](../design/design.md) |

**The rule:** documentation is written **with** the code, in the **same pull
request**. A feature without docs is not finished, however good the code is.
This applies to human contributors and AI assistants (Claude Code etc.) alike.

---

## 1. Why this matters

Good documentation is a large part of what makes a framework easy to
adopt. For Anetos, docs are also a **design tool**: if a feature is hard to explain in a
short guide, the API is probably wrong. We fix the API rather than write
longer docs.

---

## 2. Definition of Done

Every PR that changes behavior must tick all of these (the PR template
contains this list):

- [ ] **Code** is complete and passes CI (`lint`, `test -race`, `govulncheck`).
- [ ] **Tests** cover the change, including failure paths.
- [ ] **Godoc**: every new or changed exported identifier has a doc comment
      (§6).
- [ ] **API**: a new or changed exported identifier follows the
      [API guidelines](api-guidelines.md), `api/*.txt` is updated
      (`make api-update`), and a renamed or removed one is deprecated,
      not removed.
- [ ] **User docs** added or updated: guide, concept or reference page
      (§4–5).
- [ ] **Examples compile**: code in the docs comes from compiled sources or
      is marked illustrative (§7).
- [ ] **CHANGELOG** has an entry under `Unreleased` (§9).
- [ ] **Breaking change?** Migration notes in the CHANGELOG and the upgrade
      guide (§9).
- [ ] **Design impact?** Decision log / ADR updated (§10).
- [ ] **Roadmap impact?** WP status or scope updated in the roadmap.
- [ ] **First pages**: the README (the first thing a developer sees),
      the website's home page and the docs' install page still say
      what is true: the status and latest version, the summary of each
      release, and the commands they show.

Docs-only PRs are welcome and follow the same review rules.

### Docs first for public APIs

For any **new public API**, write the guide section or example **before**
(or together with) the implementation, and put it in the PR description or a
draft PR. Review the *usage* first, then the implementation.

---

## 3. Where documentation lives

```
docs/
├── planning/
│   └── roadmap.md               plan, milestones, work packages (internal)
├── design/
│   ├── design.md                architecture and decisions (internal)
│   └── adr/                     architecture decision records (internal)
├── contributing/
│   └── documentation-guide.md   this file
└── site/                        user-facing docs (becomes the docs website at v0.3)
    ├── getting-started/         tutorials
    ├── guides/                  how-to guides, one per feature area
    ├── concepts/                explanations
    ├── reference/               config, CLI, API reference (partly generated)
    ├── upgrade/                 upgrade guides per version
    └── images/                  screenshots and other images the pages show
examples/                        compiled example code used by the docs
CHANGELOG.md
```

- **Internal docs** (`planning/`, `design/`, `contributing/`) explain *why*
  and *how we work*. Users don't need them.
- **User docs** (`docs/site/`) explain *how to use Anetos*. Write them in
  standard Markdown with front matter, so they move to whichever site
  generator we choose at v0.3 (roadmap Q4) without rewriting.
- **Godoc** lives in the Go source and is the API reference on
  pkg.go.dev.

---

## 4. Kinds of documentation

We follow the [Diátaxis](https://diataxis.fr) model. Each page is **one** of
these kinds; don't mix them.

| Kind | Purpose | Reader's question | Location | Example |
|---|---|---|---|---|
| **Tutorial** | Learn by building | "Teach me" | `site/getting-started/` | "Build your first blog" |
| **How-to guide** | Get a task done | "How do I…?" | `site/guides/` | "Send email from a queued job" |
| **Concept** | Understand how it works | "Why / how does this work?" | `site/concepts/` | "The runtime supervisor" |
| **Reference** | Look up facts | "What exactly is…?" | `site/reference/` + godoc | Config keys, CLI flags |

**What each work package needs, at minimum:**

- A **how-to guide** for each user-facing feature.
- A **concept page** for each major component (router, data layer, runtime,
  queue, plugins…).
- **Reference** entries for new config keys, CLI commands and flags.
- The **getting-started tutorial** is updated whenever a change affects the
  first-hour experience.

---

## 5. Page templates

### 5.1 How-to guide

```markdown
---
title: Send email from a queued job
since: v0.2.0
group: "Background work"
weight: 406
---

# Send email from a queued job

One sentence: what you'll achieve.

## Before you start
- Prerequisites (links), required config.

## Steps
1. …numbered, each with a code block…

## Complete example
A full, working version (from `examples/`).

## How it works
Short explanation; link to the concept page for depth.

## Testing it
How to test this with `anetostest` fakes.

## Common problems
Symptom → cause → fix.

## Next steps
Related guides.
```

Every page of `getting-started/`, `guides/`, `concepts/` and
`reference/` (but the folders' `README.md`) has a `group:` and a
`weight:` in its front matter. The docs site shows each group as a
folder of the sidebar (the page's URL stays `/guides/<file>/`); the
weight orders the pages in their group, and the groups by their
pages' weights. Give a group's pages weights next to each other (the
groups of guides are hundreds: Basics 100–199, Data 200–299…; a small
group may take part of one, after the pages it follows, as the CSS
frameworks' 150–155), and put a new page where a reader would look for it in
the order of work, not the alphabet. A group's name mustn't be a page's
file name (the group "Installation" next to `installation.md` would
take its URL). Pages of a subfolder (the tutorial's parts) have a
weight only. `make docs-check` checks all this (`internal/cmd/docnav`).

Images live in `docs/site/images/<topic>/`, screenshots as WebP. A page
shows one with a relative path (`![…](../images/kits/pico-list-light.webp)`)
and alt text saying what it shows. `make docs-check` checks that each
image a page shows exists and has alt text, and that each image is shown
by a page (remove one no page shows). Make screenshots with a script, so
they can be made again when the pages change: the CSS frameworks' come
from `scripts/kit-screenshots/run.sh`; run it after changing their
components.

### 5.2 Concept page

Summary → the mental model (a diagram if it helps) → the lifecycle or data
flow → design trade-offs and guarantees (e.g. at-least-once delivery) → links
to guides and reference.

### 5.3 Reference page

Tables, not prose. Every row: name, type, default, description; a row
added after v0.5 says "Since v0.N" in its description. **Generate
reference pages from source where possible** (config structs, CLI command
definitions) so they can't drift. Until a `docs:gen` task does, keep them
in sync by hand as part of the Definition of Done.

### 5.4 "Coming from another framework?" boxes

Readers come from many frameworks. Where it helps, add a short callout
mapping the concept to its name in several of them, in alphabetical
order:

```markdown
> **Coming from another framework?** A job is what Django apps run as
> a Celery task, Laravel as a queued job, and Rails as an Active Job.
> Here the workers run inside your app's own binary.
```

Use them sparingly (at most one per page). Name several frameworks or
none, in alphabetical order unless the sentence contrasts them: a box
never maps Anetos to one framework alone. Never write "just
like X" when the behavior differs.

---

## 6. Godoc standards

- **Every exported identifier** has a doc comment beginning with its name:
  `// Dispatch sends a job to its queue…`. Exported struct fields and
  interface methods too (a line comment after a field is enough);
  `make api-docs` checks this in CI.
- **Every package** has a `doc.go` with an overview: what the package is for,
  its main types, a minimal usage snippet, and links to the user guide.
- **Document the contract, not the implementation:** parameters, return
  values, **errors returned**, **concurrency safety** ("safe for concurrent
  use"), zero-value behavior, and context cancellation behavior.
- **Runnable examples:** key APIs get `Example…` functions in
  `example_test.go`. They are compiled and run by `go test` and appear on
  pkg.go.dev.
- Deprecations: a `// Deprecated: Use X; Y is removed in v0.N.` paragraph,
  and `//go:fix inline` where it applies ([API guidelines](api-guidelines.md) §7).
- Link related identifiers with `[Name]` doc links (Go 1.19+ syntax).
- Every Go file starts with the license header `// SPDX-License-Identifier: Apache-2.0`
  on its first line, followed by a blank line, before any package doc comment.
  Generated files (first line `// Code generated … DO NOT EDIT.`) are exempt,
  and so is `examples/tutorial`: it is the project a reader of the tutorial
  has, as `anetos new` writes it.
- Unexported code gets comments where the *why* isn't obvious. No comments
  that just restate the code.

---

## 7. Code in documentation

Broken examples are the fastest way to lose trust.

1. **Runnable code lives in `examples/`** (or `Example` tests) and is
   **compiled in CI**. Docs pages show it by copying or embedding tagged
   regions:

   ```go
   // examples/queue/welcome/main.go
   // region: dispatch
   queue.Dispatch(c, jobs.SendWelcome{UserID: user.ID})
   // endregion
   ```

   Copy the region into the page and follow the block with a claim
   paragraph such as ``(Copied from [`examples/x`](…), region `name`.)``, or
   ``(Region `name`.)`` when the example file was already linked on the
   page. The link may name a file other than `main.go`
   (``[`examples/x/migrations.go`](…)``), including a `.templ` file, whose
   regions are shown in `templ` code blocks, and a frontend's TypeScript
   or JavaScript file (``[`examples/spa/frontend/src/api.ts`](…)``), in
   `ts`, `tsx`, `js` or `jsx` blocks. First-party plugins are
   compiled and tested too, so their regions can be claimed the same
   way (``[`plugins/postmark/plugin.go`](…)``). `make docs-check` (part of
   `make check`) fails if a claimed block differs from its region. Indentation shared by every line is ignored, so
   a region inside a function body can be shown unindented.
2. **Illustrative snippets** that aren't compiled (design sketches, partial
   fragments) must say so in a comment: `// illustrative`.
3. Code must be `gofmt`-formatted, use real package names, and have no `…`
   inside code meant to be copied. Show imports when a snippet is longer than
   about 10 lines or uses non-obvious packages.
4. Shell commands use `$` only when output is shown; otherwise, plain
   commands.
5. Never put real secrets, even example-looking ones, in docs; use `secret`,
   `changeme` or `${VAR}`.

---

## 8. Writing style

- **Audience:** developers building web applications in Go, including
  those new to Go who come from another language or framework. Assume
  programming experience; don't assume Anetos knowledge.
- **Anetos on its own terms.** Anetos is a new framework: easy to adopt,
  easy to extend, highly manageable, built the Go way. Describe what it
  does as a fact ("rules are separated by `|`"), never as a copy of
  another framework ("X's syntax", "the comfort of X"), and never write
  as if aimed at one framework's developers. Other frameworks appear in
  a neutral comparison that names several (the boxes of §5.4, the
  glossary's "Elsewhere" column) and in benchmarks. A single tool may be
  named for a fact: where a name comes from, a precedent in the decision
  log, or a clash to avoid (`gin.H`), never as the model or the
  audience.
- **Voice:** second person ("you"), present tense, active voice. "Anetos
  starts the workers", not "the workers will be started".
- **Short sentences, short paragraphs.** One idea per paragraph. Lead with
  the most important information.
- **Spelling:** US English, for consistency with Go and its ecosystem.
- **Headings:** sentence case ("Send email from a queued job").
- **Avoid:** "simply", "just", "easy", "obviously". They make readers who
  are stuck feel worse. Also avoid unexplained jargon and memes.
- **Be honest about trade-offs.** If something is at-least-once, not
  crash-safe, or slower in some case, say so plainly, with a callout.
- **Callouts:** `> **Note:**` (helpful context), `> **Warning:**` (data
  loss, security, surprising behavior), `> **Coming from another framework?**` (§5.4).
- **Link, don't duplicate.** Explain a concept once and link to it.
- **Terminology:** use the glossary terms exactly (§11).

---

## 9. CHANGELOG, versions & upgrade guides

- `CHANGELOG.md` follows [Keep a Changelog](https://keepachangelog.com).
  Every PR adds a line under `## [Unreleased]` in one of: **Added**,
  **Changed**, **Deprecated**, **Removed**, **Fixed**, **Security**.
- Entries are written for users: what changed and what they need to do,
  with the WP ID and PR number: `- Queue: retry with exponential backoff
  (B5, #123)`.
- **Breaking changes** are marked `**BREAKING:**` and include before/after
  code.
- On release, `Unreleased` becomes the version heading. For each minor
  version before 1.0, `docs/site/upgrade/v0.N.md` collects every breaking
  change with migration steps. Upgrade guides come newest first: the
  site orders them by the version in their file name, and
  `upgrade/README.md` lists them in the same order.
- v0.5 is the first public release and the docs' baseline: they say
  nothing about v0.1–v0.4 except where a former name is still read. What
  later versions add is marked with `since: v0.N.0` in front matter (the
  site shows it after v0.5.0) or an inline "Since v0.N" note.
- **Versioned docs:** docs.anetos.dev shows the latest release at its
  root, `main` at `/main/` (labeled unreleased) and older minor versions
  at `/v0.N/`, each from its newest patch tag (D320). So a page on
  `main` describes the next release: a fix to a released version's page
  goes live with a patch release cut from that version's last tag
  ([releasing](releasing.md#a-patch-release)).
- Driver and plugin modules keep their own `CHANGELOG.md`.

---

## 10. Internal documents

### Roadmap (`docs/planning/roadmap.md`)
Updated when WP scope or status changes, and fully reviewed at every
milestone end (re-estimate, move items, record changes in its change log).

### Design document (`docs/design/design.md`)
Describes the **current** intended design. When a PR changes the design,
update the relevant section **and** the decision log (§24) in the same PR.
Keep code in the design doc marked illustrative.

### ADRs (`docs/design/adr/`)
Write an ADR when a decision is significant, contested, hard to reverse, or
picks between real alternatives (e.g. router implementation, data layer
approach, serialization format).

- Copy [`0000-template.md`](../design/adr/0000-template.md) to
  `NNNN-short-title.md` with the next number.
- Link it from the decision log entry.
- **Accepted ADRs are not rewritten.** To change a decision, write a new ADR
  that supersedes the old one and update both statuses.

---

## 11. Glossary

The words of the docs, the code and discussion are those of the user
glossary, [`docs/site/concepts/glossary.md`](../site/concepts/glossary.md):
use them, and add a word there (with other frameworks' words for it, when they differ)
before using it anywhere else. The rule behind them is the API
guidelines' first: use the word developers already know, and a new word
only for a new idea.

One word is for contributors only:

| Term | Meaning |
|---|---|
| **Contract** | A service's interface that drivers implement (`cache.Store`, `storage.Backend`, `mailer.Transport`) |

---

## 12. Review checklist for docs

Reviewers check:

- [ ] Right page kind (tutorial / how-to / concept / reference), not mixed.
- [ ] Someone new to Anetos could follow it without asking questions.
- [ ] Code compiles (from `examples/`), or is marked illustrative.
- [ ] Trade-offs and failure modes are stated.
- [ ] Links work; no duplicated explanations.
- [ ] Glossary terms used correctly.
- [ ] `since` version set; CHANGELOG entry present.
- [ ] `group` and `weight` place the page where a reader looks for it (§5.1).
- [ ] Images have alt text; screenshots are current (§5.1).

---

## 13. AI-assisted contributions

Claude Code and other assistants follow this guide exactly like human
contributors. The repository's `CLAUDE.md` points here. In particular:
update docs, examples and the CHANGELOG in the same change; mark design
sketches as illustrative; never invent APIs in docs that don't exist in code.

---

## Document history

| Date | Change |
|---|---|
| 2026-09-29 | Initial guide |
| 2026-09-30 | §6: struct fields and interface methods need doc comments; `make api-docs` checks |
| 2026-10-01 | §7: regions of first-party plugins (`plugins/`) can be claimed like examples (B11) |
| 2026-10-06 | §5.1, §12: every page has a `group` and a `weight`, the docs site's sidebar groups; `make docs-check` checks them (M2, D242) |
| 2026-10-07 | §9: upgrade guides newest first |
| 2026-10-10 | §2, §6: the API guidelines, `api/*.txt` and deprecations in the Definition of Done; the `Deprecated:` form (M8a) |
| 2026-10-10 | §5.1, §12: images in `docs/site/images`, WebP screenshots made by a script, alt text; `make docs-check` checks them (K5, D307) |
| 2026-10-11 | §5.3, §9: v0.5 is the docs' baseline, later additions say "Since v0.N"; §11: the user glossary page is the glossary (M8b-6, D317) |
| 2026-10-11 | §2: the first pages (README, the website's home page, the install page) in the Definition of Done (after rc.1) |
| 2026-10-11 | §9: the docs site has one version per minor release; a fix to a released version's pages ships with a patch release (M2v, D320) |
| 2026-10-11 | §1, §5.4, §8, §11: Anetos described on its own terms; "Coming from another framework?" boxes name several frameworks (M8d) |
| 2026-10-27 | §7: regions of a frontend's TypeScript and JavaScript files can be claimed (SP2) |
