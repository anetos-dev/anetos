// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/internal/securitycheck"
)

// SecurityLevel is how thorough the security:check command is.
type SecurityLevel int

const (
	// SecurityBasic rules find what is almost always a mistake, with few
	// false positives: the default.
	SecurityBasic SecurityLevel = iota
	// SecurityAdvanced rules find what needs a look and may be on
	// purpose (--level=advanced).
	SecurityAdvanced
)

// String returns "basic" or "advanced".
func (l SecurityLevel) String() string {
	if l == SecurityAdvanced {
		return "advanced"
	}
	return "basic"
}

// SecurityFinding is what the security:check command reports: a
// [SecurityRule]'s finding, or a doctor [Finding] about security.
type SecurityFinding struct {
	// Rule is the rule's ID: "route.csrf", "setting.session-secure".
	Rule string `json:"rule"`
	// Severity says how much it matters; a Problem fails the check.
	Severity Severity `json:"severity"`
	// Where names the place: a route ("POST /posts"), a setting
	// ("SESSION_SECURE").
	Where string `json:"where"`
	// File is where in the code, when known: the file that registered
	// a route ([web.RouteInfo]). Reports show it relative to the
	// module's folder.
	File string `json:"file,omitempty"`
	// Line is File's line, when known.
	Line int `json:"line,omitempty"`
	// Message says what is wrong, why it matters and what to do.
	Message string `json:"message"`
	// OWASP is the OWASP Top 10:2025 category: "A01:2025 Broken Access
	// Control". The rule's when empty.
	OWASP string `json:"owasp,omitempty"`
}

// SecurityRule is a rule of the security:check command. Features add
// theirs when they are set up (web.NewServer's routes rules); an app or
// a plugin adds its own with [App.AddSecurityRule].
type SecurityRule struct {
	// ID names the rule: lower case, dotted ("billing.webhook-auth").
	ID string
	// Level is the level from which it runs: SecurityBasic rules run
	// always, SecurityAdvanced ones with --level=advanced.
	Level SecurityLevel
	// Summary says in a sentence what it finds, for reports (SARIF's
	// rule descriptions).
	Summary string
	// OWASP is its OWASP Top 10:2025 category, for its findings without
	// one.
	OWASP string
	// Docs is a page that explains it and its fix: an https URL, which
	// reports link to.
	Docs string
	// Run returns what it found, the app booted. The context has the
	// app's values.
	Run func(ctx context.Context, app *App) []SecurityFinding
}

// The OWASP Top 10:2025 categories the framework's rules use.
const (
	OWASPAccessControl    = "A01:2025 Broken Access Control"
	OWASPMisconfiguration = "A02:2025 Security Misconfiguration"
	OWASPCryptographic    = "A04:2025 Cryptographic Failures"
	OWASPAuthentication   = "A07:2025 Authentication Failures"
	OWASPLogging          = "A09:2025 Security Logging and Alerting Failures"
)

// settingRules describe the rules of the framework's doctor findings
// (Finding.Rule).
var settingRules = map[string]SecurityRule{
	"setting.debug":             {Summary: "Debug pages where others reach the app", OWASP: OWASPMisconfiguration},
	"setting.app-key":           {Summary: "No APP_KEY where the app is deployed", OWASP: OWASPCryptographic},
	"setting.app-url":           {Summary: "An http APP_URL where the app is deployed", OWASP: OWASPCryptographic},
	"setting.session-secure":    {Summary: "Session cookies sent over plain HTTP", OWASP: OWASPAuthentication},
	"setting.session-same-site": {Summary: "Session cookies sent with other sites' requests", OWASP: OWASPAccessControl},
	"setting.session-domain":    {Summary: "Session cookies shared with every subdomain", OWASP: OWASPAuthentication},
	"setting.trusted-proxies":   {Summary: "Clients trusted to set their IP address", OWASP: OWASPMisconfiguration},
	"setting.cors":              {Summary: "Any site's scripts may call the app", OWASP: OWASPMisconfiguration},
	"setting.body-limit":        {Summary: "Request bodies without a size limit", OWASP: OWASPMisconfiguration},
	"setting.request-timeout":   {Summary: "Requests without a deadline", OWASP: OWASPMisconfiguration},
	"setting.header-timeout":    {Summary: "Request headers without a deadline", OWASP: OWASPMisconfiguration},
	"setting.mail-tls":          {Summary: "Mail sent in plain text", OWASP: OWASPCryptographic},
	"setting.db-tls":            {Summary: "Database connections without (verified) TLS", OWASP: OWASPCryptographic},
	"setting.db-log-queries":    {Summary: "Queries logged with their values", OWASP: OWASPLogging},
	"setting.log-level":         {Summary: "Debug logs where the app is deployed", OWASP: OWASPLogging},
}

