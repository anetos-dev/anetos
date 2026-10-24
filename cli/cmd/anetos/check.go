// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"anetos.dev/anetos/cli/internal/devserver"
	"anetos.dev/anetos/cli/internal/tailwind"
)

const checkUsage = `Usage: anetos check [--short] [--json]

Checks that the project works, in one command, for you or a coding agent
before calling a change done. In order, stopping at the first failure:

  generate  templ's views, the models' typed columns (anetos generate),
            and a Tailwind CSS project's stylesheet (downloading the
            tailwindcss binary on first use; offline, a note)
  build     go build ./...
  vet       go vet ./...
  test      go test ./... (--short: go test -short ./...)
  doctor    anetos doctor: the project's files and the app's settings

It writes generated code, as anetos dev does; it changes nothing else.
Exits 1 when a step fails. --json prints one object: {"ok": …, "steps":
[{"step", "ok", "seconds", "output"}…]}, with each step's output (and
"error" when check couldn't start).

Without a .env (a fresh clone, an agent's sandbox, CI), the doctor step
checks the project's files only, and says how to write one:
cp .env.example .env && go tool anetos key:generate.
`

// checkStep is one step of anetos check, as --json prints it.
type checkStep struct {
	Step    string  `json:"step"`
	OK      bool    `json:"ok"`
	Seconds float64 `json:"seconds"`
	Output  string  `json:"output,omitempty"`
}

// maxStepOutput bounds a step's output in the report: a test run's can
// be long, and its end says what failed.
const maxStepOutput = 16 << 10

// check runs anetos check.
func check(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos check", flag.ContinueOnError)
	short := fs.Bool("short", false, "run go test -short")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	pos, code := parse(fs, args, stderr, checkUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fs.Usage()
		return 2
	}
	root, err := projectRoot()
	if err != nil {
		if *asJSON {
			writeCheckJSON(stdout, false, err.Error(), []checkStep{})
		}
		fmt.Fprintln(stderr, "anetos check:", err)
		return 1
	}
	testArgs := []string{"test", "./..."}
	if *short {
		testArgs = []string{"test", "-short", "./..."}
	}
	steps := []struct {
		name string
		run  func(out io.Writer) error
	}{
		{"generate", func(out io.Writer) error { return checkGenerate(ctx, root, out) }},
		{"build", func(out io.Writer) error { return runGoOut(ctx, root, out, out, "build", "./...") }},
		{"vet", func(out io.Writer) error { return runGoOut(ctx, root, out, out, "vet", "./...") }},
		{"test", func(out io.Writer) error { return runGoOut(ctx, root, out, out, testArgs...) }},
		{"doctor", func(out io.Writer) error {
			var o doctorOptions
			if _, err := os.Stat(filepath.Join(root, ".env")); err != nil && os.Getenv("APP_KEY") == "" {
				o.projectOnly = "note: the app's settings aren't checked: there's no .env (a fresh clone?). Write one: cp .env.example .env && go tool anetos key:generate"
			}
			if runDoctor(ctx, root, o, out, out) != 0 {
				return errors.New("doctor found a problem")
			}
			return nil
		}},
	}
	var report []checkStep
	ok := true
	for _, s := range steps {
		if !*asJSON {
			fmt.Fprintf(stdout, "%-9s", s.name)
		}
		var out bytes.Buffer
		start := time.Now()
		err := s.run(&out)
		st := checkStep{Step: s.name, OK: err == nil, Seconds: math.Round(time.Since(start).Seconds()*100) / 100}
		text := strings.TrimSpace(out.String())
		if err != nil && !strings.Contains(text, err.Error()) {
			text = strings.TrimSpace(text + "\n" + err.Error())
		}
		text = tailText(text, maxStepOutput)
		st.Output = text
		report = append(report, st)
		if !*asJSON {
			if st.OK {
				fmt.Fprintf(stdout, "ok (%.1fs)\n", st.Seconds)
				if s.name == "doctor" {
					if w := warningLines(text); w != "" {
						fmt.Fprintln(stdout, indent(w))
					}
				}
			} else {
				fmt.Fprintf(stdout, "FAILED (%.1fs)\n%s\n", st.Seconds, indent(text))
			}
		}
		if err != nil {
			ok = false
			break
		}
	}
	switch {
	case *asJSON:
		writeCheckJSON(stdout, ok, "", report)
	case ok:
		fmt.Fprintln(stdout, "All checks passed.")
	default:
		fmt.Fprintln(stdout, "Fix the failure above, then run go tool anetos check again.")
	}
	if !ok {
		return 1
	}
	return 0
}

// checkGenerate brings the generated code up to date, as anetos dev
// does: templ, the typed columns, and a Tailwind CSS project's
// stylesheet (when the tailwindcss binary is at hand; without it, the
// stylesheet is left as it is and the output says so).
func checkGenerate(ctx context.Context, root string, out io.Writer) error {
	if err := devserver.Generate(ctx, root); err != nil {
		return err
	}
	if !tailwind.Uses(root) {
		return nil
	}
	bin, err := tailwind.Binary(ctx, func(string, ...any) {})
	if err != nil {
		fmt.Fprintf(out, "note: public/static/app.css not rebuilt: %v\n", err)
		return nil
	}
	_, err = tailwind.Build(ctx, bin, root)
	return err
}

// tailText is text's last n bytes or fewer, whole characters, after
// "…" when it's cut.
func tailText(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := len(text) - n
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return "…" + text[cut:]
}

// writeCheckJSON prints check's report as --json does.
func writeCheckJSON(w io.Writer, ok bool, errText string, steps []checkStep) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(struct {
		OK    bool        `json:"ok"`
		Error string      `json:"error,omitempty"`
		Steps []checkStep `json:"steps"`
	}{ok, errText, steps})
}

func indent(s string) string {
	if s == "" {
		return s
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// warningLines are doctor's warning and note lines in its output.
func warningLines(s string) string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "warning") || strings.HasPrefix(t, "note:") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return strings.Join(out, "\n")
}
