// SPDX-License-Identifier: Apache-2.0

// Package audit keeps an audit log: who created, changed, deleted,
// restored and permanently deleted the rows of the models an app tracks,
// field by field, and the events the app records itself.
//
// Set it up after the database, and track models:
//
//	trail, err := audit.New(app) // after db.Connect
//	if err != nil {
//		return err
//	}
//	if err := audit.Track[models.Post](trail, audit.Redact("private_notes")); err != nil {
//		return err
//	}
//
// and add [Migrations] to the app's migrations. From then on every
// db.Create, db.Update, db.Delete… of a Post writes an [Entry], and every
// write to many posts at once (a query's Update or Delete, CreateMany,
// Upsert) a [BulkOp], with the key of every row it touched in
// [BulkItem]s, so "who deleted post 42?" has an answer either way.
//
// Entries are written in the transaction of the change, so a committed
// change always has its entry and a rolled-back one never does. The
// price is a transaction, a read and an insert per write of a tracked
// model; untracked models pay nothing. Raw SQL (db.Exec), pivot writes
// and the database's own cascades aren't seen (see db.DB.Watch).
//
// An entry records:
//
//   - the actor ([ActorOf]): the one set with [WithActor], else the
//     logged-in user (or the one a job acts as), else, in a queue job or
//     async event listener, the actor of the work that started it, else
//     [System];
//   - the action ([Created], [Updated], [Deleted], [Restored],
//     [ForceDeleted], [Upserted], or the app's own) and the subject (the
//     table and the row's key);
//   - the [Changes]: new values for a create, old and new values of the
//     changed columns for an update, old values for a permanent delete;
//     columns named like password, secret or token are [Redacted];
//   - the operation (request, job, listener, task, command), the
//     request ID, and the client's IP address if AUDIT_IP asks for it.
//
// [History] returns a row's events, its own and the bulk writes that
// touched it. [Record] adds the app's own events. [Prune] (audit:prune)
// applies AUDIT_RETENTION_DAYS; [Anonymize] (audit:anonymize) replaces
// an actor with a placeholder for erasure requests.
package audit
