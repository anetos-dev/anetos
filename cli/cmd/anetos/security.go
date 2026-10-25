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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos/cli/internal/secscan"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/internal/securitycheck"
)

const securityUsage = `Usage: anetos security:check [--level=basic|advanced] [--format=text|json|sarif|markdown] [--out=FILE] [--ignore=FILE] [--strict] [--no-vuln] [--url=URL [--live-only] [--allow-host=HOST]]

Checks the project's code: request data that reaches SQL, HTML,
redirects, commands and files, certificates not checked, math/rand for
secrets, secrets written in the files, and (govulncheck, built in) known
vulnerabilities in the code the app calls. Then builds the app and runs
its security:check command in the project, which adds these findings to
its own: its settings and routes checked against security rules (CSRF,
logins on admin routes, rate limits, cookies, TLS, debug endpoints…).
One report, one ignore file. The flags are the app's, but --no-vuln,
which skips govulncheck (it needs the network for vuln.go.dev), and
the live scan's.

--url=URL also checks the running site, with a few GET requests (and a
TRACE at the advanced level): security headers, cookies, TLS, the HTTPS
redirect, exposed files (/.env, /.git, /debug/), stack traces in error
pages; advanced: CORS, TRACE, version headers, certificates about to
expire. Its host must be APP_URL's (from the environment, else .env) or
localhost; --allow-host=HOST adds one. --live-only (or outside a
project) runs the live checks only: no .env, no build. See go tool
anetos docs guides/security-check.

The settings checked are this machine's (.env and the environment), as
doctor's: run ./<app> security:check where the production settings
are too. Without a .env (and no APP_KEY in the environment), it says
how to write one. Files given to --out and --ignore are relative to
the current folder. Exits 1 on a problem (--strict: on a warning too).
`

