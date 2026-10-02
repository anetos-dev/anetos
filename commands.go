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
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"

	"anetos.dev/anetos/cmd"
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
	if _, dup := a.commands[c.Name]; dup {
		return fmt.Errorf("anetos: command %q registered twice", c.Name)
	}
	a.commands[c.Name] = c
	return nil
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
	if args[0] == "help" || isHelpFlag(args[0]) {
		return a.help(bin, args[1:], stdout, stderr)
	}
	a.cmdMu.Lock()
	c, ok := a.commands[args[0]]
	a.cmdMu.Unlock()
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q\n\n", bin, args[0])
		a.printCommands(bin, stderr)
		return 2
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
	a.cmdMu.Lock()
	c, ok := a.commands[args[0]]
	a.cmdMu.Unlock()
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
	fmt.Fprintf(w, "\nRun %q for a command's arguments.\n", bin+" help <command>")
}
