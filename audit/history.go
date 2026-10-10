// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
)

// SubjectOf returns the subject of a row of a model: its table and key.
func SubjectOf(row any) (Subject, error) {
	table, key, err := db.KeyOf(row)
	if err != nil {
		return Subject{}, err
	}
	return Subject{table, keyText(key)}, nil
}

// Record adds an entry for something the app did that isn't a write of a
// tracked model: an export, a login, a permission check that failed.
// The actor and the operation are found as for tracked writes;
// properties are the details, stored as JSON. With a transaction in ctx,
// the entry is part of it.
//
//	subject, err := audit.SubjectOf(&invoice)
//	err = audit.Record(ctx, "invoice.exported", subject, map[string]any{"format": "pdf"})
//
// action is up to 100 bytes; subject may be empty.
func Record(ctx context.Context, action string, subject Subject, properties map[string]any) error {
	t, err := trailFrom(ctx)
	if err != nil {
		return err
	}
	if action == "" || len(action) > 100 || len(subject.Type) > 100 || len(subject.ID) > 255 {
		return errors.New("audit: Record needs an action of up to 100 bytes, a subject type of up to 100 and a subject ID of up to 255")
	}
	c, err := t.contextOf(ctx)
	if err != nil {
		return err
	}
	e := Entry{Action: action, SubjectType: subject.Type, SubjectID: subject.ID, Properties: properties}
	c.fill(&e.OccurredAt, &e.ActorType, &e.ActorID, &e.ActingAs, &e.ViaKind, &e.ViaName, &e.RequestID, &e.IP)
	if err := checkLengths(e.SubjectID, e.ActorID); err != nil {
		return err
	}
	return db.Create(db.AllowRepeatedQueries(ctx), &e)
}

// Event is one item of a subject's history: an entry of its own, or a
// bulk write that touched it. Exactly one of the two is set.
type Event struct {
	// Entry is an entry about the subject alone.
	Entry *Entry `json:"entry,omitempty"`
	// Bulk is a bulk write the subject was among the rows of.
	Bulk *BulkOp `json:"bulk,omitempty"`
}

// At returns when it happened.
func (e Event) At() time.Time {
	if e.Entry != nil {
		return e.Entry.OccurredAt
	}
	return e.Bulk.OccurredAt
}

// Actor returns who did it.
func (e Event) Actor() Actor {
	if e.Entry != nil {
		return e.Entry.Actor()
	}
	return e.Bulk.Actor()
}

// Action returns what was done.
func (e Event) Action() string {
	if e.Entry != nil {
		return e.Entry.Action
	}
	return e.Bulk.Action
}

// History returns up to limit events of subject (a row: see
// [SubjectOf]), its own entries and the bulk writes that touched it,
// newest first, and a cursor for the next (older) page, "" after the
// last. Pass "" for the first page:
//
//	events, next, err := audit.History(ctx, subject, 50, "")
//	older, next, err := audit.History(ctx, subject, 50, next)
//
// A cursor is opaque text, safe in a URL; an invalid one is an error.
func History(ctx context.Context, subject Subject, limit int, cursor string) ([]Event, string, error) {
	if limit <= 0 {
		return nil, "", errors.New("audit: History needs a positive limit")
	}
	entries := db.Query[Entry](ctx).
		Where(db.C("subject_type").Eq(subject.Type), db.C("subject_id").Eq(subject.ID))
	bulks := db.Query[BulkOp](ctx).
		WhereRaw("subject_type = ? AND id IN (SELECT bulk_id FROM audit_bulk_items WHERE subject_type = ? AND subject_id = ?)", subject.Type, subject.Type, subject.ID)
	if cursor != "" {
		c, err := parseCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		// Events sort by (time, entries before bulk entries, ID), newest
		// first; the next page has what sorts after the cursor's event.
		at := db.C("occurred_at")
		if c.bulk {
			entries = entries.Where(at.Lt(c.at))
			bulks = bulks.Where(db.Or(at.Lt(c.at), db.And(at.Eq(c.at), db.C("id").Lt(c.id))))
		} else {
			entries = entries.Where(db.Or(at.Lt(c.at), db.And(at.Eq(c.at), db.C("id").Lt(c.id))))
			bulks = bulks.Where(db.C("occurred_at").Lte(c.at))
		}
	}
	// One more than a page: whether there is a next page.
	es, err := entries.OrderBy(db.C("occurred_at").Desc(), db.C("id").Desc()).Limit(limit + 1).Get()
	if err != nil {
		return nil, "", err
	}
	bs, err := bulks.OrderBy(db.C("occurred_at").Desc(), db.C("id").Desc()).Limit(limit + 1).Get()
	if err != nil {
		return nil, "", err
	}
	out := make([]Event, 0, len(es)+len(bs))
	for i := range es {
		out = append(out, Event{Entry: &es[i]})
	}
	for i := range bs {
		out = append(out, Event{Bulk: &bs[i]})
	}
	slices.SortFunc(out, func(a, b Event) int {
		if c := b.At().Compare(a.At()); c != 0 {
			return c
		}
		if (a.Entry != nil) != (b.Entry != nil) {
			if a.Entry != nil {
				return -1 // entries first
			}
			return 1
		}
		return cmp.Compare(b.id(), a.id())
	})
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = historyCursor{at: last.At(), bulk: last.Bulk != nil, id: last.id()}.String()
	}
	return out, next, nil
}

