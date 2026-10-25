// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"time"

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
	if !securitycheck.RuleID.MatchString(r.ID) || r.Run == nil {
		panic(fmt.Sprintf("anetos: AddSecurityRule needs an ID such as \"billing.test-mode\" and a Run function (got %q)", r.ID))
	}
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()
	if slices.ContainsFunc(a.rules, func(x SecurityRule) bool { return x.ID == r.ID }) {
		panic("anetos: security rule " + r.ID + " added twice")
	}
	a.rules = append(a.rules, r)
}

// securityCheck runs the security checks and rules of the booted app,
// adds the tool's findings (extra), and applies the ignore entries.
func (a *App) securityCheck(ctx context.Context, level SecurityLevel, ignorePath string, ignores []*securitycheck.IgnoreEntry,
	extra securitycheck.ToolFindings, now time.Time) securitycheck.Report {
	a.cmdMu.Lock()
	checks := slices.Clone(a.checks)
	rules := slices.Clone(a.rules)
	a.cmdMu.Unlock()
	rep := securitycheck.Report{App: a.cfg.Name, Env: string(a.cfg.Env), Level: level.String(), Limits: securitycheck.Limits,
		Rules: map[string]securitycheck.Rule{}, IgnoreFile: ignorePath, Version: Version()}
	ruleFor := func(id string) securitycheck.Rule {
		if r, ok := rep.Rules[id]; ok {
			return r
		}
		if r, ok := settingRules[id]; ok {
			return securitycheck.Rule{ID: id, Summary: r.Summary, OWASP: r.OWASP, Docs: securitycheck.RuleDocs(id)}
		}
		return securitycheck.Rule{ID: id, OWASP: OWASPMisconfiguration} // check.<name>, check.panic
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
		rep.Rules[r.ID] = securitycheck.Rule{ID: r.ID, Summary: r.Summary, OWASP: r.OWASP, Docs: r.Docs}
		safely(r.ID, func() []SecurityFinding {
			out := r.Run(base, a)
			for i := range out {
				out[i].Rule = cmp.Or(out[i].Rule, r.ID)
			}
			return out
		})
	}
	for _, r := range extra.Rules {
		rep.Rules[r.ID] = r
	}
	all := make([]securitycheck.Finding, 0, len(found)+len(extra.Findings))
	for _, f := range found {
		all = append(all, securitycheck.Finding{Rule: f.Rule, Severity: securitycheck.Severity(f.Severity), Where: f.Where, File: f.File,
			Line: f.Line, Message: f.Message, OWASP: f.OWASP})
	}
	all = append(all, extra.Findings...)
	for i := range all {
		r := ruleFor(all[i].Rule)
		rep.Rules[r.ID] = r
		all[i].OWASP = cmp.Or(all[i].OWASP, r.OWASP)
		all[i].File = securitycheck.Rel(all[i].File)
	}
	rep.Finish(all, ignores, now)
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
		Usage:       "[--level=basic|advanced] [--format=text|json|sarif|markdown] [--out=FILE] [--ignore=FILE] [--strict] [--findings=FILE]",
		Description: "Check the app's settings and routes against security rules (CSRF, logins, limits, cookies, TLS…); exits 1 on problems",
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("security:check", flag.ContinueOnError)
			levelName := fs.String("level", "basic", "basic (high confidence) or advanced (adds rules that need a look)")
			format := fs.String("format", "text", "text, json, sarif (GitHub code scanning) or markdown")
			out := fs.String("out", "", "write the report to this file instead of the standard output (-: the standard output)")
			ignore := fs.String("ignore", "", "the file of ignored findings (default "+securitycheck.DefaultIgnoreFile+")")
			strict := fs.Bool("strict", false, "exit 1 on warnings too")
			findings := fs.String("findings", "", "a file of findings to add to the report (the anetos tool's, about the code)")
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
			write := securitycheck.Formats[*format]
			if write == nil {
				return cmd.Usagef("--format is text, json, sarif or markdown, not %q", *format)
			}
			now := a.Now()
			ignorePath := cmp.Or(*ignore, securitycheck.DefaultIgnoreFile)
			ignores, err := securitycheck.ReadIgnores(ignorePath, *ignore != "", now.Location())
			if err != nil {
				return err
			}
			var extra securitycheck.ToolFindings
			if *findings != "" {
				if extra, err = securitycheck.ReadToolFindings(*findings); err != nil {
					return err
				}
			}
			rep := a.securityCheck(ctx, level, ignorePath, ignores, extra, now)
			var buf bytes.Buffer
			if err := write(&buf, rep); err != nil {
				return err
			}
			if *out != "" && *out != "-" {
				if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(args.Stdout, "Wrote %s: %s.\n", *out, rep.SummaryLine())
			} else if _, err := args.Stdout.Write(buf.Bytes()); err != nil {
				return err
			}
			if rep.Summary["problems"] > 0 || *strict && rep.Summary["warnings"] > 0 {
				return errors.New(rep.SummaryLine())
			}
			return nil
		},
	}
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
