// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"

	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/internal/cmdname"
)

// AddCommand registers a command of the application binary (see
// [App.Execute]). It returns an error if the command is invalid or its
// name is taken.
func (a *App) AddCommand(c cmd.Command) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Name == "help" {
		return errors.New(`anetos: the command name "help" is reserved`)
	}
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()
	for _, name := range append([]string{c.Name}, c.Former...) {
		if name == "help" {
			return fmt.Errorf("anetos: command %q: the name \"help\" is reserved", c.Name)
		}
		if a.taken(name) {
			return fmt.Errorf("anetos: command %q registered twice", name)
		}
	}
	a.commands[c.Name] = c
	for _, f := range c.Former {
		a.former[f] = c.Name
	}
	return nil
}

// taken reports whether name is a command's name or former name. The
// caller holds cmdMu.
func (a *App) taken(name string) bool {
	_, isCmd := a.commands[name]
	_, former := a.former[name]
	return isCmd || former
}

// expand returns the command name means: itself when it is a command, a
// former name or a flag, else the only command whose parts each start
// with name's ("r:l" for route:list), or the candidates when several do.
func (a *App) expand(name string) (string, []string) {
	if strings.HasPrefix(name, "-") {
		return name, nil
	}
	a.cmdMu.Lock()
	names := []string{"help"}
	for n := range a.commands {
		names = append(names, n)
	}
	_, former := a.former[name]
	a.cmdMu.Unlock()
	if former {
		return name, nil
	}
	if full, candidates := cmdname.Match(names, name); full != "" || len(candidates) > 0 {
		return full, candidates
	}
	return name, nil
}

// lookup returns the command name or a former name runs, and whether
// name is a former one.
func (a *App) lookup(name string) (c cmd.Command, ok, former bool) {
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()
	if c, ok = a.commands[name]; ok {
		return c, true, false
	}
	if n, isFormer := a.former[name]; isFormer {
		c, ok = a.commands[n]
		return c, ok, true
	}
	return cmd.Command{}, false, false
}

// Command registers a custom command, run with the app booted:
//
//	app.Command("reports:send", "Email the weekly report", func(ctx context.Context, args *cmd.Args) error {
//		return reports.Send(ctx)
//	})
//
// It panics if the name is invalid or taken, which is a programming error.
func (a *App) Command(name, description string, run func(ctx context.Context, args *cmd.Args) error) {
	if err := a.AddCommand(cmd.Command{Name: name, Description: description, Run: run}); err != nil {
		panic(err)
	}
}

// Commands returns the registered commands, built-in ones included, sorted
// by name.
func (a *App) Commands() []cmd.Command {
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()
	out := make([]cmd.Command, 0, len(a.commands))
	for _, c := range a.commands {
		out = append(out, c)
	}
	slices.SortFunc(out, func(x, y cmd.Command) int { return strings.Compare(x.Name, y.Name) })
	return out
}

func (a *App) addBuiltins() {
	a.commands["doctor"] = a.doctorCommand()
	a.commands["version"] = cmd.Command{
		Name:        "version",
		Description: "Print the app's version, its commit, and the Anetos and Go versions it was built with",
		ManagesApp:  true, // nothing to boot
		Run: func(_ context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("version", flag.ContinueOnError)
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 0 {
				return cmd.Usagef("unexpected argument %q", fs.Arg(0))
			}
			fmt.Fprint(args.Stdout, VersionText())
			return nil
		},
	}
	a.commands["run"] = cmd.Command{
		Name:        "run",
		Usage:       "[--only=role,…]",
		Description: "Run the application's components (the default command)",
		ManagesApp:  true,
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("run", flag.ContinueOnError)
			only := fs.String("only", "", "run only the components with these comma-separated roles, e.g. http")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 0 {
				return cmd.Usagef("unexpected argument %q", fs.Arg(0))
			}
			var roles []string
			for r := range strings.SplitSeq(*only, ",") {
				if r = strings.TrimSpace(r); r != "" {
					roles = append(roles, r)
				}
			}
			return a.Run(ctx, roles...)
		},
	}
}

