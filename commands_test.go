// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
)

type bootProvider struct{ booted *bool }

func (bootProvider) Name() string               { return "boot" }
func (bootProvider) Register(*anetos.App) error { return nil }
func (p bootProvider) Boot(context.Context, *anetos.App) error {
	*p.booted = true
	return nil
}

func execute(t *testing.T, app *anetos.App, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := app.ExecuteArgs(t.Context(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCommands(t *testing.T) {
	app := newApp(t, config.Map{})
	var booted, closed bool
	app.Use(bootProvider{&booted})
	app.OnShutdown("mark", func(context.Context) error { closed = true; return nil })
	app.Command("reports:send", "Email the weekly report", func(ctx context.Context, args *cmd.Args) error {
		fs := flag.NewFlagSet("reports:send", flag.ContinueOnError)
		dry := fs.Bool("dry-run", false, "print only")
		if err := args.Parse(fs); err != nil {
			return err
		}
		if !booted {
			return errors.New("not booted")
		}
		switch {
		case fs.Arg(0) == "fail":
			return errors.New("smtp down")
		case fs.Arg(0) == "bad":
			return cmd.Usagef("no such report %q", fs.Arg(0))
		}
		_, _ = args.Stdout.Write([]byte("sent dry=" + map[bool]string{true: "yes", false: "no"}[*dry]))
		return nil
	})
	if code, out, errOut := execute(t, app, "reports:send", "--dry-run"); code != 0 || out != "sent dry=yes" || !closed {
		t.Errorf("run: %d %q %q closed=%v", code, out, errOut, closed)
	}

	newApp2 := func() *anetos.App {
		a := newApp(t, config.Map{})
		a.Command("reports:send", "Email the weekly report", func(ctx context.Context, args *cmd.Args) error {
			switch {
			case len(args.Args) > 0 && args.Args[0] == "fail":
				return errors.New("smtp down")
			case len(args.Args) > 0 && args.Args[0] == "bad":
				return cmd.Usagef("no such report")
			case len(args.Args) > 0 && args.Args[0] == "-h":
				return args.Parse(flag.NewFlagSet("reports:send", flag.ContinueOnError))
			}
			return nil
		})
		return a
	}
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"reports:send", "fail"}, 1, "reports:send: smtp down"},
		{[]string{"reports:send", "bad"}, 2, "no such report"},
		{[]string{"reports:send", "-h"}, 0, "Email the weekly report"},
		{[]string{"reports:send", "--help"}, 0, "Usage: "},
		{[]string{"nope"}, 2, `unknown command "nope"`},
		{[]string{"help"}, 0, "reports:send   Email the weekly report"},
		{[]string{"--help"}, 0, "Commands:"},
		{[]string{"help", "run"}, 0, "[--only=type,…]"},
		{[]string{"help", "nope"}, 2, "unknown command"},
		{[]string{"run", "--only=nope"}, 1, `unknown process type "nope"`},
		{[]string{"run", "extra"}, 2, `unexpected argument "extra"`},
	} {
		a := newApp2() // an app runs one command
		code, out, errOut := execute(t, a, c.args...)
		if code != c.code || !strings.Contains(out+errOut, c.want) {
			t.Errorf("%v: %d %q %q", c.args, code, out, errOut)
		}
	}
}

func TestRunIsTheDefault(t *testing.T) {
	app := newApp(t, config.Map{})
	started := make(chan struct{})
	if err := app.Go("worker", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	}, anetos.ProcessTypes("worker")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()
	var out, errOut bytes.Buffer
	done := make(chan int)
	go func() { done <- app.ExecuteArgs(ctx, nil, &out, &errOut) }()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("code %d: %s", code, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run didn't stop")
	}
}

func TestAddCommandErrors(t *testing.T) {
	app := newApp(t, config.Map{})
	run := func(context.Context, *cmd.Args) error { return nil }
	for _, c := range []cmd.Command{
		{Name: "Bad Name", Run: run},
		{Name: "x:", Run: run},
		{Name: "norun"},
		{Name: "help", Run: run},
		{Name: "run", Run: run},
	} {
		if err := app.AddCommand(c); err == nil {
			t.Errorf("%q accepted", c.Name)
		}
	}
	if err := app.AddCommand(cmd.Command{Name: "db:backup-now", Run: run}); err != nil {
		t.Error(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("duplicate Command didn't panic")
		}
	}()
	app.Command("db:backup-now", "", run)
}

