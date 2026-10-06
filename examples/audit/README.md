# Audit: who changed what

A JSON API of documents whose every change is in an audit log, made with
package `audit`:

- **Tracked model** ([`models.go`](models.go)): every create, edit (the
  fields that changed, from what to what), soft delete, restore and
  permanent delete of a `Document`, by whom, in which request.
- **Left out and redacted**: the view counter isn't logged
  (`audit.Except`), so reading a document doesn't fill the log; the share
  token is recorded as changed but never shown, because its name says
  token.
- **Bulk writes**: `POST /api/documents/archive` archives the user's
  published documents in one statement, which is one bulk entry with
  every document's key, so each document's history still shows it.
- **Events of the app's own**: `GET /api/documents/export` records
  `documents.exported` with `audit.Record`.
- **History**: `GET /api/documents/{id}/history`, newest first.
- **Soft deletes**: a slug is unique among the documents that aren't
  deleted (`UniqueLive` and the `unique_live` rule), and documents deleted
  30 days ago go for good with `go run . db:prune-trashed`, which the log
  records too.

## Run it

```sh
anetos key:generate >> .env   # APP_KEY, once (the anetos developer tool)
export APP_ENV=development HTTP_ADDR=:8080
go run . migrate
go run . seed                 # a user and an API token
go run .
```

Then, with the token that `seed` printed:

```sh
T="Authorization: Bearer <token>"
curl -H "$T" -d '{"slug":"plan","title":"Plan"}' localhost:8080/api/documents
curl -H "$T" -X PATCH -d '{"title":"The plan","status":"published"}' localhost:8080/api/documents/1
curl -H "$T" -X POST localhost:8080/api/documents/archive
curl -H "$T" localhost:8080/api/documents/1/history
```

## Tests

`go test` runs the API against a fresh SQLite database with
`anetostest`: [`main_test.go`](main_test.go).

The guide: [Keep an audit log](../../docs/site/guides/audit-log.md).
