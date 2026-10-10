// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
)

// Config is the log's settings.
type Config struct {
	// IP is whether entries keep the client's IP address: none (the
	// default), masked (IPv4 to its /24, IPv6 to its /48) or full. An IP
	// address is personal data under the GDPR, so keeping it is the app's
	// decision. AUDIT_IP.
	IP string `env:"AUDIT_IP" default:"none"`
	// BulkMaxValues is how many rows' values a bulk entry keeps (every
	// row's key is kept). AUDIT_BULK_MAX_VALUES, default 10000.
	BulkMaxValues int `env:"AUDIT_BULK_MAX_VALUES" default:"10000"`
	// RetentionDays is how long entries are kept by [Prune] and
	// audit:prune; 0 (the default) keeps them forever.
	// AUDIT_RETENTION_DAYS.
	RetentionDays int `env:"AUDIT_RETENTION_DAYS" default:"0"`
}

// Validate checks the settings.
func (c Config) Validate() error {
	var errs []error
	if !slices.Contains([]string{"none", "masked", "full"}, c.IP) {
		errs = append(errs, fmt.Errorf("audit: AUDIT_IP is %q: use none, masked or full", c.IP))
	}
	if c.BulkMaxValues < 0 {
		errs = append(errs, errors.New("audit: AUDIT_BULK_MAX_VALUES can't be negative"))
	}
	if c.RetentionDays < 0 {
		errs = append(errs, errors.New("audit: AUDIT_RETENTION_DAYS can't be negative"))
	}
	return errors.Join(errs...)
}

// Trail is an app's audit log: its settings and tracked models. Create it
// with [New] and track models with [Track].
type Trail struct {
	cfg Config
	db  *db.DB
	log *slog.Logger
}

// trackMu makes checking that a table isn't tracked and tracking it one
// step: a table tracked twice would get every entry twice.
var trackMu sync.Mutex

type trailKey struct{}