func TestCommandEdgeCases(t *testing.T) {
	ran := false
	newWith := func(run func(ctx context.Context, args *cmd.Args) error) *anetos.App {
		a := newApp(t, config.Map{})
		a.Command("job", "A job", run)
		return a
	}
	// -h never runs the command.
	a := newWith(func(context.Context, *cmd.Args) error { ran = true; return nil })
	if code, out, _ := execute(t, a, "job", "--help"); code != 0 || ran || !strings.Contains(out, "A job") {
		t.Errorf("help ran the command: %d %v %q", code, ran, out)
	}
	// A bad flag is reported once, with the usage.
	a = newWith(func(ctx context.Context, args *cmd.Args) error {
		return args.Parse(flag.NewFlagSet("job", flag.ContinueOnError))
	})
	code, out, errOut := execute(t, a, "job", "--bogus")
	if code != 2 || out != "" || strings.Count(errOut, "bogus") != 1 || !strings.Contains(errOut, "Usage: ") || strings.Contains(errOut, "usage error") {
		t.Errorf("bad flag: %d %q %q", code, out, errOut)
	}
	// An interrupted command is a failure, not a success.
	ctx, cancel := context.WithCancel(t.Context())
	a = newWith(func(ctx context.Context, args *cmd.Args) error {
		cancel()
		<-ctx.Done()
		return errors.Join(errors.New("import stopped half-way"), ctx.Err())
	})
	var o, e bytes.Buffer
	if code := a.ExecuteArgs(ctx, []string{"job"}, &o, &e); code != 1 || !strings.Contains(e.String(), "half-way") {
		t.Errorf("interrupted: %d %q", code, e.String())
	}
	// A panicking command still closes the app.
	closed := false
	a = newWith(func(context.Context, *cmd.Args) error { panic("boom") })
	a.OnShutdown("mark", func(context.Context) error { closed = true; return nil })
	func() {
		defer func() { _ = recover() }()
		execute(t, a, "job")
	}()
	if !closed {
		t.Error("app not closed after a panic")
	}
}

func TestHelpOnlyFirst(t *testing.T) {
	a := newApp(t, config.Map{})
	var got []string
	a.Command("echo", "Echo", func(ctx context.Context, args *cmd.Args) error {
		fs := flag.NewFlagSet("echo", flag.ContinueOnError)
		sep := fs.String("sep", " ", "separator")
		if err := args.Parse(fs); err != nil {
			return err
		}
		got = append([]string{*sep}, fs.Args()...)
		return nil
	})
	if code, _, errOut := execute(t, a, "echo", "--sep", "-h", "--", "-h"); code != 0 || strings.Join(got, "|") != "-h|-h" {
		t.Errorf("%d %q %v", code, errOut, got)
	}
	b := newApp(t, config.Map{})
	b.Command("echo", "Echo", func(ctx context.Context, args *cmd.Args) error {
		fs := flag.NewFlagSet("echo", flag.ContinueOnError)
		fs.String("sep", " ", "separator")
		return args.Parse(fs)
	})
	if code, _, errOut := execute(t, b, "echo", "--sep=,", "-h"); code != 0 || !strings.Contains(errOut, "-sep") {
		t.Errorf("late -h: %d %q", code, errOut)
	}
}

// runningProvider records the command the app boots for.
type runningProvider struct{ seen *string }

func (runningProvider) Name() string               { return "running" }
func (runningProvider) Register(*anetos.App) error { return nil }
func (p runningProvider) Boot(ctx context.Context, _ *anetos.App) error {
	if c, ok := cmd.Running(ctx); ok {
		*p.seen = c.Name + map[bool]string{true: " changes the schema"}[c.ChangesSchema]
	}
	return nil
}

