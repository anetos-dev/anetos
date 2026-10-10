// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// parse parses flags that may come before or after positional arguments
// (anetos new blog --db=postgres) and returns the positional ones. It
// returns the exit status to use when parsing ends the command: 0 for -h,
// 2 for bad flags, -1 to go on.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, usage string) ([]string, int) {
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		if hasFlags(fs) {
			fmt.Fprintln(stderr, "\nFlags:")
			printFlags(stderr, fs)
		}
	}
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, 0
			}
			return nil, 2
		}
		if fs.NArg() == 0 {
			return positional, -1
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func hasFlags(fs *flag.FlagSet) bool {
	n := 0
	fs.VisitAll(func(*flag.Flag) { n++ })
	return n > 0
}

// printFlags prints fs's flags as flag.PrintDefaults does, with two
// dashes before names longer than a letter (--check, -o), as the help
// texts write them; Go's flag package accepts either.
func printFlags(w io.Writer, fs *flag.FlagSet) {
	fs.VisitAll(func(f *flag.Flag) {
		dash := "--"
		if len(f.Name) == 1 {
			dash = "-"
		}
		name, usage := flag.UnquoteUsage(f)
		line := "  " + dash + f.Name
		if name != "" {
			line += " " + name
		}
		line += "\n    \t" + strings.ReplaceAll(usage, "\n", "\n    \t")
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
			if name == "string" {
				line += fmt.Sprintf(" (default %q)", f.DefValue)
			} else {
				line += " (default " + f.DefValue + ")"
			}
		}
		fmt.Fprintln(w, line)
	})
}
