# Anetos — Documentation Guide

> **Working codename.** "Anetos" is a placeholder until the final name is chosen.

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

Laravel became popular because of its documentation as much as its code. For
Anetos, docs are also a **design tool**: if a feature is hard to explain in a
short guide, the API is probably wrong. We fix the API rather than write
longer docs.

---

## 2. Definition of Done

Every PR that changes behaviour must tick all of these (the PR template
contains this list):

- [ ] **Code** is complete and passes CI (`lint`, `test -race`, `govulncheck`).
- [ ] **Tests** cover the change, including failure paths.
- [ ] **Godoc**: every new or changed exported identifier has a doc comment
      (§6).
- [ ] **User docs** added or updated: guide, concept or reference page
      (§4–5).
- [ ] **Examples compile**: code in the docs comes from compiled sources or
      is marked illustrative (§7).
- [ ] **CHANGELOG** has an entry under `Unreleased` (§9).
- [ ] **Breaking change?** Migration notes in the CHANGELOG and the upgrade
      guide (§9).
- [ ] **Design impact?** Decision log / ADR updated (§10).
- [ ] **Roadmap impact?** WP status or scope updated in the roadmap.

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
    └── upgrade/                 upgrade guides per version
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

### 5.2 Concept page

Summary → the mental model (a diagram if it helps) → the lifecycle or data
flow → design trade-offs and guarantees (e.g. at-least-once delivery) → links
to guides and reference.

### 5.3 Reference page

Tables, not prose. Every row: name, type, default, description, `since`
version. **Generate reference pages from source where possible** (config
structs, CLI command definitions) so they can't drift. A `docs:gen` task is
planned for v0.3; until then, keep them in sync by hand as part of the
Definition of Done.

### 5.4 "Coming from Laravel?" boxes

Our audience includes many Laravel developers. Where it helps, add a short
callout mapping the concept:

```markdown
> **Coming from Laravel?** `app.Worker(...)` plays the role of
> `php artisan queue:work`, but runs inside your app's own binary.
```

Use them sparingly (at most one per page). Never write "just like Laravel"
when the behaviour differs.

---

## 6. Godoc standards

- **Every exported identifier** has a doc comment beginning with its name:
  `// Dispatch sends a job to its queue…`
- **Every package** has a `doc.go` with an overview: what the package is for,
  its main types, a minimal usage snippet, and links to the user guide.
- **Document the contract, not the implementation:** parameters, return
  values, **errors returned**, **concurrency safety** ("safe for concurrent
  use"), zero-value behaviour, and context cancellation behaviour.
- **Runnable examples:** key APIs get `Example…` functions in
  `example_test.go`. They are compiled and run by `go test` and appear on
  pkg.go.dev.
- Use `// Deprecated: use X instead.` (with the version) for deprecations.
- Link related identifiers with `[Name]` doc links (Go 1.19+ syntax).
- Every Go file starts with the license header `// SPDX-License-Identifier: Apache-2.0`
  on its first line, followed by a blank line, before any package doc comment.
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
   page. `make docs-check` (part of `make check`) fails if a claimed block
   differs from its region.
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

- **Audience:** Go developers, including those new to Go who come from
  Laravel, Rails or Django. Assume programming experience; don't assume
  Anetos knowledge.
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
  loss, security, surprising behaviour), `> **Coming from Laravel?**` (§5.4).
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
  change with migration steps.
- User docs mark new features with `since: v0.N.0` in front matter or an
  inline "Since v0.N" note.
- **Versioned docs:** the docs site (v0.3+) publishes docs per minor
  version; `main` is labeled "unreleased".
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

Use these terms consistently in code, docs and discussion.

| Term | Meaning |
|---|---|
| **App** | The application instance (`*anetos.App`) that owns config, services and the runtime |
| **Component** | A long-running unit supervised by the runtime (HTTP server, worker pool, listener, scheduler, `app.Go` task) |
| **Role** | A named group of components a process runs (`http`, `workers`, `listeners`, `scheduler`) |
| **Supervisor** | The runtime part that starts, restarts and stops components |
| **Contract** | A service interface (e.g. `cache.Store`) |
| **Driver** | An implementation of a contract (e.g. the Redis cache driver) |
| **Plugin** | A package that extends an app through the `ext.Plugin` interface |
| **Provider** | The internal equivalent of a plugin, used by the framework's own features |
| **Job** | A typed unit of background work sent to a queue |
| **Worker** | A component that processes jobs from a queue |
| **Event** | A typed in-process message emitted with `events.Emit` |
| **Listener** | A handler for an event, *or* a component consuming an external pub/sub topic. Say "event listener" or "pub/sub listener" when it's ambiguous |
| **Handler** | An HTTP handler in one of the three forms (plain, context, typed) |
| **Model** | A struct mapped to a database table |
| **Scope** | A reusable query modifier function |

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
