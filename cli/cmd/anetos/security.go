// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"anetos.dev/anetos/cli/internal/secscan"
)

const securityUsage = `Usage: anetos security:check [--level=basic|advanced] [--format=text|json|sarif|markdown] [--out=FILE] [--ignore=FILE] [--strict] [--no-vuln]

Checks the project's code: request data that reaches SQL, HTML,
redirects, commands and files, certificates not checked, math/rand for
secrets, secrets written in the files, and (govulncheck, built in) known
vulnerabilities in the code the app calls. Then builds the app and runs
its security:check command in the project, which adds these findings to
its own: its settings and routes checked against security rules (CSRF,
logins on admin routes, rate limits, cookies, TLS, debug endpoints…).
One report, one ignore file. The flags are the app's, but --no-vuln,
which skips govulncheck (it needs the network for vuln.go.dev); see go
tool anetos docs guides/security-check.

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
	args = slices.DeleteFunc(args, func(a string) bool {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		return strings.HasPrefix(a, "-") && name == "no-vuln"
	})
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
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
