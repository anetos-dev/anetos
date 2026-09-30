// SPDX-License-Identifier: Apache-2.0

package cmd_test

import (
	"context"
	"errors"
	"flag"
	"io"
	"testing"

	"anetos.dev/anetos/cmd"
)

func TestValidate(t *testing.T) {
	run := func(context.Context, *cmd.Args) error { return nil }
	for name, ok := range map[string]bool{
		"migrate": true, "migrate:rollback": true, "db:seed": true, "reports:send-weekly": true, "a1:b2": true,
		"": false, "Migrate": false, "a b": false, ":x": false, "x:": false, "x::y": false, "1x": false,
	} {
		if err := (cmd.Command{Name: name, Run: run}).Validate(); (err == nil) != ok {
			t.Errorf("%q: %v", name, err)
		}
	}
	if (cmd.Command{Name: "x"}).Validate() == nil {
		t.Error("nil Run accepted")
	}
}

func TestParse(t *testing.T) {
	a := &cmd.Args{Args: []string{"--n=3", "rest"}, Stdout: io.Discard, Stderr: io.Discard}
	fs := flag.NewFlagSet("x", flag.ExitOnError) // Parse switches it to ContinueOnError
	n := fs.Int("n", 0, "")
	if err := a.Parse(fs); err != nil || *n != 3 || fs.Arg(0) != "rest" {
		t.Errorf("%v %d %v", err, *n, fs.Args())
	}
	a.Args = []string{"--nope"}
	err := a.Parse(flag.NewFlagSet("x", flag.ContinueOnError))
	if !errors.Is(err, cmd.ErrUsage) || err.Error() != "flag provided but not defined: -nope" {
		t.Errorf("bad flag: %v", err)
	}
	if err := cmd.Usagef("no %s", "report"); !errors.Is(err, cmd.ErrUsage) || err.Error() != "no report" {
		t.Errorf("Usagef: %v", err)
	}
}
