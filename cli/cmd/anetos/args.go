// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
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
			fs.PrintDefaults()
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