// Boot checks see which command the app boots for.
func TestRunningCommand(t *testing.T) {
	app := newApp(t, config.Map{})
	var seen string
	app.Use(runningProvider{&seen})
	if err := app.AddCommand(cmd.Command{Name: "schema:fix", ChangesSchema: true, Run: func(ctx context.Context, _ *cmd.Args) error {
		if c, ok := cmd.Running(ctx); !ok || c.Name != "schema:fix" {
			return errors.New("the command's context doesn't say which command runs")
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := execute(t, app, "schema:fix"); code != 0 || seen != "schema:fix changes the schema" {
		t.Errorf("%d %q %q, seen %q", code, out, errOut, seen)
	}
	if _, ok := cmd.Running(context.Background()); ok {
		t.Error("Running without a command")
	}
}

func TestVersionCommand(t *testing.T) {
	app := newApp(t, config.Map{})
	var booted bool
	app.Use(bootProvider{&booted})
	code, out, errOut := execute(t, app, "version")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[1], "Anetos v") || !strings.HasPrefix(lines[2], "go1.") {
		t.Fatalf("version: %d %q %q", code, out, errOut)
	}
	if booted {
		t.Error("version booted the app")
	}
	if code, _, _ := execute(t, app, "version", "extra"); code != 2 {
		t.Errorf("an argument: %d", code)
	}
}

func TestFormerCommandNames(t *testing.T) {
	app := newApp(t, config.Map{})
	ran := 0
	if err := app.AddCommand(cmd.Command{Name: "report:send", Former: []string{"reports:send"}, Description: "Send the report", ManagesApp: true,
		Run: func(context.Context, *cmd.Args) error { ran++; return nil }}); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := execute(t, app, "reports:send"); code != 0 || ran != 1 || out != "" || !strings.Contains(errOut, "reports:send is now report:send") {
		t.Errorf("reports:send = %d %q %q, ran %d", code, out, errOut, ran)
	}
	if code, _, errOut := execute(t, app, "report:send"); code != 0 || ran != 2 || errOut != "" {
		t.Errorf("report:send = %d %q", code, errOut)
	}
	if _, out, _ := execute(t, app, "help"); strings.Contains(out, "reports:send") || !strings.Contains(out, "report:send") {
		t.Errorf("help lists a former name:\n%s", out)
	}
	if code, out, _ := execute(t, app, "help", "reports:send"); code != 0 || !strings.Contains(out, "Usage:") || !strings.Contains(out, "report:send") {
		t.Errorf("help reports:send = %d %q", code, out)
	}
	for _, c := range []cmd.Command{
		{Name: "other", Former: []string{"reports:send"}},  // taken as a former name
		{Name: "reports:send"},                             // likewise
		{Name: "another", Former: []string{"report:send"}}, // a command's name
		{Name: "third", Former: []string{"help"}},
		{Name: "fourth", Former: []string{"Bad Name"}},
	} {
		c.Run = func(context.Context, *cmd.Args) error { return nil }
		if err := app.AddCommand(c); err == nil {
			t.Errorf("AddCommand(%s, former %v) = nil", c.Name, c.Former)
		}
	}
}

func TestShortCommandNames(t *testing.T) {
	app := newApp(t, config.Map{})
	var ran []string
	for _, name := range []string{"report:send", "report:list", "reindex"} {
		if err := app.AddCommand(cmd.Command{Name: name, ManagesApp: true, Run: func(_ context.Context, args *cmd.Args) error {
			ran = append(ran, args.Name)
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	for in, want := range map[string]string{"r:s": "report:send", "rep:l": "report:list", "rei": "reindex"} {
		ran = nil
		if code, _, errOut := execute(t, app, in); code != 0 || len(ran) != 1 || ran[0] != want || errOut != "" {
			t.Errorf("%s = %d %v %q, want %s", in, code, ran, errOut, want)
		}
	}
	if code, _, errOut := execute(t, app, "r"); code != 2 || !strings.Contains(errOut, `"r" could be reindex, run`) {
		t.Errorf("r = %d %q", code, errOut)
	}
	if code, out, _ := execute(t, app, "he"); code != 0 || !strings.Contains(out, "Commands:") {
		t.Errorf("he = %d %q", code, out)
	}
	if code, out, _ := execute(t, app, "help", "r:s"); code != 0 || !strings.Contains(out, "report:send") {
		t.Errorf("help r:s = %d %q", code, out)
	}
	if code, _, errOut := execute(t, app, "zz"); code != 2 || !strings.Contains(errOut, `unknown command "zz"`) {
		t.Errorf("zz = %d %q", code, errOut)
	}
}