// securityCheck runs anetos security:check.
func securityCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if slices.ContainsFunc(args, isHelp) {
		fmt.Fprint(stdout, securityUsage)
		return 0
	}
	vuln := true
	for _, a := range args {
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch {
		case !strings.HasPrefix(a, "-"):
		case name == "no-vuln":
			skip, err := strconv.ParseBool(cmp.Or(value, "true"))
			if err != nil || hasValue && value == "" {
				fmt.Fprintf(stderr, "anetos security:check: %s: true or false\n", a)
				return 2
			}
			vuln = !skip
		case name == "findings":
			fmt.Fprintln(stderr, "anetos security:check: --findings is the tool's own: run ./<app> security:check --findings=FILE to add a file of yours")
			return 2
		}
	}
	var lo liveOpts
	var err error
	if args, err = lo.parse(args); err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 2
	}
	args = slices.DeleteFunc(args, func(a string) bool {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		return strings.HasPrefix(a, "-") && name == "no-vuln"
	})
	if lo.url != "" {
		if root, err := projectRoot(); lo.only || err != nil {
			if err == nil {
				lo.root = root
			}
			return liveOnly(ctx, lo, args, stdout, stderr)
		}
	}
	root, err := projectRoot()
	lo.root = root
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	if lo.url != "" {
		if _, err := secscan.CheckURL(lo.url, lo.allowed()); err != nil {
			fmt.Fprintln(stderr, "anetos security:check:", err)
			return 2
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); err != nil && os.Getenv("APP_KEY") == "" {
		fmt.Fprintln(stderr, "anetos security:check: there's no .env (a fresh clone, CI?), and the app needs its settings to boot. Write one: cp .env.example .env && go tool anetos key:generate (or set APP_ENV and APP_KEY)")
		return 1
	}
	args = absPaths(args)
	tmp, err := os.MkdirTemp("", "anetos-security-")
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "app")
	if err := runGo(ctx, root, stderr, "build", "-o", bin, "."); err != nil {
		fmt.Fprintln(stderr, "anetos security:check: the app doesn't build:", err)
		return 1
	}
	rep, err := secscan.Scan(ctx, secscan.Options{Dir: root, Level: securityLevel(args), Vuln: vuln})
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	if lo.url != "" {
		found, err := lo.run(ctx, securityLevel(args))
		if err != nil {
			// The rest of the report still stands: say the site wasn't checked.
			found = []secscan.Finding{{Rule: "live.unchecked", Severity: "warning", Where: lo.url,
				Message: "the running site wasn't checked: " + err.Error()}}
		}
		rep.Findings = append(rep.Findings, found...)
	}
	findings := filepath.Join(tmp, "findings.json")
	b, err := json.Marshal(rep)
	if err == nil {
		err = os.WriteFile(findings, b, 0o600)
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	var appErr bytes.Buffer
	appArgs := append([]string{"security:check", "--findings=" + findings}, args...)
	if err := runApp(ctx, root, bin, stdout, io.MultiWriter(stderr, &appErr), appArgs...); err != nil {
		if strings.Contains(appErr.String(), `unknown command "security:check"`) {
			fmt.Fprintln(stderr, "anetos security:check: the app's version of Anetos has no security:check command (v0.5 has)")
			return 1
		}
		if strings.Contains(appErr.String(), "flag provided but not defined: -findings") {
			fmt.Fprintln(stderr, "anetos security:check: the app's version of Anetos is older than the tool's: update it (go get anetos.dev/anetos@<the tool's version>)")
			return 1
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode() // the app printed why
		}
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	return 0
}

// securityLevel is the --level among the app's flags.
func securityLevel(args []string) secscan.Level {
	level := secscan.Basic
	for i, a := range args { // the last one counts, as for the app
		name, value, ok := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name != "level" || !strings.HasPrefix(a, "-") {
			continue
		}
		if !ok && i+1 < len(args) {
			value = args[i+1]
		}
		level = secscan.Basic
		if value == "advanced" {
			level = secscan.Advanced
		}
	}
	return level
}

// absPaths makes the files --out and --ignore name absolute: the app
// runs in the project's root, and they are relative to where you are.
func absPaths(args []string) []string {
	out := slices.Clone(args)
	for i := 0; i < len(out); i++ {
		name, value, hasValue := strings.Cut(strings.TrimLeft(out[i], "-"), "=")
		if !strings.HasPrefix(out[i], "-") || name != "out" && name != "ignore" {
			continue
		}
		if !hasValue {
			if i+1 == len(out) {
				break
			}
			i++
			value = out[i]
		}
		if value == "" || value == "-" || filepath.IsAbs(value) {
			continue
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			continue
		}
		if hasValue {
			out[i] = "--" + name + "=" + abs
		} else {
			out[i] = abs
		}
	}
	return out
}

func isHelp(s string) bool { return s == "-h" || s == "-help" || s == "--help" }

// liveOpts are the live scan's flags.
type liveOpts struct {
	url   string
	only  bool
	hosts []string // --allow-host
	root  string   // the project, "" outside one
}

// parse takes the live scan's flags out of args.
func (lo *liveOpts) parse(args []string) ([]string, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		switch name {
		case "url", "allow-host":
			if !hasValue {
				if i+1 == len(args) {
					return nil, fmt.Errorf("--%s needs a value", name)
				}
				i++
				value = args[i]
			}
			if name == "url" {
				lo.url = value
			} else {
				for h := range strings.SplitSeq(value, ",") {
					if h = strings.TrimSpace(h); h != "" {
						lo.hosts = append(lo.hosts, h)
					}
				}
			}
		case "live-only":
			only, err := strconv.ParseBool(cmp.Or(value, "true"))
			if err != nil {
				return nil, fmt.Errorf("%s: true or false", a)
			}
			lo.only = only
		default:
			rest = append(rest, a)
		}
	}
	if lo.only && lo.url == "" {
		return nil, errors.New("--live-only needs --url")
	}
	return rest, nil
}

