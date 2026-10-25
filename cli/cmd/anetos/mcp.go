// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"anetos.dev/anetos/cli/internal/docpages"
	"anetos.dev/anetos/cli/internal/modelgen"
	"anetos.dev/anetos/cli/internal/scaffold"
	"anetos.dev/anetos/docs"
)

const mcpUsage = `Usage: anetos mcp [--dir=<project>]

Runs an MCP server on standard input and output, for coding agents:
its tools read the project and the documentation, and check changes.
Nothing it does changes the project, but for the generated code check
writes, as anetos dev does. The agent starts it, from the files
agent:install writes (.mcp.json, .cursor/mcp.json, .vscode/mcp.json,
.gemini/settings.json, .codex/config.toml); you don't run it yourself.

Tools:
  docs_list, docs_read, docs_search   the documentation, at the project's version
  model_list                          the models, read from the code
  app_about, route_list, db_schema,   the app's own commands, with --json
  migrate_status, command_list        (it builds the app; they need a .env)
  check, doctor, security_check       does the change work, is it safe?

The project is --dir's, else the one around the current directory.
The tools that ask the app run its commands, which create what the app
creates when it starts (storage/, an empty SQLite database).
`

// mcpCmd runs anetos mcp.
func mcpCmd(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos mcp", flag.ContinueOnError)
	dir := fs.String("dir", "", "the project's directory (default: the one around the current directory)")
	pos, code := parse(fs, args, stderr, mcpUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fs.Usage()
		return 2
	}
	ctx := context.Background()
	server, cleanup := newMCPServer(*dir)
	defer cleanup()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "anetos mcp:", err)
		return 1
	}
	return 0
}

// mcpInstructions tell the agent what the server is for.
const mcpInstructions = `Tools for an Anetos project (a Go web framework). Read the project's AGENTS.md first.
Before guessing an Anetos API, search the docs (docs_search) and read the page (docs_read): they are the project's Anetos version.
To learn the app, ask it: model_list, route_list, db_schema, app_about.
Before calling a change done, run check: it must pass.`

// projectFinder finds the project the tools work on: --dir's, else the
// one around the current directory (agents start a project's server in
// it, or with go -C).
type projectFinder struct {
	dir  string // --dir
	mu   sync.Mutex
	root string
}

// find returns the project's root.
func (p *projectFinder) find() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.root != "" {
		return p.root, nil
	}
	start := p.dir
	if start == "" {
		start, _ = os.Getwd()
	}
	root, err := scaffold.FindRoot(start)
	if err != nil {
		return "", fmt.Errorf("no Anetos project here (%s): start the server in the project (go -C <project> tool anetos mcp), or with --dir", start)
	}
	p.root = root
	return root, nil
}

// text is a tool's result: text, or a tool error.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func toolError(err error) *mcp.CallToolResult {
	r := text(err.Error())
	r.IsError = true
	return r
}

type (
	noInput     struct{}
	searchInput struct {
		Query string `json:"query" jsonschema:"the words to look for, such as \"retry a failed job\""`
		Limit int    `json:"limit,omitempty" jsonschema:"the most pages to return (default 5)"`
	}
	pageInput struct {
		Page string `json:"page" jsonschema:"the page: its path (guides/queues), the end of it (queues), a word of its title, or a link from another page (../guides/mail.md)"`
	}
	packagesInput struct {
		Packages []string `json:"packages,omitempty" jsonschema:"the packages to look in (default ./...)"`
	}
	tablesInput struct {
		Tables []string `json:"tables,omitempty" jsonschema:"the tables to describe (default: all)"`
	}
	checkInput struct {
		Short bool `json:"short,omitempty" jsonschema:"run go test -short"`
	}
	securityInput struct {
		Level string `json:"level,omitempty" jsonschema:"basic (the default: few false alarms) or advanced (adds what needs a look)"`
		URL   string `json:"url,omitempty" jsonschema:"the running site to check too (APP_URL's host or localhost)"`
		Vuln  bool   `json:"vuln,omitempty" jsonschema:"run govulncheck too (needs the network; takes a while)"`
	}
)

