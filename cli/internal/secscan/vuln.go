// SPDX-License-Identifier: Apache-2.0

package secscan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	vulnscan "golang.org/x/vuln/scan"
)

// Govulncheck runs govulncheck (golang.org/x/vuln, built in) on the
// project in dir with args, its output going to stdout, as doctor --vuln
// does.
func Govulncheck(ctx context.Context, dir string, stdout, stderr io.Writer, args ...string) error {
	cmd := vulnscan.Command(ctx, append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Stdin = bytes.NewReader(nil)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Wait()
}

// vulnMessage is a message of govulncheck's JSON stream (the fields
// read).
type vulnMessage struct {
	OSV *struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
	} `json:"osv"`
	Finding *struct {
		OSV          string       `json:"osv"`
		FixedVersion string       `json:"fixed_version"`
		Trace        []*vulnFrame `json:"trace"`
	} `json:"finding"`
}

type vulnFrame struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Package  string `json:"package"`
	Function string `json:"function"`
	Receiver string `json:"receiver"`
	Position *struct {
		Filename string `json:"filename"`
		Line     int    `json:"line"`
	} `json:"position"`
}

// vulns runs govulncheck and makes its findings the report's: a
// vulnerable function the app calls is a problem, at the app's call; a
// vulnerable package it imports without calling the function, a note.
// A module it only requires isn't reported. When govulncheck can't run
// (offline), a note says so.
func vulns(ctx context.Context, dir string) []Finding {
	ctx, cancel := context.WithTimeout(ctx, vulnTimeout)
	defer cancel()
	var out, errOut bytes.Buffer
	if err := Govulncheck(ctx, dir, &out, &errOut, "-format", "json", "./..."); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		switch {
		case ctx.Err() != nil:
			return []Finding{{Rule: "dependency.vuln", Severity: "note", Where: "govulncheck",
				Message: fmt.Sprintf("the known vulnerabilities weren't checked: govulncheck didn't finish in %v (a network that drops the connection?); --no-vuln skips it", vulnTimeout)}}
		case networkError.MatchString(msg):
			return []Finding{{Rule: "dependency.vuln", Severity: "note", Where: "govulncheck",
				Message: "the known vulnerabilities weren't checked (" + msg + "): it needs the network for vuln.go.dev; run go tool anetos security:check where it has it, or --no-vuln"}}
		}
		return []Finding{{Rule: "dependency.vuln", Severity: "warning", Where: "govulncheck",
			Message: "govulncheck failed, so the known vulnerabilities weren't checked: " + msg}}
	}
	return vulnFindings(&out, dir)
}

// vulnTimeout bounds govulncheck: its HTTP client has no timeout.
const vulnTimeout = 5 * time.Minute

// networkError matches govulncheck's errors that come from the network.
var networkError = regexp.MustCompile(`(?i)fetching vulnerabilities|dial tcp|no such host|connection refused|proxyconnect|timeout|tls:|network is unreachable|i/o timeout`)

// vulnFindings reads govulncheck's JSON stream.
func vulnFindings(r io.Reader, dir string) []Finding {
	main := mainModule(dir)
	summaries := map[string]string{}
	type key = vulnKey
	stdlib := map[key]bool{}
	called := map[key]Finding{}
	imported := map[key]Finding{}
	var order []key
	dec := json.NewDecoder(r)
	for {
		var m vulnMessage
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m.OSV != nil {
			summaries[m.OSV.ID] = m.OSV.Summary
		}
		f := m.Finding
		if f == nil || len(f.Trace) == 0 || f.Trace[0].Package == "" {
			continue // a module the app requires, without importing the package
		}
		vuln := f.Trace[0]
		k := key{f.OSV, vuln.Package}
		stdlib[k] = vuln.Module == "stdlib"
		if vuln.Function == "" {
			if _, ok := imported[k]; !ok {
				imported[k] = Finding{Rule: "dependency.vuln", Severity: "note", Where: f.OSV + " " + vuln.Package,
					Message: fmt.Sprintf("%s: the app imports %s, which has it, but doesn't call the vulnerable code; %s", f.OSV, vuln.Package, fix(vuln, f.FixedVersion))}
				order = append(order, k)
			}
			continue
		}
		if _, ok := called[k]; ok {
			continue
		}
		symbol := vuln.Function
		if vuln.Receiver != "" {
			symbol = strings.TrimPrefix(vuln.Receiver, "*") + "." + symbol
		}
		fd := Finding{Rule: "dependency.vuln", Severity: "problem", Where: f.OSV + " " + vuln.Package,
			Message: fmt.Sprintf("%s: the app calls %s.%s, which has it; %s", f.OSV, vuln.Package, symbol, fix(vuln, f.FixedVersion))}
		// The app's frame nearest the vulnerable symbol: the line of its
		// code that leads to it (filenames are relative to their module).
		for _, fr := range f.Trace[1:] {
			if fr.Module == main && fr.Position != nil && fr.Position.Line > 0 {
				fd.File, fd.Line = filepath.ToSlash(fr.Position.Filename), fr.Position.Line
				break
			}
		}
		called[k] = fd
		order = append(order, k)
	}
	var findings []Finding
	seen := map[key]bool{}
	for _, k := range order {
		if seen[k] {
			continue
		}
		seen[k] = true
		f, ok := called[k]
		if !ok {
			f = imported[k]
		}
		if s := summaries[k.osv]; s != "" {
			f.Message = strings.Replace(f.Message, k.osv+":", k.osv+" ("+strings.TrimSuffix(s, ".")+"):", 1)
		}
		if f.File == "" {
			f.File, f.Line = "go.mod", goModLine(dir, k, stdlib[k])
		}
		findings = append(findings, f)
	}
	return findings
}

// fix says how to get a vulnerability's fix.
func fix(fr *vulnFrame, fixed string) string {
	switch {
	case fixed == "":
		return "no fixed version yet: see whether you can avoid the code"
	case fr.Module == "stdlib":
		return "build with Go " + strings.TrimPrefix(strings.TrimPrefix(fixed, "v"), "go") + " or later"
	}
	return fmt.Sprintf("update it: go get %s@%s (it has %s)", fr.Module, fixed, fr.Version)
}

// vulnKey is a vulnerability in a package.
type vulnKey struct{ osv, pkg string }

// mainModule is the path of the module in dir.
func mainModule(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`)
		}
	}
	return ""
}

// goModLine is the line of go.mod that requires the vulnerable module
// (for the standard library, its toolchain or go line), else 1.
func goModLine(dir string, k vulnKey, stdlib bool) int {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return 1
	}
	lines := strings.Split(string(b), "\n")
	if stdlib {
		line := 1
		for i, l := range lines {
			if f := strings.Fields(l); len(f) == 2 && (f[0] == "go" && line == 1 || f[0] == "toolchain") {
				line = i + 1
			}
		}
		return line
	}
	best, bestLen := 1, 0
	for i, line := range lines {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "require "))
		if len(f) < 2 {
			continue
		}
		mod := f[0]
		if (k.pkg == mod || strings.HasPrefix(k.pkg, mod+"/")) && len(mod) > bestLen {
			best, bestLen = i+1, len(mod)
		}
	}
	return best
}
