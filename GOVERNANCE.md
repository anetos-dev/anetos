# Governance

How decisions about Anetos are made, by whom, and how that changes as the
project grows. It covers every repository of the
[anetos-dev](https://github.com/anetos-dev) organization.

## Roles

- **Lead maintainer:** Samiul Hoque, who created Anetos. He sets its
  direction (the [roadmap](docs/planning/roadmap.md)), decides when
  maintainers disagree, and cuts the releases.
- **Maintainers:** people who review and merge pull requests, triage
  issues, take part in design decisions and act on code of conduct
  reports. Today the lead maintainer is the only one.
- **Contributors:** anyone who opens an issue, answers a question,
  reviews or sends a pull request.

## How decisions are made

- In public: in issues, pull requests and Discussions, where anyone can
  comment. Security and conduct reports are the exception: they stay
  private.
- By consensus when possible. When there is none, the lead maintainer
  decides, and says why.
- Design decisions are recorded in the
  [design document](docs/design/design.md)'s decision log, with their
  reasons, so they can be revisited when the reasons change.
- Breaking changes follow the roadmap's
  [versioning rules](docs/planning/roadmap.md#versioning-rules) and the
  design document's §23: before 1.0 they come in a minor version, with
  an upgrade guide, and a renamed or removed identifier stays one minor
  version, marked `Deprecated:`.

## Becoming a maintainer

Someone who has contributed well for a while (code, reviews, docs,
translations, help in Discussions) and shares the project's principles
can be invited by the maintainers. A maintainer gets write access to the
repositories they work on and agrees to follow this document and the
[code of conduct](https://github.com/anetos-dev/.github/blob/main/CODE_OF_CONDUCT.md).
A maintainer other than the lead who is inactive for six months becomes
an emeritus maintainer, and can come back by asking.

## As the project grows

- With a second maintainer, every pull request needs a review by a
  maintainer other than its author before it is merged.
- If the lead maintainer steps away, he hands the role to a maintainer.
  If he can't, the maintainers choose a new lead together. The
  organization, the repositories and the anetos.dev domain go with the
  role. While he is the only maintainer, there is no one to hand it to:
  the code's Apache 2.0 license lets anyone carry it on in a fork.
- Changes to this document are made by pull request, and need the lead
  maintainer's approval.

## Conduct and security

Everyone follows the
[code of conduct](https://github.com/anetos-dev/.github/blob/main/CODE_OF_CONDUCT.md).
Vulnerabilities are reported privately, as [SECURITY.md](SECURITY.md)
says.
