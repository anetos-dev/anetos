// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
)

const doctorUsage = `Usage: anetos doctor [--strict] [--vuln]

Checks the project, then builds the app and runs its doctor command,
which checks its settings (.env and the environment). The project's
checks: .env's permissions, and that no file of settings with secrets
(.env, production.env…) is in git; --vuln runs govulncheck too. Exits 1
on problems.

The settings checked are this machine's, usually development ones: run
the app's doctor where the production settings are too
(./<app> doctor on the server).
`

// doctor runs anetos doctor.
func doctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos doctor", flag.ContinueOnError)
	strict := fs.Bool("strict", false, "exit 1 on warnings too")
	vuln := fs.Bool("vuln", false, "run govulncheck ./... (it must be installed)")
	pos, code := parse(fs, args, stderr, doctorUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fs.Usage()
		return 2
	}
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(stderr, "anetos doctor:", err)
		return 1
	}
	found := projectChecks(ctx, root)
	core := moduleVersion(ctx, root, corePath)
	if core == "" {
		core = "not a dependency"
	}
	fmt.Fprintf(stdout, "Checking the project (Anetos %s, %s).\n", core, goVersion(ctx, root))
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	problems, warnings := 0, 0
	for _, f := range found {
		switch f.severity {
		case "problem":
			problems++
		case "warning":
			warnings++
		}
		if f.severity == "ok" {
			fmt.Fprintf(tw, "  ok\t%s\n", f.name)
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s: %s\n", f.severity, f.name, f.message)
	}
	_ = tw.Flush()
	fail := problems > 0 || *strict && warnings > 0

	if *vuln {
		fmt.Fprintln(stdout, "\nRunning govulncheck ./...")
		if err := govulncheck(ctx, root, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "anetos doctor:", err)
			fail = true
		}
	}

	fmt.Fprintln(stdout)
	if core == "not a dependency" {
		fmt.Fprintln(stdout, "The app's settings aren't checked: it doesn't use Anetos.")
		if fail {
			return 1
		}
		return 0
	}
	tmp, err := os.MkdirTemp("", "anetos-doctor-")
	if err != nil {
		fmt.Fprintln(stderr, "anetos doctor:", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "app")
	if err := runGo(ctx, root, stderr, "build", "-o", bin, "."); err != nil {
		fmt.Fprintln(stderr, "anetos doctor: the app doesn't build:", err)
		return 1
	}
	appArgs := []string{"doctor"}
	if *strict {
		appArgs = append(appArgs, "--strict")
	}
	var appErr bytes.Buffer
	if err := runApp(ctx, root, bin, stdout, io.MultiWriter(stderr, &appErr), appArgs...); err != nil {
		if strings.Contains(appErr.String(), `unknown command "doctor"`) {
			fmt.Fprintln(stdout, "The app's settings aren't checked: its version of Anetos has no doctor command (v0.3 has).")
		} else {
			fail = true // the app printed why
		}
	}
	if fail {
		return 1
	}
	return 0
}

type projectFinding struct{ severity, name, message string }

// projectChecks checks the project's files: .env's permissions, and
// files of secrets in git.
func projectChecks(ctx context.Context, root string) []projectFinding {
	var out []projectFinding
	ok := func(name string) { out = append(out, projectFinding{"ok", name, ""}) }
	add := func(sev, name, msg string) { out = append(out, projectFinding{sev, name, msg}) }

	envFile := filepath.Join(root, ".env")
	switch info, err := os.Stat(envFile); {
	case errors.Is(err, os.ErrNotExist):
		ok(".env")
	case err != nil:
		add("warning", ".env", err.Error())
	case runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0:
		add("warning", ".env", fmt.Sprintf("other users of this machine can read it (%v): chmod 600 .env", info.Mode().Perm()))
	default:
		ok(".env")
	}

	if _, err := exec.LookPath("git"); err != nil {
		return out
	}
	var tracked bytes.Buffer
	c := exec.CommandContext(ctx, "git", "ls-files", "-z")
	c.Dir, c.Stdout = root, &tracked
	if c.Run() != nil {
		return out // not a repository
	}
	secrets := 0
	envTracked := false
	for name := range strings.SplitSeq(tracked.String(), "\x00") {
		envTracked = envTracked || name == ".env"
		if secretFile(name) {
			secrets++
			add("problem", "git", fmt.Sprintf("%s is in git, with its secrets in the history for whoever has the repository: git rm --cached %s, add it to .gitignore, and change the secrets it held", name, name))
		}
	}
	if _, err := os.Stat(envFile); err == nil && !envTracked { // a tracked one is reported above
		c := exec.CommandContext(ctx, "git", "check-ignore", "-q", ".env")
		c.Dir = root
		if err := c.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				secrets++
				add("warning", "git", ".env isn't ignored: add it to .gitignore before a commit takes it")
			}
		}
	}
	if secrets == 0 {
		ok("git")
	}
	return out
}

// secretFile reports whether a tracked file looks like one of settings
// with secrets: .env, .env.production, deploy/production.env; not the
// examples, nor the .env.testing that anetos new writes to be committed.
func secretFile(name string) bool {
	base := filepath.Base(name)
	if strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".dist") ||
		base == ".env.testing" { // the test settings anetos new commits, without secrets
		return false
	}
	return base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env")
}

// goVersion returns the go command's version ("go1.26.8"), or "".
func goVersion(ctx context.Context, root string) string {
	var out bytes.Buffer
	if err := runGoOut(ctx, root, &out, io.Discard, "env", "GOVERSION"); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// govulncheck runs govulncheck ./... in dir: the known vulnerabilities
// of the code the app calls, in its modules and in Go's standard
// library.
func govulncheck(ctx context.Context, dir string, stdout, stderr io.Writer) error {
	path, err := exec.LookPath("govulncheck")
	if err != nil {
		return errors.New("govulncheck isn't installed: go install golang.org/x/vuln/cmd/govulncheck@latest")
	}
	c := exec.CommandContext(ctx, path, "./...")
	c.Dir = dir
	c.Stdout, c.Stderr = stdout, stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("govulncheck: %w", err)
	}
	return nil
}
