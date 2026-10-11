// SPDX-License-Identifier: Apache-2.0

// Command release prepares a release of the repository's modules, and
// rehearses one (docs/contributing/releasing.md):
//
//	release prepare [-root dir] [-locales dir] vX.Y.Z
//	release rehearse [-root dir] [-keep] [-stacks web,api]
//
// prepare checks the CHANGELOG sections, sets every requirement of a
// published module to the version, in this repository and in the
// locales' go.mod, and prints the tags to create. rehearse installs the
// committed tree from a module proxy on disk, as users will (make
// release-rehearsal, run by CI).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"anetos.dev/anetos/cli/internal/release"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: release prepare|rehearse [flags] …")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fs := flag.NewFlagSet("release "+args[0], flag.ContinueOnError)
	root := fs.String("root", "..", "the repository's root")
	switch args[0] {
	case "prepare":
		locales := fs.String("locales", "", "the locales repository, whose go.mod gets the version too")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: release prepare [-root dir] [-locales dir] vX.Y.Z")
			return 2
		}
		v := fs.Arg(0)
		if err := release.CheckVersion(v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if err := release.CheckChangelogs(*root, v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var extra []string
		if *locales != "" {
			extra = append(extra, filepath.Join(*locales, "go.mod"))
		}
		changed, err := release.Prepare(*root, v, extra...)
		for _, p := range changed {
			fmt.Println("updated", p)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		mods, err := release.Published(*root)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("\nAfter the release commit, in the repository (then push the commit and the tags):\n\n")
		for _, t := range release.Tags(mods, v) {
			fmt.Println("  git tag", t)
		}
		if *locales != "" {
			fmt.Printf("\nand in %s, after its commit:\n\n  git tag %s\n", *locales, v)
		}
		return 0
	case "rehearse":
		keep := fs.Bool("keep", false, "keep the temporary directory")
		stacks := fs.String("stacks", "web,api", "the stacks of anetos new to try")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "usage: release rehearse [-root dir] [-keep] [-stacks web,api]")
			return 2
		}
		r := release.Rehearsal{Root: *root, Out: os.Stdout, Keep: *keep, Stacks: strings.Split(*stacks, ",")}
		if err := r.Run(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "release: unknown command %q (prepare, rehearse)\n", args[0])
		return 2
	}
}