func (e Event) id() int64 {
	if e.Entry != nil {
		return e.Entry.ID
	}
	return e.Bulk.ID
}

// historyCursor is where a page of History ended.
type historyCursor struct {
	at   time.Time
	bulk bool
	id   int64
}

func (c historyCursor) String() string {
	kind := "e"
	if c.bulk {
		kind = "b"
	}
	return fmt.Sprintf("%d.%s.%d", c.at.UnixMicro(), kind, c.id)
}

func parseCursor(s string) (historyCursor, error) {
	parts := strings.Split(s, ".")
	if len(parts) == 3 && (parts[1] == "e" || parts[1] == "b") {
		us, err1 := strconv.ParseInt(parts[0], 10, 64)
		id, err2 := strconv.ParseInt(parts[2], 10, 64)
		if err1 == nil && err2 == nil {
			return historyCursor{at: time.UnixMicro(us).UTC(), bulk: parts[1] == "b", id: id}, nil
		}
	}
	return historyCursor{}, fmt.Errorf("audit: invalid history cursor %q", s)
}

// pruneBatch is how many entries one statement of Prune deletes.
const pruneBatch = 1000

// Pruned is what [Prune] deleted.
type Pruned struct {
	// Entries is how many entries of audit_log.
	Entries int64
	// Bulk is how many bulk entries, with their items.
	Bulk int64
	// Before is the cutoff: entries older than this were deleted.
	Before time.Time
}

// Prune deletes the entries older than AUDIT_RETENTION_DAYS, in batches,
// and records that it did ("audit.pruned"). With a retention of 0 (the
// default) it does nothing (Before is zero). Run it from a scheduled task,
// or with the audit:prune command.
func Prune(ctx context.Context) (Pruned, error) {
	t, err := trailFrom(ctx)
	if err != nil {
		return Pruned{}, err
	}
	if t.cfg.RetentionDays == 0 {
		return Pruned{}, nil // kept forever
	}
	cutoff := anetos.Now(ctx).UTC().AddDate(0, 0, -t.cfg.RetentionDays)
	p := Pruned{Before: cutoff}
	old := db.C("occurred_at").Lt(cutoff)
	for {
		ids, err := db.Pluck(db.Query[Entry](ctx).Where(old).OrderBy(db.C("id").Asc()).Limit(pruneBatch), db.Col[int64]("id"))
		if err != nil {
			return p, err
		}
		if len(ids) == 0 {
			break
		}
		n, err := db.Query[Entry](ctx).WhereKeys(anyOf(ids)...).Delete()
		p.Entries += n
		if err != nil {
			return p, err
		}
	}
	for {
		ids, err := db.Pluck(db.Query[BulkOp](ctx).Where(old).OrderBy(db.C("id").Asc()).Limit(pruneBatch), db.Col[int64]("id"))
		if err != nil {
			return p, err
		}
		if len(ids) == 0 {
			break
		}
		err = db.Tx(ctx, func(ctx context.Context) error {
			if _, err := db.Query[BulkItem](ctx).Where(db.Col[int64]("bulk_id").In(ids...)).Delete(); err != nil {
				return err
			}
			n, err := db.Query[BulkOp](ctx).WhereKeys(anyOf(ids)...).Delete()
			p.Bulk += n
			return err
		})
		if err != nil {
			return p, err
		}
	}
	err = Record(ctx, "audit.pruned", Subject{}, map[string]any{
		"before": cutoff, "entries": p.Entries, "bulk": p.Bulk,
	})
	return p, err
}

// Anonymize replaces actor a with a placeholder ("erased") in every entry
// and bulk entry, as the actor and as the impersonated user (ActingAs), and
// drops the IP addresses of the entries a made, for a request to
// erase a person's data; it records that it did ("audit.anonymized",
// without the ID) and returns how many entries changed. Entries about the
// person's own rows (their changes) are left: delete those with
// db.Query[audit.Entry] if the request covers them.
func Anonymize(ctx context.Context, a Actor) (int64, error) {
	if a.ID == "" {
		return 0, errors.New("audit: Anonymize needs an actor with an ID")
	}
	if _, err := trailFrom(ctx); err != nil {
		return 0, err
	}
	var total int64
	err := db.Tx(ctx, func(ctx context.Context) error {
		is := []db.Expr{db.C("actor_type").Eq(a.Type), db.C("actor_id").Eq(a.ID)}
		set := []db.Assignment{db.Col[string]("actor_id").Set("erased"), db.Col[string]("ip").Set("")}
		n, err := db.Query[Entry](ctx).Where(is...).Update(set...)
		if err != nil {
			return err
		}
		m, err := db.Query[BulkOp](ctx).Where(is...).Update(set...)
		if err != nil {
			return err
		}
		// Entries of others impersonating the person.
		as := []db.Expr{db.C("acting_as").Eq(a.String())}
		erased := db.Col[string]("acting_as").Set(Actor{Type: a.Type, ID: "erased"}.String())
		k, err := db.Query[Entry](ctx).Where(as...).Update(erased)
		if err != nil {
			return err
		}
		l, err := db.Query[BulkOp](ctx).Where(as...).Update(erased)
		if err != nil {
			return err
		}
		total = n + m + k + l
		return Record(ctx, "audit.anonymized", Subject{}, map[string]any{"actor_type": a.Type, "entries": total})
	})
	return total, err
}

func anyOf[T any](xs []T) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}
