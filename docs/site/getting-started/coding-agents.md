---
title: Build with a coding agent
since: v0.5.0
group: "Installation"
weight: 13
---

# Build with a coding agent

Coding agents such as Claude Code, Codex, Cursor, Gemini CLI and GitHub
Copilot can build an Anetos app with you. Every project `anetos new`
makes is ready for them: it has instructions they read before they
start, guides for the common tasks, the documentation at the project's
version a command away, and one command that tells them whether a
change works.

## Before you start

- An Anetos project ([Create a project](create-a-project.md)), or an
  existing one (step 5).
- A coding agent that works in your project's folder.

## Steps

### 1. Start the agent in the project

Open the agent in the project's folder, as you would for any other
work. What `anetos new` wrote is already there, and committed with the
rest:

| File | Read by | Holds |
|---|---|---|
| `AGENTS.md` | every agent above | The project (stack, database, CSS framework), the commands, where things go, the conventions, and how to check a change |
| `CLAUDE.md`, `GEMINI.md` | Claude Code, Gemini CLI | One line, `@AGENTS.md`, importing it |
| `.agents/skills/anetos-*/SKILL.md` | Codex, Copilot, Cursor, Gemini CLI | Skills: step-by-step guides an agent loads only for a task that needs one |
| `.claude/skills/anetos-*/SKILL.md` | Claude Code (and Copilot, Cursor) | The same skills |
| `.mcp.json`, `.vscode/mcp.json`, `.cursor/mcp.json`, `.gemini/settings.json`, `.codex/config.toml` | Claude Code and Copilot; VS Code; Cursor; Gemini CLI; Codex | The project's MCP server, `go tool anetos mcp` (step 4) |

The skills cover a resource (`anetos-crud`), accounts and access
(`anetos-auth`), schema changes (`anetos-migration`), background work
(`anetos-job`), pages and forms (`anetos-page`; in an API project,
`anetos-api` for endpoints), tests (`anetos-test`), deploying
(`anetos-deploy`), checking security and fixing its findings
(`anetos-security`), upgrading (`anetos-upgrade`) and plugins
(`anetos-plugin`).

### 2. Ask for a feature

Ask for what you want, in your words: "add a blog with posts and
comments, only logged-in users can comment". `AGENTS.md` tells the agent
to use the generators first (`make:crud`, `make:auth`), Anetos's
conventions (typed handlers, typed columns, the components of
`views/ui`), and to read the docs before guessing an API.

### 3. Let it read the docs and check its work

Two commands do most of the work for an agent, and are as useful to you:

```sh
go tool anetos docs --search "comments on a post"   # which pages to read
go tool anetos docs guides/relations                # print one, as Markdown
go tool anetos check                                # generate, build, vet, test, doctor, security
```

The pages `docs` prints are those of the Anetos version in your
`go.mod`, built into the tool: no network, and never another version's.
`check` stops at the first failing step and prints its output; with
`--json` it prints one object an agent can read. `AGENTS.md` asks the
agent to run it before calling a change done. Its last step runs the
security check's basic rules ([Check your app's
security](../guides/security-check.md)): a problem, such as SQL built
from request data, fails it. In a fresh clone, with no `.env` (an
agent's sandbox, CI), its doctor step checks the project's files only,
its security step the code only, and they say how to write one:
`cp .env.example .env && go tool anetos key:generate`.

### 4. Let it ask the app

Rather than reading every file to learn the app, an agent asks it:

```sh
go tool anetos model:list            # the models: tables, columns, relations
go run . route:list --json           # every route: handler, types, middleware
go run . db:schema posts             # a table as the database has it
go run . about                       # versions, services, settings (secrets hidden)
```

`model:list` reads the code, so it works before the app does; the
others are the app's own commands, which need a `.env`. Each takes
`--json` (and `migrate:status`, `schedule:list`, `plugin:list` and
`help` do too).

The agent can also call them as tools: the project's MCP server,
`go tool anetos mcp`, is in each agent's MCP settings (the files in the
table above), with `docs_search`, `docs_read`, `model_list`,
`route_list`, `db_schema`, `app_about`, `check`, `doctor` and
`security_check`. It only reads and checks. Agents decide differently when a project's server
starts: Claude Code asks you first; VS Code, Copilot CLI and Codex
start it in a folder you trust (Workspace Trust, folder trust, a trusted
project); Gemini CLI starts it unless you turned folder trust on and
haven't trusted the folder. `about` hides what looks like a secret (by the
setting's name, and a connection string's password), but shows the
other settings, and an agent sends what it reads to its model's
provider: read it before pasting it anywhere public.

