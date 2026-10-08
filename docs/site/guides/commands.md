---
title: Commands
since: v0.1.0
group: "Basics"
weight: 108
---

# Commands

Your application builds to one binary. Its first argument picks what it
does: run the app (the default), run only some roles, migrate the
database, list the routes, or run commands of your own.

## Before you start

The app calls `app.Execute()` at the end of `main`, as projects made by
`anetos new` do:

```go
// illustrative
func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	// connect the database, add the server and routes, …
	app.Execute()
}
```

## Steps

### 1. Run the app

```sh
./blog                      # the default: run every component
./blog run --only=http      # only the components with these roles
./blog serve                # the same as run --only=http
./blog help                 # every command
./blog help migrate         # one command's arguments
```

`run` starts the components and stops them gracefully on Ctrl-C or
`SIGTERM`. Components without roles run in every process (see
[the runtime supervisor](../concepts/runtime-supervisor.md)).

### 2. Use the built-in commands

| Command | Added by |
|---|---|
| `run [--only=role,…]` | Every app |
| `serve`, `routes:list` | `web.NewServer` |
| `openapi` | `openapi.ForApp` ([Describe an API with OpenAPI](openapi.md)) |
| `migrate`, `migrate:rollback`, `migrate:reset`, `migrate:fresh`, `migrate:status`, `db:seed`, `search:reindex` | `migrate.ForApp` ([Migrations](migrations.md), [Search](search.md)) |
| `cache:clear` | `cache.ForApp` ([Cache values](cache.md)) |
| `ai:embed` | `ai.EmbeddingsFor` ([Search by meaning](semantic-search.md)) |
| `queue:failed`, `queue:retry`, `queue:forget`, `queue:flush`, `queue:clear` | `queue.ForApp` ([Queues](queues.md)) |
| `pubsub:publish` | `pubsub.ForApp` ([Pub/sub listeners](pubsub.md)) |
| `schedule:list`, `schedule:run` | `schedule.ForApp` ([Scheduling](scheduling.md)) |

### 3. Add your own

```go
app.Command("blog:stats", "Print how many authors and posts there are", func(ctx context.Context, args *cmd.Args) error {
	authors, err := db.Query[Author](ctx).Count()
	if err != nil {
		return err
	}
	posts, err := db.Query[Post](ctx).Count()
	if err != nil {
		return err
	}
	fmt.Fprintf(args.Stdout, "%d authors, %d posts\n", authors, posts)
	return nil
})
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `custom-command`.)

A command runs with the app booted: its context carries what the app
provides (the database here), and the app is closed afterwards, so
shutdown hooks run. The context is canceled on Ctrl-C.

For flags, parse `args.Args` with a `flag.FlagSet` through `args.Parse`,
which reports bad flags as usage errors (the binary prints them with the
command's usage and exits with status 2):

```go
// illustrative
app.Command("reports:send", "Email the weekly report", func(ctx context.Context, args *cmd.Args) error {
	fs := flag.NewFlagSet("reports:send", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the report instead of sending it")
	if err := args.Parse(fs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return cmd.Usagef("unexpected argument %q", fs.Arg(0))
	}
	return reports.Send(ctx, *dryRun)
})
```

`app.AddCommand(cmd.Command{…})` takes a `Usage` line for help (list the
flags there) and returns an error instead of panicking. `-h` or `--help`
right after the command's name never reaches it: the binary prints its
usage and description. Later in the arguments, `args.Parse` handles it
and prints the flags.

## How it works

`app.Execute()` reads `os.Args`: no arguments (or only flags) means
`run`; `help`, `-h` and `--help` list the commands. It exits with status
0 on success, 1 when the command fails (also when a signal interrupts it
before it finishes), and 2 for usage errors (an unknown command, a bad
flag, a `cmd.Usagef` error). `run` and `serve` exit with 0 when a signal
stops the app, since that is how they end. A second Ctrl-C ends the
program at once. Commands that manage the app themselves (`run`,
`serve`: `ManagesApp` in `cmd.Command`) boot, run and stop it; the others
run between `app.Boot` and `app.Close`, which also runs if the command
panics. `help` doesn't boot the app, so it works without a database;
commands that boot it (`routes:list` too) need the database reachable.

> **Coming from Laravel?** This is `artisan` built into your binary:
> `./blog migrate` instead of `php artisan migrate`, and custom commands
> are functions registered on the app instead of classes. Code generators
> (`make:*`) live in the `anetos` tool instead, since they write source.

## Testing it

`app.ExecuteArgs(ctx, args, stdout, stderr)` runs a command without
exiting and returns the exit status, as
[`examples/database`](../../../examples/database/main_test.go) does. An
app runs one command: create a new one per call.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `unknown command "migrate"` | `migrate.ForApp` wasn't called before `Execute` | Call it while setting up the app |
| `command "x" registered twice` | Two commands with one name (`app.Command` panics, `app.AddCommand`, `migrate.ForApp` and `web.NewServer` return the error) | Register each once |
| `unknown role "…"` | `--only` names a role no component has and no package declared (the error lists the known ones) | Check `help run` and the roles of your components |

## Next steps

- [`anetos` tool reference](../reference/cli.md)
- [Migrations](migrations.md)
