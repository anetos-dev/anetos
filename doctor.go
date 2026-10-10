// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"text/tabwriter"

	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/internal/netaddr"
)

// Severity says how much a doctor [Finding] matters.
type Severity int

const (
	// Note is for information: a choice worth knowing about, nothing to
	// fix.
	Note Severity = iota
	// Warning is a setting that is often a mistake: look at it.
	Warning
	// Problem is a setting that is unsafe or broken: fix it. The doctor
	// command exits 1.
	Problem
)

// String returns "note", "warning" or "problem".
func (s Severity) String() string {
	switch s {
	case Note:
		return "note"
	case Warning:
		return "warning"
	case Problem:
		return "problem"
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

// Finding is what a [Check] found.
type Finding struct {
	// Severity says how much it matters.
	Severity Severity
	// Message says what is wrong and what to do, naming the setting:
	// "SESSION_SECURE=false in production: session cookies go over plain
	// HTTP; remove the setting".
	Message string
}

// Check is a check of the app's settings that the doctor command runs
// (v0.3). Features add theirs when they are set up (session.New
// checks SESSION_SECURE, migrate.New checks for pending migrations);
// an app or plugin can add its own with [App.AddCheck].
type Check struct {
	// Name says what it checks, as doctor prints it: "app", "db",
	// "session".
	Name string
	// Booted runs it after the app has booted, for checks that need a
	// service: the database's connection, say. Other checks run first,
	// so doctor reports them even when the app can't boot.
	Booted bool
	// Run returns what it found: nothing when all is well. The context
	// has the app's values (see [App.Context]).
	Run func(ctx context.Context) []Finding
}

// AddCheck adds a check to the app's doctor command. Checks run in the
// order they were added, the booted ones last, after doctor boots the
// app; checks added while it boots (by a plugin's Boot) run then too:
//
//	app.AddCheck(anetos.Check{Name: "billing", Run: func(ctx context.Context) []anetos.Finding {
//		if cfg.TestMode && app.Config().Env.IsProduction() {
//			return []anetos.Finding{{Severity: anetos.Problem, Message: "BILLING_TEST_MODE=true in production: nobody pays"}}
//		}
//		return nil
//	}})
//
// It panics if c has no name or no Run, which is a programming error.
func (a *App) AddCheck(c Check) {
	if c.Name == "" || c.Run == nil {
		panic("anetos: AddCheck needs a Name and a Run function")
	}
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()
	a.checks = append(a.checks, c)
}

// Deployed reports whether the environment is one the world reaches,
// production or staging: where the doctor's checks of deployment
// settings apply.
func (e Environment) Deployed() bool { return e == Production || e == Staging }

// doctorCommand is the doctor command: it runs the checks, boots the app
// for the booted ones, and prints what they found.
func (a *App) doctorCommand() cmd.Command {
	return cmd.Command{
		Name:        "doctor",
		Usage:       "[--strict]",
		Description: "Check the app's settings for unsafe or broken values (APP_DEBUG, SESSION_SECURE, DB_TLS…) and pending migrations; exits 1 on problems",
		ManagesApp:  true, // boots after the checks that don't need it
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
			strict := fs.Bool("strict", false, "exit 1 on warnings too")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 0 {
				return cmd.Usagef("unexpected argument %q", fs.Arg(0))
			}
			return a.doctor(ctx, args.Stdout, *strict)
		},
	}
}

func (a *App) doctor(ctx context.Context, w io.Writer, strict bool) error {
	a.cmdMu.Lock()
	checks := append([]Check(nil), a.checks...)
	a.cmdMu.Unlock()

	env := a.cfg.Env
	fmt.Fprintf(w, "Checking %s (APP_ENV=%s).\n", a.cfg.Name, env)
	if !env.Deployed() {
		fmt.Fprintf(w, "The checks of deployment settings apply to production and staging: run doctor with those settings too (on the server: ./%s doctor).\n", a.cfg.Name)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	counts := map[Severity]int{}
	report := func(name string, found []Finding) {
		if len(found) == 0 {
			fmt.Fprintf(tw, "  ok\t%s\n", name)
			return
		}
		for _, f := range found {
			counts[f.Severity]++
			fmt.Fprintf(tw, "  %s\t%s: %s\n", f.Severity, name, f.Message)
		}
	}
	run := func(ctx context.Context, c Check) {
		defer func() {
			if v := recover(); v != nil {
				report(c.Name, []Finding{{Problem, fmt.Sprintf("the check panicked: %v", v)}})
			}
		}()
		report(c.Name, c.Run(ctx))
	}
	base := a.Context(ctx)
	var booted []Check
	for _, c := range checks {
		if c.Booted {
			booted = append(booted, c)
			continue
		}
		run(base, c)
	}
	// Then the app boots, for the booted checks and for those that
	// providers (plugins) add as they boot. An app booted already (in a
	// test) is left open.
	var closeErr error
	wasBooted := a.Booted()
	if err := a.Boot(ctx); err != nil {
		if len(booted) > 0 {
			report("boot", []Finding{{Problem, fmt.Sprintf("the app doesn't boot, so %d check(s) didn't run: %v", len(booted), err)}})
		} else {
			report("boot", []Finding{{Problem, fmt.Sprintf("the app doesn't boot: %v", err)}})
		}
	} else {
		a.cmdMu.Lock()
		booted = append(booted, a.checks[len(checks):]...) // added during Boot
		a.cmdMu.Unlock()
		bctx := a.Context(ctx)
		for _, c := range booted {
			run(bctx, c)
		}
		if !wasBooted {
			closeErr = a.Close()
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	summary := fmt.Sprintf("%s, %s", plural(counts[Problem], "problem"), plural(counts[Warning], "warning"))
	if n := counts[Note]; n > 0 {
		summary += ", " + plural(n, "note")
	}
	fmt.Fprintln(w, summary+".")
	if counts[Problem] > 0 || strict && counts[Warning] > 0 {
		return errors.Join(errors.New(summary), closeErr)
	}
	return closeErr
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// appChecks are the checks of the APP_* settings.
func (a *App) appChecks(context.Context) []Finding {
	c := a.cfg
	if !c.Env.Deployed() {
		return nil
	}
	var out []Finding
	if c.Debug { // refused in production by Validate
		out = append(out, Finding{Warning, "APP_DEBUG=true in staging: error pages show stack traces and settings to visitors; keep it off where others can reach the app"})
	}
	if c.Key == "" {
		out = append(out, Finding{Warning, "APP_KEY isn't set: sessions, encryption and signed URLs refuse to start without it; set one (anetos key:generate)"})
	}
	switch u, _ := url.Parse(c.URL); {
	case c.URL == "":
		out = append(out, Finding{Warning, "APP_URL isn't set: links in emails and OAuth callbacks need the app's public URL; set it (https://example.com)"})
	case u.Scheme == "http" && !netaddr.Local(u.Hostname()):
		sev := Warning
		if c.Env.IsProduction() {
			sev = Problem
		}
		out = append(out, Finding{sev, fmt.Sprintf("APP_URL %s is http: links in emails and OAuth callbacks go over plain HTTP; serve the app over HTTPS and use https://", c.URL)})
	case netaddr.Example(u.Hostname()):
		out = append(out, Finding{Warning, fmt.Sprintf("APP_URL %s is an example's: links in emails and OAuth callbacks would point there; set the app's public URL", c.URL)})
	}
	return out
}