// newMCPServer returns the MCP server of anetos mcp: read and check
// only (design D327), and the function that removes its temporary files.
func newMCPServer(dir string) (*mcp.Server, func()) {
	s := mcp.NewServer(&mcp.Implementation{Name: "anetos", Title: "Anetos", Version: version()}, &mcp.ServerOptions{Instructions: mcpInstructions})
	project := &projectFinder{dir: dir}
	app := &appRunner{}
	var checking sync.Mutex // one check at a time: they write the generated code
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}

	pages := sync.OnceValues(func() ([]docpages.Page, error) { return docpages.Load(docs.Site) })
	mcp.AddTool(s, &mcp.Tool{Name: "docs_list", Description: "List the pages of Anetos's documentation, at the project's Anetos version: each page's name (for docs_read) and title.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
			all, err := pages()
			if err != nil {
				return toolError(err), nil, nil
			}
			var b strings.Builder
			for _, p := range all {
				fmt.Fprintf(&b, "%s\t%s\n", p.ID, p.Title)
			}
			return text(b.String()), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "docs_read", Description: "Print a page of Anetos's documentation as Markdown, at the project's Anetos version. Read the page before using an API you haven't seen in the project.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, in pageInput) (*mcp.CallToolResult, any, error) {
			all, err := pages()
			if err != nil {
				return toolError(err), nil, nil
			}
			found := docpages.Find(all, in.Page)
			switch len(found) {
			case 0:
				return toolError(fmt.Errorf("no page %q: search for it with docs_search", in.Page)), nil, nil
			case 1:
				return text(docpages.Show(found[0])), nil, nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%q could be several pages; read one of them:\n", in.Page)
			for _, p := range found {
				fmt.Fprintf(&b, "%s\t%s\n", p.ID, p.Title)
			}
			return toolError(errors.New(b.String())), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "docs_search", Description: "Search Anetos's documentation, at the project's Anetos version: the pages that best match the words, with their lines that match. Then read a page with docs_read.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
			all, err := pages()
			if err != nil {
				return toolError(err), nil, nil
			}
			if strings.TrimSpace(in.Query) == "" {
				return toolError(errors.New("the query is empty")), nil, nil
			}
			limit := in.Limit
			if limit < 1 {
				limit = 5
			}
			results := docpages.Search(all, in.Query, limit)
			if len(results) == 0 {
				return text(fmt.Sprintf("No page mentions %q.", in.Query)), nil, nil
			}
			var b strings.Builder
			for _, r := range results {
				fmt.Fprintf(&b, "%s (docs_read %s)\n", r.Page.Title, r.Page.ID)
				for _, l := range r.Lines {
					fmt.Fprintf(&b, "    %s\n", l)
				}
			}
			return text(b.String()), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "model_list", Description: "List the project's models, read from its code (the app needn't run): each one's table, columns (the column, the typed column's field in <Model>Cols, the Go type) and relations, as JSON.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in packagesInput) (*mcp.CallToolResult, any, error) {
			root, err := project.find()
			if err != nil {
				return toolError(err), nil, nil
			}
			models, listErr := modelgen.List(root, in.Packages...)
			if listErr != nil && len(models) == 0 {
				return toolError(listErr), nil, nil
			}
			out, err := json.MarshalIndent(models, "", "  ")
			if err != nil {
				return toolError(err), nil, nil
			}
			res := text(string(out))
			if listErr != nil {
				res = text(string(out) + "\n\nSome packages failed:\n" + listErr.Error())
			}
			return res, nil, nil
		})

	appTool := func(name, description string, args ...string) {
		mcp.AddTool(s, &mcp.Tool{Name: name, Description: description, Annotations: readOnly},
			func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
				root, err := project.find()
				if err != nil {
					return toolError(err), nil, nil
				}
				out, err := app.run(ctx, root, args...)
				if err != nil {
					return toolError(err), nil, nil
				}
				return text(out), nil, nil
			})
	}
	appTool("app_about", "Describe the app (its about command): versions, services, components, and the settings it reads with their values, secrets hidden. Builds and runs the app's command; needs a .env.",
		"about", "--json")
	appTool("route_list", "List the app's routes (route:list --json): method, path, name, handler, input and result types, and every middleware, with the global middleware. Builds and runs the app's command; needs a .env.",
		"route:list", "--json")
	appTool("migrate_status", "List the migrations and whether they ran (migrate:status --json). Builds and runs the app's command; needs a .env and the database.",
		"migrate:status", "--json")
	appTool("command_list", "List the app binary's commands (help --json), its own included. Builds and runs the app.",
		"help", "--json")
	mcp.AddTool(s, &mcp.Tool{Name: "db_schema", Description: "Describe the database's tables as the database has them (db:schema --json): columns, indexes, foreign keys. Builds and runs the app's command; needs a .env and the database.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, in tablesInput) (*mcp.CallToolResult, any, error) {
			root, err := project.find()
			if err != nil {
				return toolError(err), nil, nil
			}
			out, err := app.run(ctx, root, append([]string{"db:schema", "--json"}, in.Tables...)...)
			if err != nil {
				return toolError(err), nil, nil
			}
			return text(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "check", Description: "Check that the project works: generate code, go build, go vet, go test, then doctor and the security check's basic rules, stopping at the first failure; the result as JSON, each step's output included. Run it before calling a change done. Slow: it runs the tests.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true}},
		func(ctx context.Context, req *mcp.CallToolRequest, in checkInput) (*mcp.CallToolResult, any, error) {
			root, err := project.find()
			if err != nil {
				return toolError(err), nil, nil
			}
			checking.Lock()
			defer checking.Unlock()
			var out bytes.Buffer
			code := runCheck(ctx, root, in.Short, true, &out)
			r := text(out.String())
			r.IsError = code != 0
			return r, nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "security_check", Description: "Check the project against security rules (anetos security:check --format=json): the code (SQL, HTML, redirects and commands built from request data, secrets), the routes (CSRF, logins, rate limits), the settings, and optionally the running site and known vulnerabilities. Each finding has its rule, place, fix and docs link; an error result on a problem. Builds and runs the app; without a .env, the code's rules only. To fix findings, read the anetos-security skill or the guide (docs_read guides/security-check).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}},
		func(ctx context.Context, req *mcp.CallToolRequest, in securityInput) (*mcp.CallToolResult, any, error) {
			root, err := project.find()
			if err != nil {
				return toolError(err), nil, nil
			}
			level := cmp.Or(in.Level, "basic")
			if level != "basic" && level != "advanced" {
				return toolError(fmt.Errorf("level is basic or advanced, not %q", in.Level)), nil, nil
			}
			args := []string{"--format=json", "--level=" + level}
			if !in.Vuln {
				args = append(args, "--no-vuln")
			}
			if in.URL != "" {
				args = append(args, "--url="+in.URL)
			}
			checking.Lock()
			defer checking.Unlock()
			var out, errOut syncBuffer
			if doctorOptionsFor(root).projectOnly != "" && in.URL == "" {
				// No .env: the app can't boot; the code's rules run alone
				// (the report's limits say so).
				ok, err := codeReport(ctx, root, "json", &out)
				if err != nil {
					return toolError(err), nil, nil
				}
				r := text(out.String())
				r.IsError = !ok
				return r, nil, nil
			}
			code := securityCheckAt(ctx, root, nil, args, &out, &errOut)
			if code == 2 || strings.TrimSpace(out.String()) == "" {
				return toolError(errors.New(strings.TrimSpace(errOut.String()))), nil, nil
			}
			r := text(out.String())
			r.IsError = code != 0
			return r, nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "doctor", Description: "Check the project's files and the app's settings for unsafe or broken values (anetos doctor).", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
			root, err := project.find()
			if err != nil {
				return toolError(err), nil, nil
			}
			var out syncBuffer
			code := runDoctor(ctx, root, doctorOptionsFor(root), &out, &out)
			r := text(out.String())
			r.IsError = code != 0
			return r, nil, nil
		})
	return s, app.cleanup
}

// appRunner builds the app and runs its commands, one at a time: go
// build's cache makes a build without changes quick.
type appRunner struct {
	mu  sync.Mutex
	dir string // the binary's temporary directory
}

// cleanup removes the app's binary.
func (a *appRunner) cleanup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dir != "" {
		_ = os.RemoveAll(a.dir)
		a.dir = ""
	}
}

// run builds the app in root and runs its command args there, returning
// its output.
func (a *appRunner) run(ctx context.Context, root string, args ...string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dir == "" {
		dir, err := os.MkdirTemp("", "anetos-mcp-")
		if err != nil {
			return "", err
		}
		a.dir = dir
	}
	bin := filepath.Join(a.dir, "app")
	var build bytes.Buffer
	if err := runGo(ctx, root, &build, "build", "-o", bin, "."); err != nil {
		return "", fmt.Errorf("the app doesn't build: %w\n%s", err, build.String())
	}
	var out, errOut bytes.Buffer
	if err := runApp(ctx, root, bin, &out, &errOut, args...); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if _, statErr := os.Stat(filepath.Join(root, ".env")); statErr != nil {
			msg += "\nThere's no .env (a fresh clone?): write one with cp .env.example .env && go tool anetos key:generate"
		}
		return "", fmt.Errorf("%w\n%s", err, msg)
	}
	return out.String(), nil
}