var ruleID = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)+$`)

// AddSecurityRule adds a rule to the app's security:check command:
//
//	app.AddSecurityRule(anetos.SecurityRule{ID: "billing.test-mode", Summary: "Billing in test mode in production",
//		OWASP: anetos.OWASPMisconfiguration, Run: func(ctx context.Context, app *anetos.App) []anetos.SecurityFinding {
//			if cfg.TestMode && app.Config().Env.IsProduction() {
//				return []anetos.SecurityFinding{{Severity: anetos.Problem, Where: "BILLING_TEST_MODE", Message: "…"}}
//			}
//			return nil
//		}})
//
// Rules about routes are written with web.RouteRule. The framework's
// rules are named route.… and setting.…; a doctor check's findings
// without a Rule are check.<name>'s. It panics without an ID (lower
// case, dotted) or a Run function, or with an ID already added, which
// is a programming error.
func (a *App) AddSecurityRule(r SecurityRule) {
	if !ruleID.MatchString(r.ID) || r.Run == nil {
		panic(fmt.Sprintf("anetos: AddSecurityRule needs an ID such as \"billing.test-mode\" and a Run function (got %q)", r.ID))
	}
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()
	if slices.ContainsFunc(a.rules, func(x SecurityRule) bool { return x.ID == r.ID }) {
		panic("anetos: security rule " + r.ID + " added twice")
	}
	a.rules = append(a.rules, r)
}

// ignoreEntry is an entry of security-ignore.yaml.
type ignoreEntry struct {
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
func (e *ignoreEntry) matches(f SecurityFinding) bool {
	return e.Rule == f.Rule && (e.Where == "" || e.Where == f.Where)
}

// over reports whether the entry has expired at now.
func (e *ignoreEntry) over(now time.Time) bool {
	return !e.expires.IsZero() && !now.Before(e.expires)
}

// describe names the entry's findings: "route.csrf at POST /import".
func (e *ignoreEntry) describe() string {
	if e.Where != "" {
		return e.Rule + " at " + e.Where
	}
	return e.Rule
}

// ignoreFile is the default file of ignored findings.
const ignoreFile = "security-ignore.yaml"

// readIgnores reads the ignore file: a missing file is none, unless it
// was named. A key it doesn't know (a typo of where or expires, which
// would ignore more, for longer) is an error. An entry expires at the
// end of its day in loc.
func readIgnores(path string, named bool, loc *time.Location) ([]*ignoreEntry, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !named {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []*ignoreEntry
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

// securityReport is the result of a security check.
type securityReport struct {
	App      string            `json:"app"`
	Env      string            `json:"env"`
	Level    string            `json:"level"`
	Findings []SecurityFinding `json:"findings"`
	Ignored  []ignoredFinding  `json:"ignored"`
	Summary  map[string]int    `json:"summary"`
	Limits   string            `json:"limits"`

	rules      map[string]SecurityRule
	ignoreFile string         // the file of ignored findings, as named
	ignoreLine map[string]int // ignore.* findings' lines in it, by Where+Message
}

type ignoredFinding struct {
	SecurityFinding
	Reason string `json:"reason"`
}

// securityLimits says what a clean report means.
const securityLimits = "None of these rules finding anything doesn't make the app secure: who may read or change whose records, business logic and the code itself need tests and review."

// securityCheck runs the security checks and rules of the booted app.
func (a *App) securityCheck(ctx context.Context, level SecurityLevel, ignorePath string, ignores []*ignoreEntry, now time.Time) securityReport {
	a.cmdMu.Lock()
	checks := slices.Clone(a.checks)
	rules := slices.Clone(a.rules)
	a.cmdMu.Unlock()
	rep := securityReport{App: a.cfg.Name, Env: string(a.cfg.Env), Level: level.String(), Findings: []SecurityFinding{}, Ignored: []ignoredFinding{},
		Summary: map[string]int{}, Limits: securityLimits, rules: map[string]SecurityRule{}, ignoreFile: ignorePath, ignoreLine: map[string]int{}}
	ruleFor := func(id string) SecurityRule {
		if r, ok := rep.rules[id]; ok {
			return r
		}
		if r, ok := settingRules[id]; ok {
			r.ID, r.Docs = id, securitycheck.RuleDocs(id)
			return r
		}
		return SecurityRule{ID: id, OWASP: OWASPMisconfiguration} // check.<name>, check.panic
	}
	var found []SecurityFinding
	safely := func(name string, run func() []SecurityFinding) {
		defer func() {
			if v := recover(); v != nil {
				found = append(found, SecurityFinding{Rule: "check.panic", Severity: Problem, Where: name, Message: fmt.Sprintf("the check panicked: %v", v)})
			}
		}()
		found = append(found, run()...)
	}
	base := a.Context(ctx)
	for _, c := range checks {
		safely(c.Name, func() []SecurityFinding {
			var out []SecurityFinding
			for _, f := range c.Run(base) {
				rule := f.Rule
				if rule == "" {
					if !c.Security {
						continue
					}
					rule = "check." + c.Name
				}
				out = append(out, SecurityFinding{Rule: rule, Severity: f.Severity, Where: settingName(f.Message, c.Name), Message: f.Message})
			}
			return out
		})
	}
	for _, r := range rules {
		if r.Level > level {
			continue
		}
		rep.rules[r.ID] = r
		safely(r.ID, func() []SecurityFinding {
			out := r.Run(base, a)
			for i := range out {
				out[i].Rule = cmp.Or(out[i].Rule, r.ID)
			}
			return out
		})
	}
	for i := range found {
		r := ruleFor(found[i].Rule)
		rep.rules[r.ID] = r
		found[i].OWASP = cmp.Or(found[i].OWASP, r.OWASP)
		found[i].File = securitycheck.Rel(found[i].File)
	}
	for _, f := range found {
		// The first entry that matches and hasn't expired; when there's
		// none, the expired ones that match say so.
		var by *ignoreEntry
		var expired []*ignoreEntry
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
			rep.Ignored = append(rep.Ignored, ignoredFinding{f, by.Reason})
			continue
		}
		for _, e := range expired {
			e.expired++
		}
		rep.Findings = append(rep.Findings, f)
	}
	for _, e := range ignores {
		var f SecurityFinding
		switch {
		case e.used > 0:
			continue
		case e.expired > 0:
			f = SecurityFinding{Rule: "ignore.expired", Severity: Warning,
				Message: fmt.Sprintf("the entry for %s expired on %s: its finding is reported again; fix it, or renew the entry with a reason", e.describe(), e.Expires)}
		case e.over(now):
			f = SecurityFinding{Rule: "ignore.unused", Severity: Note,
				Message: fmt.Sprintf("the entry for %s expired on %s and matches no finding (at this level): remove it", e.describe(), e.Expires)}
		default:
			f = SecurityFinding{Rule: "ignore.unused", Severity: Note,
				Message: fmt.Sprintf("the entry for %s matches no finding (at this level): remove it if it's fixed", e.describe())}
		}
		f.Where = ignorePath
		rep.ignoreLine[f.Where+f.Message] = e.line
		rep.Findings = append(rep.Findings, f)
	}
	slices.SortStableFunc(rep.Findings, func(x, y SecurityFinding) int {
		return cmp.Or(cmp.Compare(y.Severity, x.Severity), cmp.Compare(x.Rule, y.Rule), cmp.Compare(x.Where, y.Where))
	})
	for _, f := range rep.Findings {
		rep.Summary[f.Severity.String()+"s"]++
	}
	for _, k := range []string{"problems", "warnings", "notes"} {
		rep.Summary[k] += 0
	}
	rep.Summary["ignored"] = len(rep.Ignored)
	return rep
}

// settingName is the setting a doctor finding's message names first
// ("SESSION_SECURE=false in production: …"), else the check's name.
func settingName(msg, check string) string {
	if m := settingInMessage.FindString(msg); m != "" {
		return m
	}
	return check
}

var settingInMessage = regexp.MustCompile(`\b[A-Z][A-Z0-9]*_[A-Z0-9_]+\b`)

func (a *App) securityCommand() cmd.Command {
	return cmd.Command{
		Name:        "security:check",
		Usage:       "[--level=basic|advanced] [--format=text|json|sarif|markdown] [--out=FILE] [--ignore=FILE] [--strict]",
		Description: "Check the app's settings and routes against security rules (CSRF, logins, limits, cookies, TLS…); exits 1 on problems",
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("security:check", flag.ContinueOnError)
			levelName := fs.String("level", "basic", "basic (high confidence) or advanced (adds rules that need a look)")
			format := fs.String("format", "text", "text, json, sarif (GitHub code scanning) or markdown")
			out := fs.String("out", "", "write the report to this file instead of the standard output (-: the standard output)")
			ignore := fs.String("ignore", "", "the file of ignored findings (default "+ignoreFile+")")
			strict := fs.Bool("strict", false, "exit 1 on warnings too")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 0 {
				return cmd.Usagef("unexpected argument %q", fs.Arg(0))
			}
			var level SecurityLevel
			switch *levelName {
			case "basic":
			case "advanced":
				level = SecurityAdvanced
			default:
				return cmd.Usagef("--level is basic or advanced, not %q", *levelName)
			}
			write := map[string]func(io.Writer, securityReport) error{"text": writeSecurityText, "json": writeSecurityJSON,
				"sarif": writeSecuritySARIF, "markdown": writeSecurityMarkdown}[*format]
			if write == nil {
				return cmd.Usagef("--format is text, json, sarif or markdown, not %q", *format)
			}
			now := a.Now()
			ignorePath := cmp.Or(*ignore, ignoreFile)
			ignores, err := readIgnores(ignorePath, *ignore != "", now.Location())
			if err != nil {
				return err
			}
			rep := a.securityCheck(ctx, level, ignorePath, ignores, now)
			var buf bytes.Buffer
			if err := write(&buf, rep); err != nil {
				return err
			}
			if *out != "" && *out != "-" {
				if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(args.Stdout, "Wrote %s: %s.\n", *out, summaryLine(rep))
			} else if _, err := args.Stdout.Write(buf.Bytes()); err != nil {
				return err
			}
			if rep.Summary["problems"] > 0 || *strict && rep.Summary["warnings"] > 0 {
				return errors.New(summaryLine(rep))
			}
			return nil
		},
	}
}

func summaryLine(rep securityReport) string {
	s := fmt.Sprintf("%s, %s, %s", plural(rep.Summary["problems"], "problem"), plural(rep.Summary["warnings"], "warning"), plural(rep.Summary["notes"], "note"))
	if n := rep.Summary["ignored"]; n > 0 {
		s += fmt.Sprintf("; %d ignored (%s)", n, rep.ignoreFile)
	}
	return s
}

func place(f SecurityFinding) string {
	p := f.Where
	if f.File != "" {
		p += fmt.Sprintf("  %s:%d", f.File, f.Line)
	}
	return p
}

func writeSecurityText(w io.Writer, rep securityReport) error {
	fmt.Fprintf(w, "Security check of %s (APP_ENV=%s, level %s).\n", rep.App, rep.Env, rep.Level)
	if env := Environment(rep.Env); !env.Deployed() {
		fmt.Fprintln(w, "The settings' rules apply to production and staging: run it with those settings too (on the server, or APP_ENV=production with them).")
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(w, "\n%-8s %s  %s\n", f.Severity, f.Rule, place(f))
		fmt.Fprintf(w, "         %s\n", f.Message)
		info := []string{}
		if f.OWASP != "" {
			info = append(info, f.OWASP)
		}
		if d := rep.rules[f.Rule].Docs; d != "" {
			info = append(info, d)
		}
		if len(info) > 0 {
			fmt.Fprintf(w, "         %s\n", strings.Join(info, " · "))
		}
	}
	if len(rep.Ignored) > 0 {
		fmt.Fprintf(w, "\nIgnored (%s):\n", rep.ignoreFile)
		for _, f := range rep.Ignored {
			fmt.Fprintf(w, "  %s  %s: %s\n", f.Rule, place(f.SecurityFinding), f.Reason)
		}
	}
	fmt.Fprintf(w, "\n%s.\n%s\n", summaryLine(rep), rep.Limits)
	return nil
}

func writeSecurityJSON(w io.Writer, rep securityReport) error {
	type finding struct {
		SecurityFinding
		Docs string `json:"docs,omitempty"`
	}
	out := struct {
		securityReport
		Findings []finding `json:"findings"`
	}{securityReport: rep, Findings: []finding{}}
	for _, f := range rep.Findings {
		out.Findings = append(out.Findings, finding{f, rep.rules[f.Rule].Docs})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeSecurityMarkdown(w io.Writer, rep securityReport) error {
	fmt.Fprintf(w, "## Security check of %s\n\n`APP_ENV=%s`, level %s: %s.\n\n", rep.App, rep.Env, rep.Level, summaryLine(rep))
	md := strings.NewReplacer("|", `\|`, "\n", " ")
	if len(rep.Findings) > 0 {
		fmt.Fprintln(w, "| Severity | Rule | Where | Finding |")
		fmt.Fprintln(w, "|---|---|---|---|")
		for _, f := range rep.Findings {
			rule := "`" + f.Rule + "`"
			if d := rep.rules[f.Rule].Docs; d != "" {
				rule = "[" + rule + "](" + d + ")"
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", f.Severity, rule, md.Replace(place(f)), md.Replace(f.Message+cond(f.OWASP != "", " ("+f.OWASP+")", "")))
		}
		fmt.Fprintln(w)
	}
	if len(rep.Ignored) > 0 {
		fmt.Fprintf(w, "Ignored (`%s`):\n\n", rep.ignoreFile)
		for _, f := range rep.Ignored {
			fmt.Fprintf(w, "- `%s` at %s: %s\n", f.Rule, md.Replace(place(f.SecurityFinding)), md.Replace(f.Reason))
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

// writeSecuritySARIF writes the report as SARIF 2.1.0, for GitHub's
// code scanning: a route's finding at the line that registered it, a
// setting's at its line in .env.example (else go.mod's first line), an
// ignore entry's at its line. Files are relative to the repository's
// root (%SRCROOT%) when the module is in a git repository.
func writeSecuritySARIF(w io.Writer, rep securityReport) error {
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
	root := securitycheck.ModuleRoot()
	prefix := securitycheck.RepoPrefix()
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
			r := rep.rules[f.Rule]
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
				file = securitycheck.Rel(abs)
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
				"name": "anetos security:check", "informationUri": securitycheck.Docs, "version": Version(),
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

// logLevelRule finds debug logs where the app is deployed (advanced:
// someone may turn them on for a while on purpose).
func (a *App) logLevelRule() SecurityRule {
	return SecurityRule{ID: "setting.log-level", Level: SecurityAdvanced, Summary: settingRules["setting.log-level"].Summary, OWASP: OWASPLogging,
		Run: func(context.Context, *App) []SecurityFinding {
			if !a.cfg.Env.Deployed() || a.cfg.Log.Level > slog.LevelDebug {
				return nil
			}
			return []SecurityFinding{{Severity: Warning, Where: "LOG_LEVEL",
				Message: fmt.Sprintf("LOG_LEVEL=debug in %s: debug logs may hold what the app handles (queries, payloads, tokens) and fill the disk; use info, and debug only while you look into something", a.cfg.Env)}}
		}}
}
