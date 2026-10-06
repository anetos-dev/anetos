# Security policy

## Reporting a vulnerability

Please don't open a public issue, discussion or pull request for a
vulnerability. Report it privately on GitHub:

**[Report a vulnerability](https://github.com/anetos-dev/anetos/security/advisories/new)**
(the repository's Security tab, then "Report a vulnerability").

Tell us what you can of:

- the module and version (`go list -m anetos.dev/anetos`, or the
  driver's or plugin's), and the Go version;
- what an attacker can do, and what they need first (an account, a
  setting, a position on the network);
- how to reproduce it: a request, a test, or a small app;
- the settings involved (`APP_ENV`, `SESSION_DRIVER`…), without your
  secrets.

If you can't use GitHub, say so in a public issue that asks for a
private contact, without details, and we will reply.

## What happens next

| When | What |
|---|---|
| Within 3 working days | We acknowledge the report |
| Within 10 working days | We tell you whether we confirm it, and how severe we think it is |
| Then | We fix it in a private fork, with tests, and agree a release date with you |
| On the release date | The fixed versions are published with a GitHub security advisory (and a CVE when it qualifies), crediting you unless you'd rather not |

We aim to release fixes within 90 days of the report, and much sooner
for severe ones. Please keep the report private until the advisory is
published. If we miss the dates above, remind us on the report.

## Supported versions

Until v1.0.0, security fixes go into the latest minor release only
(v0.5.x when v0.5 is the latest): upgrade to get them. The
[upgrade guides](docs/site/upgrade) list each release's breaking
changes.

| Version | Security fixes |
|---|---|
| Latest v0.x minor | Yes |
| Older v0.x | No |

## Scope

In scope: every module of this repository (the core
`anetos.dev/anetos`, `cli`, `admin`, `drivers/*`, `plugins/*`) and the
code the generators write (`anetos new`, `make:auth`, `make:crud`,
`make:admin`): a flaw in generated code is fixed in the generator, and
the upgrade guide says how to fix apps that already have the code.

Not in scope:

- your app's own code, and apps made with Anetos (report those to their
  owners);
- vulnerabilities in a dependency that Anetos doesn't make reachable:
  report those upstream (tell us if Anetos uses the dependency in a
  vulnerable way);
- the `examples/` directory's demo settings (its code is in scope);
- findings that need settings the documentation warns against
  (`APP_DEBUG=true` in production is refused, for example).

## How we keep Anetos secure

- `govulncheck` runs in CI on the minimum and the latest Go release;
  dependencies are updated weekly (Dependabot) and reviewed.
- The CI's actions are pinned by commit.
- `anetos doctor` and the app's `doctor` command check settings for
  unsafe values; [Secure your app](docs/site/guides/security.md) is the
  checklist for apps.
- The framework's own security review, with the risks we accept and why,
  is in [docs/security/checklist.md](docs/security/checklist.md).