// Execute runs the command named by the program's arguments and exits:
//
//	func main() {
//		app, err := anetos.New()
//		…
//		app.Execute()
//	}
//
// With no arguments (or only flags) it runs the "run" command, which
// starts every component. "help" lists the commands. The context is
// canceled on SIGINT and SIGTERM. The exit status is 0 on success, 1 on
// errors and 2 for usage errors.
func (a *App) Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop() // a second signal ends the program
	}()
	code := a.ExecuteArgs(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// ExecuteArgs is [App.Execute] with explicit arguments and output, for
// tests. It returns the exit status.
func (a *App) ExecuteArgs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	bin := filepath.Base(os.Args[0])
	if len(args) == 0 || strings.HasPrefix(args[0], "-") && !isHelpFlag(args[0]) {
		args = append([]string{"run"}, args...)
	}
	name, candidates := a.expand(args[0])
	if len(candidates) > 0 {
		fmt.Fprintf(stderr, "%s: %q could be %s\n", bin, args[0], strings.Join(candidates, ", "))
		return 2
	}
	if name == "help" || isHelpFlag(name) {
		return a.help(bin, args[1:], stdout, stderr)
	}
	c, ok, former := a.lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q\n\n", bin, args[0])
		a.printCommands(bin, stderr)
		return 2
	}
	if former {
		fmt.Fprintf(stderr, "%s: %s is now %s; the old name will be removed\n", bin, args[0], c.Name)
	}
	if len(args) > 1 && isHelpFlag(args[1]) {
		printUsage(stdout, bin, c) // without running the command
		return 0
	}
	ctx = cmd.WithCommand(ctx, c) // for boot checks (cmd.Running)
	err := a.runCommand(ctx, c, &cmd.Args{Name: c.Name, Args: args[1:], Stdout: stdout, Stderr: stderr})
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, cmd.ErrUsage):
		fmt.Fprintf(stderr, "%s %s: %v\n", bin, c.Name, err)
		printUsage(stderr, bin, c)
		return 2
	case c.ManagesApp && errors.Is(err, context.Canceled) && ctx.Err() != nil:
		return 0 // the app was stopped by a signal, as it should be
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", bin, c.Name, err)
	return 1
}

func printUsage(w io.Writer, bin string, c cmd.Command) {
	fmt.Fprintf(w, "Usage: %s\n", strings.TrimSpace(bin+" "+c.Name+" "+c.Usage))
	if c.Description != "" {
		fmt.Fprintf(w, "\n%s\n", c.Description)
	}
}

func (a *App) runCommand(ctx context.Context, c cmd.Command, args *cmd.Args) error {
	if c.ManagesApp {
		return c.Run(ctx, args)
	}
	if err := a.Boot(ctx); err != nil {
		return err
	}
	defer func() {
		if v := recover(); v != nil {
			_ = a.Close() // release resources before the panic ends the program
			panic(v)
		}
	}()
	err := c.Run(a.Context(ctx), args)
	return errors.Join(err, a.Close())
}

func isHelpFlag(s string) bool { return s == "-h" || s == "-help" || s == "--help" }

func (a *App) help(bin string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stdout, "Usage: %s [command] [arguments]\n\n", bin)
		a.printCommands(bin, stdout)
		return 0
	}
	name, candidates := a.expand(args[0])
	if len(candidates) > 0 {
		fmt.Fprintf(stderr, "%s: %q could be %s\n", bin, args[0], strings.Join(candidates, ", "))
		return 2
	}
	c, ok, _ := a.lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q\n", bin, args[0])
		return 2
	}
	printUsage(stdout, bin, c)
	return 0
}

func (a *App) printCommands(bin string, w io.Writer) {
	fmt.Fprintln(w, "Commands:")
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "  help\tShow this list, or a command's usage (help <command>)\n")
	for _, c := range a.Commands() {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name, c.Description)
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\nRun %q for a command's arguments. A name may be shortened while it\nstays unique: r:l is route:list.\n", bin+" help <command>")
}

// buildVersion is the app's version given to anetos build --version
// (with -ldflags=-X), for builds without the repository's history (in a
// container, say); it wins over the one Go records.
var buildVersion string

// VersionText describes the binary, as the version command prints it:
// the app's version (anetos build --version, else the one Go records
// from git), the commit, the Anetos and Go versions. It needs no
// settings, so main can print it before setting the app up:
//
//	if len(os.Args) == 2 && os.Args[1] == "version" {
//		fmt.Print(anetos.VersionText())
//		return
//	}
//
// It prints:
//
//	blog v1.2.0 (commit 1a2b3c4d5e6f, 2026-10-06T10:00:00Z)
//	Anetos v0.3.0
//	go1.26.8 linux/amd64
func VersionText() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown (no build information)\nAnetos " + Version() + "\n" + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
	}
	name := path.Base(bi.Main.Path)
	if len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" && path.Dir(bi.Main.Path) != "." {
		name = path.Base(path.Dir(bi.Main.Path)) // example.com/blog/v2: blog
	}
	if name == "" || name == "." {
		name = "app"
	}
	v := bi.Main.Version
	if buildVersion != "" {
		v = buildVersion
	} else if v == "" {
		v = "(devel)"
	}
	var rev, at string
	modified := false
	goos, goarch := runtime.GOOS, runtime.GOARCH
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		case "GOOS":
			goos = s.Value
		case "GOARCH":
			goarch = s.Value
		}
	}
	var details []string
	if rev != "" {
		details = append(details, "commit "+rev[:min(len(rev), 12)])
	}
	if at != "" {
		details = append(details, at)
	}
	if modified {
		details = append(details, "modified")
		v = strings.TrimSuffix(v, "+dirty") // Go's mark of the same
	}
	line := name + " " + v
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	return line + "\nAnetos " + Version() + "\n" + bi.GoVersion + " " + goos + "/" + goarch + "\n"
}
