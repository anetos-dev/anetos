# Examples

Compiled example programs used by the documentation. CI builds and vets
everything here, so code shown in the docs can't silently break. Doc pages
copy tagged regions (`// region: name` … `// endregion`) from these files.
See the [documentation guide](../docs/contributing/documentation-guide.md) §7.

Most examples show one feature. [`tracker`](tracker) is the reference
app: an issue tracker with projects, members and roles, issues with
labels, comments, files and their history, search, emails from queue
jobs, a weekday digest, an admin and a JSON API, tested on every
database. [`tutorial`](tutorial) is the app the
[tutorial](../docs/site/getting-started/tutorial/README.md) builds. [`saas`](saas) is a whole app, made with
`anetos new` and `anetos make:auth`: accounts with a password, Google or
GitHub, a welcome email from a queue job, a pub/sub listener and a
scheduled task, in one binary that also runs split by role
(`run --only=…`), with a test that runs it that way. [`teams`](teams) is
a JSON API where users have roles in teams and across them
(`auth/rbac`). [`assistant`](assistant) is a help center with an AI
assistant: stored conversations, answers streamed with htmx, replies
from queue jobs and daily budgets. [`i18n`](i18n) speaks English and
Bangla: catalogs, plurals, dates, prices and relative times, a language
switcher, and validation messages in the visitor's language.
[`admin`](admin) is a shop's back office made with the admin interface:
products and categories, roles for administrators and editors, and every
change in the audit log.
