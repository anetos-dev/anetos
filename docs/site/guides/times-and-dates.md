---
title: Times and dates
since: v0.3.0
group: "Languages and time"
weight: 602
---

# Times and dates

Store times without thinking about time zones, keep calendar dates on
their day, and choose the zone your app works in.

## Before you start

- A database set up with `db.Connect` ([database guide](database.md)).
- Nothing to configure: times are stored in UTC by default, and the app
  won't start against a database session in another zone.

## Steps

### 1. Store times as they are

Write `time.Time` values from any zone. The framework converts every
time it sends to the database to UTC (the query builder, `db.Create`,
raw SQL), opens database sessions in UTC and reads times back in UTC:

```go
// illustrative
order.PaidAt = anetos.Now(ctx) // in the app's zone (APP_TIMEZONE)
err := db.Save(ctx, &order)    // stored in UTC
```

A time in Dhaka and the same instant in UTC are stored alike, and
`created_at`/`updated_at` are set in UTC. There is no setting to forget at
the start of a project.

### 2. Keep dates in an `anetos.Date`

A birthday or a due date is a day, not an instant. In a `time.Time` it is
a midnight somewhere, and midnight in Dhaka is 18:00 the day before in
UTC, so it would be stored a day early. `anetos.Date` holds a year, month
and day with no zone:

```go
// illustrative
type Task struct {
	db.Model
	Title string      `db:"title"`
	Due   anetos.Date `db:"due"` // a DATE column: t.Date("due")
}

due := anetos.NewDate(2026, time.October, 3)
tasks, err := db.Query[Task](ctx).Where(TaskCols.Due.Lte(anetos.Today(ctx))).Get()
```

- Create the column with `t.Date("due")` in a migration. It is written as
  `2026-10-03` and read back on every database as the same day.
- `anetos.Today(ctx)` is today's date in the app's zone, on the app's
  clock. `anetos.DateOf(t)` is the date of a time in that time's own zone:
  convert first to see it elsewhere (`anetos.DateOf(t.In(loc))`).
- The zero `Date` is "no date": it is written as NULL and shown as an
  empty string. Use `*anetos.Date` where you need to tell them apart. A
  date that isn't on the calendar (February 30) is an error when written.
- Switching an existing `time.Time` field of a `DATE` column to
  `anetos.Date`? On SQLite the old rows hold `2026-03-15 00:00:00` and new
  ones `2026-03-15`, which compare as text: convert the old rows once,
  `UPDATE tasks SET due = date(due) WHERE due IS NOT NULL`. PostgreSQL and
  MySQL need nothing.

### 3. Take dates from forms and JSON

A `Date` field binds from `<input type="date">` (which sends
`2026-10-03`), from query strings and from JSON strings. The date rules
compare dates; `now` is today in the app's zone:

```go
// illustrative
type BookInput struct {
	CheckIn  anetos.Date `form:"check_in" validate:"required|after_or_equal:now"`
	CheckOut anetos.Date `form:"check_out" validate:"required|after:CheckIn"`
}
```

A value that isn't a date (`03/10/2026`) is rejected before validation,
as for other typed fields.

### 4. Choose the app's time zone

`APP_TIMEZONE` (an IANA name, default `UTC`) is the zone your app works
in:

```sh
APP_TIMEZONE=Asia/Dhaka
```

- It is the process's local zone, so `time.Now()`, log lines and
  formatted times agree on your laptop and on the server.
- `anetos.Now(ctx)` returns times in it and `anetos.Location(ctx)` returns
  it.
- Schedules use it unless `SCHEDULE_TIMEZONE` says otherwise
  ([scheduling](scheduling.md)).

Storage doesn't change: times are UTC in the database whatever the app's
zone. Keep `UTC` unless your app serves one region and you want its
times in logs and schedules in local time. To show a time to a user in
their own zone and language, format it with `i18n.Date`, `i18n.Time` or
`i18n.DateTime` ([Numbers, dates and languages](formatting.md)); to
convert it yourself, `t.In(i18n.TimeZone(ctx))`.

The zone database is built into the framework, so zone names work in
containers without one (`FROM scratch`, distroless).

## How it works

The rule is UTC in storage and the user's zone on display; the framework
enforces the first half. When the app boots, `db.Connect` checks the
database session's time zone: if `DB_URL` sets one other than UTC
(`timezone=` for PostgreSQL, `time_zone=` for MySQL), the app stops,
because `CURRENT_TIMESTAMP` and `NOW()` would then write local times next
to the app's UTC ones. See [the data layer](../concepts/data-layer.md#times-are-utc).

## Testing it

`app.Freeze(at)` stops the app's clock at `at`; `anetos.Now` and
`anetos.Today` follow it, in the app's zone:

```go
// illustrative
app := anetostest.New(t, setup, anetostest.Env(map[string]string{"APP_TIMEZONE": "Asia/Dhaka"}))
app.Freeze(time.Date(2026, 3, 15, 20, 0, 0, 0, time.UTC))
anetos.Today(app.Context()) // 2026-03-16: it's already the 16th in Dhaka
```

In a test binary the process's local zone comes from `APP_TIMEZONE` in
the environment (UTC when unset), not from `.env` files, so tests that
run in parallel never change it; each app's `anetos.Now` is still in its
own zone. Export `APP_TIMEZONE` in CI if your app sets it in a `.env`
file and tests format times with `time.Now()`.

## Troubleshooting

| Problem | Cause | Fix |
|---|---|---|
| `the database session's time zone is …, not UTC` at startup | `DB_URL` sets a session time zone | Remove `timezone=`/`time_zone=` from `DB_URL`; for a legacy database whose times are local, set `DB_ALLOW_LOCAL_TIMEZONE=true` |
| A date is a day early | It was stored as a `time.Time` at a local midnight | Use `anetos.Date` for dates |
| `APP_TIMEZONE "…" is not an IANA time zone` | A name like `BST` or `GMT+6` | Use a region name: `Asia/Dhaka`, `Europe/London` |
| Logs show another zone than `APP_TIMEZONE` | Two apps with different zones in one process | A process has one local zone, the first app's; set the same `APP_TIMEZONE` |