// New creates the app's audit log, after db.Connect: it reads the
// AUDIT_* settings, notes the operation each write happens in, carries
// the actor into the queue jobs and async event listeners it starts
// (anetos.App.AddCarrier), and adds the audit:prune and audit:anonymize
// commands. Nothing is logged until models are tracked with [Track]; the
// tables come from [Migrations].
func New(app *anetos.App) (*Trail, error) {
	if _, ok := anetos.Lookup[*Trail](app); ok {
		return nil, errors.New("audit: New called twice for one app")
	}
	cfg, err := config.Get[Config](app.Source())
	if err != nil {
		return nil, err
	}
	d, ok := anetos.Lookup[*db.DB](app)
	if !ok {
		return nil, errors.New("audit: New needs the app's database: call db.Connect first")
	}
	t := &Trail{cfg: cfg, db: d, log: app.Logger().With("component", "audit")}
	app.AroundOperations(func(ctx context.Context, u anetos.Operation) (context.Context, func()) {
		return context.WithValue(ctx, operationKey{}, u), nil
	})
	app.AddCarrier(anetos.Carrier{
		Name: "audit.actor",
		Capture: func(ctx context.Context) string {
			a, _, err := actorOf(ctx) // the impersonator, if any: the person
			switch {
			case err != nil:
				// Who dispatched isn't known: the job's tracked writes
				// fail rather than be attributed to someone else.
				t.log.ErrorContext(ctx, "audit: who dispatched this work isn't known", "error", err)
				return unknownActor
			case a == System:
				return ""
			}
			return a.String()
		},
		Restore: func(ctx context.Context, v string) context.Context {
			if a, ok := parseActor(v); ok {
				return context.WithValue(ctx, carriedKey{}, a)
			}
			return context.WithValue(ctx, carriedKey{}, errUnknownActor)
		},
	})
	if err := addCommands(app); err != nil {
		return nil, err
	}
	anetos.Provide(app, t)
	app.AddContextValue(trailKey{}, t)
	return t, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp(app *anetos.App) (*Trail, error) {
	return New(app)
}

// Config returns the log's settings.
func (t *Trail) Config() Config { return t.cfg }

// Tracked reports whether the app's audit log (in ctx) tracks table
// ([Track]).
func Tracked(ctx context.Context, table string) bool {
	t, err := trailFrom(ctx)
	if err != nil {
		return false
	}
	for _, w := range t.db.Watchers(table) {
		if _, ok := w.(*tracking); ok {
			return true
		}
	}
	return false
}

// Enabled reports whether ctx has the app's audit log (audit.New), for
// code that records events only in apps that keep one.
func Enabled(ctx context.Context) bool {
	_, ok := ctx.Value(trailKey{}).(*Trail)
	return ok
}

func trailFrom(ctx context.Context) (*Trail, error) {
	if t, ok := ctx.Value(trailKey{}).(*Trail); ok {
		return t, nil
	}
	return nil, errors.New("audit: no audit log in the context: call audit.New at setup")
}

// TrackOption changes how a model is tracked ([Track]).
type TrackOption func(*tracking)

// Option is [TrackOption].
//
// Deprecated: Use TrackOption; Option is removed in v0.6.
//
//go:fix inline
type Option = TrackOption

// Except leaves columns out of the log entirely: their changes aren't
// recorded, and an update changing only them isn't logged.
func Except(columns ...string) TrackOption {
	return func(t *tracking) { t.except = append(t.except, columns...) }
}

// Redact records that columns changed, but not their values ([Redacted]).
// Columns whose names contain "password", "secret" or "token" are
// redacted unless [Reveal] says otherwise.
func Redact(columns ...string) TrackOption {
	return func(t *tracking) { t.redact = append(t.redact, columns...) }
}

// Reveal records the values of columns that would be redacted because of
// their names ("token_count").
func Reveal(columns ...string) TrackOption {
	return func(t *tracking) { t.reveal = append(t.reveal, columns...) }
}

// tracking is how one model is tracked; it is the table's db.Watcher.
type tracking struct {
	trail                  *Trail
	table                  string
	except, redact, reveal []string
	hidden, masked         map[string]bool
}

// secretName matches column names redacted by default.
var secretName = regexp.MustCompile(`(?i)password|passwd|secret|token|credential|api_?key|private_?key|session_?key|recovery_code|(^|_)otp(_|$)`)

// Track logs the writes to model T's table, from now on: creates,
// updates (the columns that changed, from what to what), soft deletes,
// restores and permanent deletes, one entry per row, and bulk writes (a
// query's Update or Delete, CreateMany, Upsert), one bulk entry each with
// every affected row's key. Entries are written in the write's
// transaction, so a committed change always has its entry: see db.Watch
// for what that costs and what isn't seen (raw SQL, pivot writes,
// cascades in the database).
//
//	err := audit.Track[models.Post](trail, audit.Except("view_count"), audit.Redact("notes"))
func Track[T any](t *Trail, opts ...TrackOption) error {
	table, err := db.TableOf[T]()
	if err != nil {
		return err
	}
	if strings.HasPrefix(table, "audit_") {
		return fmt.Errorf("audit: can't track %s, one of the log's own tables", table)
	}
	if _, _, err := db.KeyOf(new(T)); err != nil {
		return fmt.Errorf("audit: Track: %w", err)
	}
	cols, err := db.Columns[T]()
	if err != nil {
		return err
	}
	tr := &tracking{trail: t, table: table, hidden: map[string]bool{}, masked: map[string]bool{}}
	for _, opt := range opts {
		opt(tr)
	}
	for _, c := range slices.Concat(tr.except, tr.redact, tr.reveal) {
		if !slices.Contains(cols, c) {
			return fmt.Errorf("audit: Track: %s has no column %q", table, c)
		}
	}
	for _, c := range tr.except {
		tr.hidden[c] = true
	}
	for _, c := range cols {
		if slices.Contains(tr.redact, c) || secretName.MatchString(c) && !slices.Contains(tr.reveal, c) {
			tr.masked[c] = true
		}
	}
	trackMu.Lock()
	defer trackMu.Unlock()
	for _, w := range t.db.Watchers(table) {
		if _, ok := w.(*tracking); ok {
			return fmt.Errorf("audit: %s is tracked already", table)
		}
	}
	return t.db.Watch(table, tr, db.WatchBulkValues(t.cfg.BulkMaxValues))
}

// Written records a write of the tracked table (db.Watcher).
func (tr *tracking) Written(ctx context.Context, w *db.Write) error {
	c, err := tr.trail.contextOf(ctx)
	if err != nil {
		return err
	}
	action := actionOf(w.Op)
	if w.Bulk != nil {
		return tr.writeBulk(ctx, c, action, w.Bulk)
	}
	e := Entry{Action: action, SubjectType: tr.table, SubjectID: keyText(w.Key)}
	switch w.Op {
	case db.OpCreate:
		e.Changes.New = tr.values(w.After)
	case db.OpUpdate:
		e.Changes = tr.diff(w.Before, w.After)
		if len(e.Changes.New) == 0 {
			return nil // nothing the log tracks changed
		}
	case db.OpForceDelete:
		e.Changes.Old = tr.values(w.Before)
	}
	c.fill(&e.OccurredAt, &e.ActorType, &e.ActorID, &e.ActingAs, &e.ViaKind, &e.ViaName, &e.RequestID, &e.IP)
	if err := checkLengths(e.SubjectID, e.ActorID); err != nil {
		return err
	}
	return db.Create(db.AllowRepeatedQueries(ctx), &e) // one insert per change is expected, not an N+1
}

func (tr *tracking) writeBulk(ctx context.Context, c who, action string, b *db.Bulk) error {
	if action == Updated && len(b.Set) > 0 {
		tracked := false
		for col := range b.Set {
			tracked = tracked || !tr.hidden[col]
		}
		if !tracked {
			return nil // it set only columns the log leaves out
		}
	}
	op := BulkOp{
		Action:      action,
		SubjectType: tr.table,
		RowCount:    int64(len(b.Keys)),
		Condition:   Condition{SQL: b.Where, Args: tr.conditionArgs(b.Where, b.Args)},
		Complete:    b.Complete,
	}
	if len(b.Set) > 0 {
		op.Assignments = tr.values(b.Set)
	}
	rows := func(in []db.RowValues) []Row {
		if len(in) == 0 {
			return nil
		}
		out := make([]Row, len(in))
		for i, r := range in {
			out[i] = Row{keyText(r.Key), tr.values(r.Values)}
		}
		return out
	}
	op.Before, op.After = rows(b.Before), rows(b.After)
	c.fill(&op.OccurredAt, &op.ActorType, &op.ActorID, &op.ActingAs, &op.ViaKind, &op.ViaName, &op.RequestID, &op.IP)
	if err := checkLengths("", op.ActorID); err != nil {
		return err
	}
	ctx = db.AllowRepeatedQueries(ctx)
	if err := db.Create(ctx, &op); err != nil {
		return err
	}
	items := make([]BulkItem, len(b.Keys))
	for i, k := range b.Keys {
		items[i] = BulkItem{BulkID: op.ID, SubjectType: tr.table, SubjectID: keyText(k)}
		if err := checkLengths(items[i].SubjectID, ""); err != nil {
			return err
		}
	}
	return db.CreateMany(ctx, items)
}

// conditionArgs returns a bulk write's condition arguments, all redacted
// if the condition names a column the log hides or redacts: its value
// would show there.
func (tr *tracking) conditionArgs(where string, args []any) []any {
	for col := range tr.hidden {
		if mentions(where, col) {
			return redactAll(args)
		}
	}
	for col := range tr.masked {
		if mentions(where, col) {
			return redactAll(args)
		}
	}
	return args
}

// mentions reports whether SQL text names col as a word.
func mentions(sql, col string) bool {
	for i := 0; ; {
		j := strings.Index(sql[i:], col)
		if j < 0 {
			return false
		}
		j += i
		before := j == 0 || !isIdent(sql[j-1])
		after := j+len(col) == len(sql) || !isIdent(sql[j+len(col)])
		if before && after {
			return true
		}
		i = j + 1
	}
}

func isIdent(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func redactAll(args []any) []any {
	out := make([]any, len(args))
	for i := range out {
		out[i] = Redacted
	}
	return out
}

// checkLengths reports keys and actor IDs too long for the log's columns,
// before the database does (on PostgreSQL and MySQL it would fail the
// insert less clearly; SQLite would store them).
func checkLengths(subjectID, actorID string) error {
	if len(subjectID) > 255 {
		return fmt.Errorf("audit: a key of %d bytes is longer than the log keeps (255)", len(subjectID))
	}
	if len(actorID) > 100 {
		return fmt.Errorf("audit: an actor ID of %d bytes is longer than the log keeps (100)", len(actorID))
	}
	return nil
}

// values returns vals without the hidden columns, redacted ones masked.
func (tr *tracking) values(vals map[string]any) map[string]any {
	out := make(map[string]any, len(vals))
	for k, v := range vals {
		if tr.hidden[k] {
			continue
		}
		if tr.masked[k] && v != nil {
			v = Redacted
		}
		out[k] = v
	}
	return out
}

// diff returns the tracked columns whose values differ, timestamps aside.
func (tr *tracking) diff(before, after db.Values) Changes {
	c := Changes{Old: map[string]any{}, New: map[string]any{}}
	for k, nv := range after {
		if tr.hidden[k] || k == "updated_at" || k == "created_at" {
			continue
		}
		ov := before[k]
		if same(ov, nv) {
			continue
		}
		if tr.masked[k] {
			ov, nv = maskValue(ov), maskValue(nv)
		}
		c.Old[k], c.New[k] = ov, nv
	}
	return c
}

func maskValue(v any) any {
	if v == nil {
		return nil
	}
	return Redacted
}

// same reports whether two of the db package's plain values are equal.
func same(a, b any) bool {
	switch x := a.(type) {
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	case []byte:
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	case json.RawMessage:
		y, ok := b.(json.RawMessage)
		if !ok {
			return false
		}
		var cx, cy bytes.Buffer
		if json.Compact(&cx, x) != nil || json.Compact(&cy, y) != nil {
			return bytes.Equal(x, y)
		}
		return bytes.Equal(cx.Bytes(), cy.Bytes())
	}
	return reflect.DeepEqual(a, b)
}

func actionOf(op db.Op) string {
	switch op {
	case db.OpCreate:
		return Created
	case db.OpUpdate:
		return Updated
	case db.OpDelete:
		return Deleted
	case db.OpRestore:
		return Restored
	case db.OpForceDelete:
		return ForceDeleted
	case db.OpUpsert:
		return Upserted
	}
	return string(op)
}

// keyText is a key as stored: fmt.Sprint, hex for bytes.
func keyText(k any) string {
	if b, ok := k.([]byte); ok {
		return hex.EncodeToString(b)
	}
	return fmt.Sprint(k)
}

// ---- who, how, from where ----

type (
	operationKey struct{}
	actorKey     struct{}
	carriedKey   struct{}
)

// WithActor returns ctx in which entries are attributed to a, rather than
// to the logged-in user or the system: a webhook's handler, say.
//
//	ctx = audit.WithActor(ctx, audit.Actor{Type: "service", ID: "stripe"})
//
// It panics if a is invalid (its type isn't lowercase letters, digits, _
// and -, up to 50 characters, or its ID is longer than 100 bytes), which
// is a programming error.
func WithActor(ctx context.Context, a Actor) context.Context {
	if err := a.check(); err != nil {
		panic(err.Error())
	}
	return context.WithValue(ctx, actorKey{}, a)
}

var kindRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,49}$`)

// unknownActor is carried when the dispatcher's actor couldn't be found.
const unknownActor = "!unknown"

var errUnknownActor = errors.New("audit: who started this work isn't known: loading the user failed where it was dispatched")

// ActorOf returns who entries made with ctx are attributed to: the actor
// set with [WithActor]; the logged-in user, or the one a job acts as
// (auth.CurrentID), or the user impersonating them (auth.Impersonator); in a
// queue job or async event listener, the actor of the work that started
// it; otherwise [System]. An error loading the user is returned: the log
// doesn't guess.
func ActorOf(ctx context.Context) (Actor, error) {
	a, _, err := actorOf(ctx)
	return a, err
}

// actorOf returns the actor, and the user they impersonate ("" if none:
// auth.Impersonate).
func actorOf(ctx context.Context) (Actor, string, error) {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok {
		return a, "", nil
	}
	id, err := auth.CurrentID(ctx)
	switch {
	case err == nil:
		if by, ok := auth.Impersonator(ctx); ok {
			return User(by), User(id).String(), nil
		}
		return User(id), "", nil
	case !errors.Is(err, auth.ErrUnauthenticated):
		return Actor{}, "", fmt.Errorf("audit: who is acting: %w", err)
	}
	switch a := ctx.Value(carriedKey{}).(type) {
	case Actor:
		return a, "", nil
	case error:
		return Actor{}, "", a
	}
	return System, "", nil
}

// who is what an entry records about who did it and how.
type who struct {
	at        time.Time
	actor     Actor
	actingAs  string
	viaKind   string
	viaName   string
	requestID string
	ip        string
}

func (t *Trail) contextOf(ctx context.Context) (who, error) {
	a, as, err := actorOf(ctx)
	if err != nil {
		return who{}, err
	}
	w := who{at: anetos.Now(ctx).UTC().Truncate(time.Microsecond), actor: a, actingAs: as, requestID: limit(web.RequestID(ctx), 100)}
	if u, ok := ctx.Value(operationKey{}).(anetos.Operation); ok {
		w.viaKind, w.viaName = u.Kind, u.Name
	} else if c, ok := cmd.Running(ctx); ok {
		w.viaKind, w.viaName = "command", c.Name
	}
	w.viaName = limit(w.viaName, 255)
	w.ip = maskIP(web.ClientIP(ctx), t.cfg.IP)
	return w, nil
}

func (w who) fill(at *time.Time, actorType, actorID, actingAs, viaKind, viaName, requestID, ip *string) {
	*at, *actorType, *actorID, *actingAs = w.at, w.actor.Type, w.actor.ID, w.actingAs
	*viaKind, *viaName, *requestID, *ip = w.viaKind, w.viaName, w.requestID, w.ip
}

// limit cuts s to at most n bytes, at a character boundary.
func limit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// maskIP keeps ip as mode says: none, masked (IPv4 /24, IPv6 /48) or
// full.
func maskIP(ip, mode string) string {
	if ip == "" || mode == "none" {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.Unmap().WithZone("")
	if mode == "full" {
		return addr.String()
	}
	bits := 24
	if addr.Is6() {
		bits = 48
	}
	p, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.Addr().String()
}
