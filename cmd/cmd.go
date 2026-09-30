// SPDX-License-Identifier: Apache-2.0

// Package cmd defines the commands of an application binary: the
// built-in ones (run, serve, migrate, routes:list, …) and your own.
// Register them on the app and let it dispatch os.Args:
//
//	app.Command("reports:send", "Email the weekly report", func(ctx context.Context, args *cmd.Args) error {
//		fs := flag.NewFlagSet("reports:send", flag.ContinueOnError)
//		dry := fs.Bool("dry-run", false, "print instead of sending")
//		if err := args.Parse(fs); err != nil {
//			return err
//		}
//		…
//	})
//	app.Execute() // ./app reports:send --dry-run
//
// A command runs with the app booted, and the app is closed after it
// returns. The context is canceled on SIGINT or SIGTERM; a second signal
// ends the program.
package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
)

// Command is a named action of the application binary.
type Command struct {
	// Name is how the command is invoked: "migrate:rollback".
	Name string
	// Usage lists the arguments, for help: "[--step=N] [--force]".
	Usage string
	// Description is one line for the command list.
	Description string
	// Run does the work. Return an error matching [ErrUsage] (from
	// [Args.Parse] or [Usagef]) for bad arguments (exit status 2); other
	// errors exit with status 1.
	Run func(ctx context.Context, args *Args) error
	// ManagesApp says Run boots, runs and stops the app itself, as run and
	// serve do. Other commands run between Boot and Close.
	ManagesApp bool
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*(:[a-z][a-z0-9-]*)*$`)

// Validate reports whether c can be registered.
func (c Command) Validate() error {
	if !nameRe.MatchString(c.Name) {
		return fmt.Errorf("cmd: invalid command name %q (use lowercase words separated by : or -, like reports:send)", c.Name)
	}
	if c.Run == nil {
		return fmt.Errorf("cmd: command %q has no Run function", c.Name)
	}
	return nil
}

// Args is a command's invocation.
type Args struct {
	// Name is the command's name.
	Name string
	// Args are the arguments after the name.
	Args []string
	// Stdout and Stderr are the command's output streams.
	Stdout, Stderr io.Writer
}

// ErrUsage matches errors caused by how a command was invoked
// (errors.Is); the binary prints the command's usage and exits with
// status 2.
var ErrUsage = errors.New("usage error")

// usageError is an error message that matches [ErrUsage].
type usageError struct{ err error }

func (e usageError) Error() string        { return e.err.Error() }
func (e usageError) Unwrap() error        { return e.err }
func (e usageError) Is(target error) bool { return target == ErrUsage }

// Parse parses a.Args with fs. It returns an error matching [ErrUsage] for
// bad flags; the binary prints it with the command's usage. For -h after
// other arguments it prints the flags to Stderr and returns
// [flag.ErrHelp] (exit status 0). (-h as the first argument never reaches
// the command: the binary prints the command's usage.)
func (a *Args) Parse(fs *flag.FlagSet) error {
	fs.Init(fs.Name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(a.Args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.Stderr, "Flags:")
			fs.SetOutput(a.Stderr)
			fs.PrintDefaults()
			return err
		}
		return usageError{err}
	}
	return nil
}

// Usagef returns an error matching [ErrUsage] with a formatted message.
func Usagef(format string, args ...any) error {
	return usageError{fmt.Errorf(format, args...)}
}
