// SPDX-License-Identifier: Apache-2.0

package securitycheck

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Severity says how much a finding matters, as anetos.Severity (the same
// values).
type Severity int

// The severities.
const (
	Note Severity = iota
	Warning
	Problem
)

// String returns "note", "warning" or "problem".
func (s Severity) String() string {
	switch s {
	case Note:
		return "note"
	case Warning:
		return "warning"
	case Problem:
		return "problem"
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

// MarshalText returns its name.
func (s Severity) MarshalText() ([]byte, error) {
	if s < Note || s > Problem {
		return nil, fmt.Errorf("no severity %d", int(s))
	}
	return []byte(s.String()), nil
}

// UnmarshalText reads a name MarshalText wrote.
func (s *Severity) UnmarshalText(b []byte) error {
	for v := Note; v <= Problem; v++ {
		if string(b) == v.String() {
			*s = v
			return nil
		}
	}
	return fmt.Errorf("%q isn't a severity (note, warning or problem)", b)
}

// Finding is a finding of the report, as anetos.SecurityFinding.
type Finding struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Where    string   `json:"where"`
	File     string   `json:"file,omitempty"`
	Line     int      `json:"line,omitempty"`
	Message  string   `json:"message"`
	OWASP    string   `json:"owasp,omitempty"`
}

// Rule describes a rule for the report.
type Rule struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	OWASP   string `json:"owasp"`
	Docs    string `json:"docs"`
}

// RuleID is a rule's name: lower case, dotted.
var RuleID = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)+$`)

// Formats are the report's writers, by --format.
var Formats = map[string]func(io.Writer, Report) error{"text": writeText, "json": writeJSON, "sarif": writeSARIF, "markdown": writeMarkdown}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// IgnoreEntry is an entry of security-ignore.yaml.
type IgnoreEntry struct {
	Rule    string `yaml:"rule"`
	Where   string `yaml:"where,omitempty"`
	Reason  string `yaml:"reason"`
	Expires string `yaml:"expires,omitempty"`

	line    int       // the entry's line in the file
	expires time.Time // the end of its last day, in the app's time zone
	used    int       // findings it ignored
	expired int       // findings it matched after it expired
}

// matches reports whether the entry ignores f.
func (e *IgnoreEntry) matches(f Finding) bool {
	return e.Rule == f.Rule && (e.Where == "" || e.Where == f.Where)
}

// over reports whether the entry has expired at now.
func (e *IgnoreEntry) over(now time.Time) bool {
	return !e.expires.IsZero() && !now.Before(e.expires)
}

// describe names the entry's findings: "route.csrf at POST /import".
func (e *IgnoreEntry) describe() string {
	if e.Where != "" {
		return e.Rule + " at " + e.Where
	}
	return e.Rule
}

// DefaultIgnoreFile is the default file of ignored findings.
const DefaultIgnoreFile = "security-ignore.yaml"

// ReadIgnores reads the ignore file: a missing file is none, unless it
// was named. A key it doesn't know (a typo of where or expires, which
// would ignore more, for longer) is an error. An entry expires at the
// end of its day in loc.
func ReadIgnores(path string, named bool, loc *time.Location) ([]*IgnoreEntry, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !named {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []*IgnoreEntry
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&entries); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w (a list of entries with rule, where, reason and expires)", path, err)
	}
	var doc yaml.Node // the entries' lines, for SARIF
	_ = yaml.Unmarshal(b, &doc)
	for i, e := range entries {
		switch {
		case e == nil || e.Rule == "":
			return nil, fmt.Errorf("%s: entry %d has no rule", path, i+1)
		case strings.TrimSpace(e.Reason) == "":
			return nil, fmt.Errorf("%s: entry %d (%s) has no reason: say why the finding is safe to ignore", path, i+1, e.Rule)
		case e.Expires != "":
			t, err := time.ParseInLocation(time.DateOnly, e.Expires, loc)
			if err != nil {
				return nil, fmt.Errorf("%s: entry %d (%s): expires %q isn't a date (2027-01-31)", path, i+1, e.Rule, e.Expires)
			}
			e.expires = t.AddDate(0, 0, 1)
		}
		e.line = 1
		if len(doc.Content) == 1 && i < len(doc.Content[0].Content) {
			e.line = doc.Content[0].Content[i].Line
		}
	}
	return entries, nil
}

// ToolFindings are findings the anetos tool adds (--findings): its rules
// about the project's code and dependencies, which it checks itself.
type ToolFindings struct {
	Rules    []Rule    `json:"rules"`
	Findings []Finding `json:"findings"`
}

// ReadToolFindings reads a --findings file.
func ReadToolFindings(path string) (ToolFindings, error) {
	var t ToolFindings
	b, err := os.ReadFile(path)
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return t, fmt.Errorf("%s: %w", path, err)
	}
	for _, f := range t.Findings {
		if !RuleID.MatchString(f.Rule) {
			return t, fmt.Errorf("%s: a finding's rule %q isn't a rule's name", path, f.Rule)
		}
	}
	return t, nil
}

// Report is the result of a security check.
type Report struct {
	App      string         `json:"app"`
	Env      string         `json:"env"`
	Level    string         `json:"level"`
	Findings []Finding      `json:"findings"`
	Ignored  []Ignored      `json:"ignored"`
	Summary  map[string]int `json:"summary"`
	Limits   string         `json:"limits"`

	// Rules describe the findings' rules, for their docs and summaries.
	Rules map[string]Rule `json:"-"`
	// IgnoreFile is the file of ignored findings, as named.
	IgnoreFile string `json:"-"`
	// Version is the checker's, for SARIF.
	Version string `json:"-"`

	ignoreLine map[string]int // ignore.* findings' lines in it, by Where+Message
}

// Ignored is a finding an ignore entry ignored, with its reason.
type Ignored struct {
	Finding
	Reason string `json:"reason"`
}

// Limits says what a clean report means.
const Limits = "None of these rules finding anything doesn't make the app secure: who may read or change whose records, business logic and the code itself need tests and review."

// Finish applies the ignore entries to found, adds the entries'
// findings, sorts the findings (the worst first) and counts them.
func (rep *Report) Finish(found []Finding, ignores []*IgnoreEntry, now time.Time) {
	if rep.Rules == nil {
		rep.Rules = map[string]Rule{}
	}
	if rep.Findings == nil {
		rep.Findings = []Finding{}
	}
	if rep.Ignored == nil {
		rep.Ignored = []Ignored{}
	}
	if rep.Summary == nil {
		rep.Summary = map[string]int{}
	}
	if rep.ignoreLine == nil {
		rep.ignoreLine = map[string]int{}
	}
	ignorePath := rep.IgnoreFile
	for _, f := range found {
		// The first entry that matches and hasn't expired; when there's
		// none, the expired ones that match say so.
		var by *IgnoreEntry
		var expired []*IgnoreEntry
		for _, e := range ignores {
			if !e.matches(f) {
				continue
			}
			if !e.over(now) {
				by = e
				break
			}
			expired = append(expired, e)
		}
		if by != nil {
			by.used++
			rep.Ignored = append(rep.Ignored, Ignored{f, by.Reason})
			continue
		}
		for _, e := range expired {
			e.expired++
		}
		rep.Findings = append(rep.Findings, f)
	}
	for _, e := range ignores {
		var f Finding
		switch {
		case e.used > 0:
			continue
		case e.expired > 0:
			f = Finding{Rule: "ignore.expired", Severity: Warning,
				Message: fmt.Sprintf("the entry for %s expired on %s: its finding is reported again; fix it, or renew the entry with a reason", e.describe(), e.Expires)}
		case e.over(now):
			f = Finding{Rule: "ignore.unused", Severity: Note,
				Message: fmt.Sprintf("the entry for %s expired on %s and matches no finding (at this level): remove it", e.describe(), e.Expires)}
		default:
			f = Finding{Rule: "ignore.unused", Severity: Note,
				Message: fmt.Sprintf("the entry for %s matches no finding (at this level): remove it if it's fixed", e.describe())}
		}
		f.Where = ignorePath
		rep.ignoreLine[f.Where+f.Message] = e.line
		rep.Findings = append(rep.Findings, f)
	}
	slices.SortStableFunc(rep.Findings, func(x, y Finding) int {
		return cmp.Or(cmp.Compare(y.Severity, x.Severity), cmp.Compare(x.Rule, y.Rule), cmp.Compare(x.Where, y.Where))
	})
	for _, f := range rep.Findings {
		rep.Summary[f.Severity.String()+"s"]++
	}
	for _, k := range []string{"problems", "warnings", "notes"} {
		rep.Summary[k] += 0
	}
	rep.Summary["ignored"] = len(rep.Ignored)
}

// SummaryLine counts the findings: "1 problem, 0 warnings, 2 notes".
func (rep Report) SummaryLine() string {
	s := fmt.Sprintf("%s, %s, %s", plural(rep.Summary["problems"], "problem"), plural(rep.Summary["warnings"], "warning"), plural(rep.Summary["notes"], "note"))
	if n := rep.Summary["ignored"]; n > 0 {
		s += fmt.Sprintf("; %d ignored (%s)", n, rep.IgnoreFile)
	}
	return s
}

func place(f Finding) string {
	p := f.Where
	if f.File != "" {
		p += fmt.Sprintf("  %s:%d", f.File, f.Line)
	}
	return p
}

func writeText(w io.Writer, rep Report) error {
	if rep.Env != "" {
		fmt.Fprintf(w, "Security check of %s (APP_ENV=%s, level %s).\n", rep.App, rep.Env, rep.Level)
	} else {
		fmt.Fprintf(w, "Security check of %s (level %s).\n", rep.App, rep.Level)
	}
	if env := rep.Env; env != "" && env != "production" && env != "staging" {
		fmt.Fprintln(w, "The settings' rules apply to production and staging: run it with those settings too (on the server, or APP_ENV=production with them).")
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(w, "\n%-8s %s  %s\n", f.Severity, f.Rule, place(f))
		fmt.Fprintf(w, "         %s\n", f.Message)
		info := []string{}
		if f.OWASP != "" {
			info = append(info, f.OWASP)
		}
		if d := rep.Rules[f.Rule].Docs; d != "" {
			info = append(info, d)
		}
		if len(info) > 0 {
			fmt.Fprintf(w, "         %s\n", strings.Join(info, " · "))
		}
	}
	if len(rep.Ignored) > 0 {
		fmt.Fprintf(w, "\nIgnored (%s):\n", rep.IgnoreFile)
		for _, f := range rep.Ignored {
			fmt.Fprintf(w, "  %s  %s: %s\n", f.Rule, place(f.Finding), f.Reason)
		}
	}
	fmt.Fprintf(w, "\n%s.\n%s\n", rep.SummaryLine(), rep.Limits)
	return nil
}

func writeJSON(w io.Writer, rep Report) error {
	type finding struct {
		Finding
		Docs string `json:"docs,omitempty"`
	}
	out := struct {
		Report
		Findings []finding `json:"findings"`
	}{Report: rep, Findings: []finding{}}
	for _, f := range rep.Findings {
		out.Findings = append(out.Findings, finding{f, rep.Rules[f.Rule].Docs})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeMarkdown(w io.Writer, rep Report) error {
	env := ""
	if rep.Env != "" {
		env = "`APP_ENV=" + rep.Env + "`, "
	}
	fmt.Fprintf(w, "## Security check of %s\n\n%slevel %s: %s.\n\n", rep.App, env, rep.Level, rep.SummaryLine())
	md := strings.NewReplacer("|", `\|`, "\n", " ")
	if len(rep.Findings) > 0 {
		fmt.Fprintln(w, "| Severity | Rule | Where | Finding |")
		fmt.Fprintln(w, "|---|---|---|---|")
		for _, f := range rep.Findings {
			rule := "`" + f.Rule + "`"
			if d := rep.Rules[f.Rule].Docs; d != "" {
				rule = "[" + rule + "](" + d + ")"
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", f.Severity, rule, md.Replace(place(f)), md.Replace(f.Message+cond(f.OWASP != "", " ("+f.OWASP+")", "")))
		}
		fmt.Fprintln(w)
	}
	if len(rep.Ignored) > 0 {
		fmt.Fprintf(w, "Ignored (`%s`):\n\n", rep.IgnoreFile)
		for _, f := range rep.Ignored {
			fmt.Fprintf(w, "- `%s` at %s: %s\n", f.Rule, md.Replace(place(f.Finding)), md.Replace(f.Reason))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "_%s_\n", rep.Limits)
	return nil
}

func cond(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

// writeSARIF writes the report as SARIF 2.1.0, for GitHub's
// code scanning: a route's finding at the line that registered it, a
// setting's at its line in .env.example (else go.mod's first line), an
// ignore entry's at its line. Files are relative to the repository's
// root (%SRCROOT%) when the module is in a git repository.
func writeSARIF(w io.Writer, rep Report) error {
	type region struct {
		StartLine int `json:"startLine"`
	}
	type artifact struct {
		URI       string `json:"uri"`
		URIBaseID string `json:"uriBaseId,omitempty"`
	}
	type location struct {
		Physical struct {
			Artifact artifact `json:"artifactLocation"`
			Region   region   `json:"region"`
		} `json:"physicalLocation"`
	}
	type text struct {
		Text string `json:"text"`
	}
	type rule struct {
		ID         string         `json:"id"`
		Short      text           `json:"shortDescription"`
		HelpURI    string         `json:"helpUri,omitempty"`
		Properties map[string]any `json:"properties"`
	}
	type result struct {
		RuleID       string            `json:"ruleId"`
		RuleIndex    int               `json:"ruleIndex"`
		Level        string            `json:"level"`
		Message      text              `json:"message"`
		Locations    []location        `json:"locations"`
		Fingerprints map[string]string `json:"partialFingerprints"`
	}
	levels := map[Severity]string{Problem: "error", Warning: "warning", Note: "note"}
	// GitHub ranks security alerts by the rule's security-severity: the
	// worst of its findings here.
	scores := map[Severity]string{Problem: "8.0", Warning: "5.0", Note: "2.0"}
	root := ModuleRoot()
	prefix := RepoPrefix()
	env, _ := os.ReadFile(filepath.Join(root, ".env.example"))
	at := func(file string, line int) location {
		var loc location
		switch {
		case filepath.IsAbs(file): // outside the module (the module cache)
			uri := filepath.ToSlash(file)
			if !strings.HasPrefix(uri, "/") {
				uri = "/" + uri // C:/…
			}
			loc.Physical.Artifact = artifact{URI: "file://" + uri}
		default:
			loc.Physical.Artifact = artifact{URI: prefix + file, URIBaseID: "%SRCROOT%"}
		}
		loc.Physical.Region.StartLine = max(line, 1)
		return loc
	}
	rules := []rule{}
	index := map[string]int{}
	worst := map[string]Severity{}
	results := []result{}
	for _, f := range rep.Findings {
		if _, ok := index[f.Rule]; !ok {
			index[f.Rule] = len(rules)
			r := rep.Rules[f.Rule]
			tags := []string{"security"}
			if owasp := cmp.Or(r.OWASP, f.OWASP); owasp != "" {
				tags = append(tags, owasp)
			}
			rules = append(rules, rule{ID: f.Rule, Short: text{cmp.Or(r.Summary, f.Rule)}, HelpURI: r.Docs, Properties: map[string]any{"tags": tags}})
			worst[f.Rule] = f.Severity
		}
		worst[f.Rule] = max(worst[f.Rule], f.Severity)
		var loc location
		switch {
		case f.File != "":
			loc = at(f.File, f.Line)
		case strings.HasPrefix(f.Rule, "ignore."):
			file := f.Where
			if abs, err := filepath.Abs(file); err == nil {
				file = Rel(abs)
			}
			loc = at(file, rep.ignoreLine[f.Where+f.Message])
		case len(env) > 0 && lineOf(env, f.Where+"=") > 0:
			loc = at(".env.example", lineOf(env, f.Where+"="))
		default:
			loc = at("go.mod", 1)
		}
		msg := f.Message
		if !strings.Contains(msg, f.Where) {
			msg = f.Where + ": " + msg
		}
		sum := sha256.Sum256([]byte(f.Rule + "\x00" + f.Where + "\x00" + f.File))
		results = append(results, result{RuleID: f.Rule, RuleIndex: index[f.Rule], Level: cmp.Or(levels[f.Severity], "warning"), Message: text{msg},
			Locations: []location{loc}, Fingerprints: map[string]string{"anetosFinding/v1": hex.EncodeToString(sum[:16])}})
	}
	for i := range rules {
		rules[i].Properties["security-severity"] = cmp.Or(scores[worst[rules[i].ID]], "5.0")
	}
	doc := map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []any{map[string]any{
			"tool": map[string]any{"driver": map[string]any{
				"name": "anetos security:check", "informationUri": Docs, "version": rep.Version,
				"rules": rules,
			}},
			"results": results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// lineOf is the line of b that starts with prefix (a comment's # aside:
// "# SESSION_DOMAIN="), or 0.
func lineOf(b []byte, prefix string) int {
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) || strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(line, "#")), prefix) {
			return i + 1
		}
	}
	return 0
}