// allowed are the hosts the scan may reach: APP_URL's (the
// environment's, else the project's .env, read as the app reads it) and
// --allow-host's; localhost always is.
func (lo *liveOpts) allowed() []string {
	hosts := slices.Clone(lo.hosts)
	appURL := os.Getenv("APP_URL")
	if appURL == "" && lo.root != "" {
		if b, err := os.ReadFile(filepath.Join(lo.root, ".env")); err == nil {
			if m, err := config.ParseDotenv(bytes.NewReader(b), nil); err == nil {
				appURL = m["APP_URL"]
			}
		}
	}
	if u, err := url.Parse(appURL); err == nil && u.Hostname() != "" {
		hosts = append(hosts, u.Hostname())
	}
	return hosts
}

// run scans the site.
func (lo *liveOpts) run(ctx context.Context, level secscan.Level) ([]secscan.Finding, error) {
	return secscan.Live(ctx, secscan.LiveOptions{URL: lo.url, Level: level, Hosts: lo.allowed(), Version: version()})
}

// liveOnly runs the live checks alone and writes their report, as the
// app's security:check would (the ignore file, the formats, the exit
// status), without building the app.
func liveOnly(ctx context.Context, lo liveOpts, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("security:check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	levelName := fs.String("level", "basic", "")
	format := fs.String("format", "text", "")
	out := fs.String("out", "", "")
	ignore := fs.String("ignore", "", "")
	strict := fs.Bool("strict", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "anetos security:check: with --live-only, the flags are --level, --format, --out, --ignore and --strict")
		return 2
	}
	level := secscan.Basic
	switch *levelName {
	case "basic":
	case "advanced":
		level = secscan.Advanced
	default:
		fmt.Fprintf(stderr, "anetos security:check: --level is basic or advanced, not %q\n", *levelName)
		return 2
	}
	write := securitycheck.Formats[*format]
	if write == nil {
		fmt.Fprintf(stderr, "anetos security:check: --format is text, json, sarif or markdown, not %q\n", *format)
		return 2
	}
	if _, err := secscan.CheckURL(lo.url, lo.allowed()); err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 2
	}
	now := time.Now()
	ignorePath, readPath := *ignore, *ignore
	if ignorePath == "" {
		ignorePath, readPath = securitycheck.DefaultIgnoreFile, securitycheck.DefaultIgnoreFile
		if lo.root != "" {
			readPath = filepath.Join(lo.root, securitycheck.DefaultIgnoreFile) // the project's, from a folder of it too
		}
	}
	ignores, err := securitycheck.ReadIgnores(readPath, *ignore != "", now.Location())
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	found, err := lo.run(ctx, level)
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	host := lo.url
	if u, err := url.Parse(lo.url); err == nil {
		host = u.Host
	}
	rep := securitycheck.Report{App: host, Level: *levelName, Limits: liveLimits, Rules: map[string]securitycheck.Rule{},
		IgnoreFile: ignorePath, Version: version()}
	for _, r := range secscan.RuleList(level) {
		rep.Rules[r.ID] = securitycheck.Rule{ID: r.ID, Summary: r.Summary, OWASP: r.OWASP, Docs: r.Docs}
	}
	all := make([]securitycheck.Finding, 0, len(found))
	for _, f := range found {
		var sev securitycheck.Severity
		_ = sev.UnmarshalText([]byte(f.Severity))
		all = append(all, securitycheck.Finding{Rule: f.Rule, Severity: sev, Where: f.Where, File: f.File, Line: f.Line, Message: f.Message,
			OWASP: rep.Rules[f.Rule].OWASP})
	}
	rep.Finish(all, ignores, now)
	var buf bytes.Buffer
	if err := write(&buf, rep); err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	if *out != "" && *out != "-" {
		if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
			fmt.Fprintln(stderr, "anetos security:check:", err)
			return 1
		}
		fmt.Fprintf(stdout, "Wrote %s: %s.\n", *out, rep.SummaryLine())
	} else {
		_, _ = stdout.Write(buf.Bytes())
	}
	if rep.Summary["problems"] > 0 || *strict && rep.Summary["warnings"] > 0 {
		return 1
	}
	return 0
}

// liveLimits says what a clean live scan means.
const liveLimits = "None of these checks finding anything doesn't make the site secure: they look at its public pages from outside; the code, the settings and the routes are the full check's (in the project)."