### 5. Add the files to an existing project

A project made before these files, or with `anetos new --no-agent`:

```sh
go tool anetos agent:install                        # for every agent above
go tool anetos agent:install --agent=claude,codex   # or some of them
```

`claude` writes `CLAUDE.md` and `.claude/skills`; `gemini`, `GEMINI.md`
and `.agents/skills`; `codex`, `copilot` and `cursor`, `.agents/skills`.
An existing `CLAUDE.md` or `GEMINI.md` keeps its text, with the import
added first. The files are those of exactly the agents you list:
`--agent=claude` also removes Anetos's skills from `.agents/skills`.

### 6. Keep them current

After upgrading Anetos, refresh them:

```sh
go tool anetos agent:update
```

`go tool anetos doctor` warns when they're out of date.

## Make them yours

- **Your own instructions** go in `AGENTS.md` outside its markers
  (`<!-- anetos:begin … -->` and `<!-- anetos:end -->`): `agent:update`
  rewrites only the text between them. Or in `CLAUDE.md` or
  `GEMINI.md`, after the import.
- **Your own skills** go next to Anetos's, under names that don't start
  with `anetos-`: `agent:update` leaves them alone.
- **Your own MCP servers** go in the same files: `agent:update` changes
  only the `anetos` entry. gopls has one for Go itself (`gopls mcp`).

## How it works

`AGENTS.md` is the shared instruction file most coding agents read at
the start of a session; Claude Code and Gemini CLI read their own file,
which imports it. Skills follow the Agent Skills format (a folder with a
`SKILL.md`, its `name` and `description` first): an agent sees each
skill's description, and loads the rest only when a task matches, so
they don't fill its context.

`docs` reads the pages from the package `anetos.dev/anetos/docs`, in
the core module, which holds `docs/site`'s Markdown; the tool is built from the version your
`go.mod` pins, so the pages match the code. Its search matches whole
words and ranks pages by them, rare ones counting most.

An agent that browses rather than runs commands can read the same
pages on the site: `https://docs.anetos.dev/llms.txt` lists them, each
with a link to its Markdown copy, and `llms-full.txt` holds them all.

A coding agent works on your code. `make:agent` is something else: it
writes an AI agent that runs inside your app, for your users
([Add AI to your app](../guides/ai.md)).

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| The agent writes code for another framework (its helpers, an ORM's methods Anetos doesn't have) | It guessed instead of reading | Ask it to read `AGENTS.md` and run `go tool anetos docs --search …` first; `check` catches what doesn't compile |
| Copilot or Cursor lists each skill twice | Both folders, `.agents/skills` and `.claude/skills`, are there, and they read both | Install for the agents you use only: `go tool anetos agent:install --agent=cursor` removes Anetos's skills from `.claude/skills` |
| `doctor` warns that agent files differ | An Anetos upgrade, or an edit between `AGENTS.md`'s markers | `go tool anetos agent:update` (move your edits outside the markers first) |
| Gemini CLI doesn't follow `AGENTS.md` | No `GEMINI.md` | `go tool anetos agent:install --agent=gemini` |
| The agent has no `anetos` tools | It hasn't been allowed to start the project's MCP server, or its settings file isn't there | Approve the `anetos` server in the agent's MCP settings (Codex: trust the project); `go tool anetos agent:install --agent=<agent>` writes the file |
| The `anetos` server doesn't start: `go: no such tool "anetos"` | The agent started it outside the project (Claude's desktop app starts servers in your home folder) | Start it as `go -C <the project's path> tool anetos mcp`: in your own (user) settings, not the committed file |

## Next steps

- [Create a project](create-a-project.md), then ask your agent for its
  first feature.
- [anetos tool reference](../reference/cli.md): `docs`, `check`,
  `agent:install` and `agent:update`.
