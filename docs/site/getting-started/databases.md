---
title: Choose a database
since: v0.3.0
group: "Installation"
weight: 12
---

# Choose a database

Anetos apps run on SQLite, PostgreSQL, MySQL and MariaDB. Pick one when
you create the project; you can switch later.

| Database | `anetos new` flag | Needs |
|---|---|---|
| SQLite | none (the default) | Nothing: the database is a file, `database/app.db` |
| PostgreSQL | `--db=postgres` | A server, a database and a user |
| MySQL or MariaDB | `--db=mysql` | A server, a database and a user |

Anetos is tested on SQLite, PostgreSQL 16 and 17, MySQL 8.0, and
MariaDB 10.11 and 11.8.

## SQLite: start here

SQLite needs no server, so a new project runs at once. It suits
development, tests (they use an in-memory database), and many apps in
production: one server, with the database file on its disk. Its
driver is pure Go: no C compiler, and the app stays one static binary.

## PostgreSQL or MySQL

Choose a server database for apps that run on several servers, or when
your host provides one. Install it (your system's packages, Docker, or
a hosted service), then create a database and a user for the app, and
another database for the tests:

```sh
# PostgreSQL
createdb blog
createdb blog_test
```

```sql
-- MySQL or MariaDB
CREATE DATABASE blog CHARACTER SET utf8mb4;
CREATE DATABASE blog_test CHARACTER SET utf8mb4;
CREATE USER 'blog'@'localhost' IDENTIFIED BY 'secret';
GRANT ALL ON blog.* TO 'blog'@'localhost';
GRANT ALL ON blog_test.* TO 'blog'@'localhost';
```

On MySQL and MariaDB, `utf8mb4` is the character set that stores any
text, emoji included. MySQL 8 and MariaDB 11.6+ use it by default, but
an older MariaDB may default to latin1, which refuses most non-Latin
text. The tables the schema builder creates (`s.Create` in a
migration) are utf8mb4 either way; the database's own setting applies
to tables made with SQL (SQL-file migrations, `s.Exec`), and `doctor`
warns about text columns in another character set.

`anetos new blog --db=postgres` then writes the `DB_*` settings in
`.env` (`DB_NAME=blog`) and `.env.testing` (`blog_test`): set the
user and password there.

Some features use what each database has: full-text search works on all
of them, ranked differently; search by meaning needs PostgreSQL with
pgvector or MariaDB 11.7+ (SQLite compares every row, for small data;
MySQL can't). Each guide says what it needs, and the app
tells you at startup when the database lacks it.

To switch an existing project, see [Connect to a
database](../guides/database.md#5-switch-a-project-to-postgresql-or-mysql).

Next: [Create a project](create-a-project.md).
