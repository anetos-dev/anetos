// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

// securityApp is an app in production with a doctor check marked
// Security, a check whose findings aren't (but one), and rules at both
// levels.
func securityApp(t *testing.T) *anetos.App {
	t.Helper()
	app := newApp(t, config.Map{"APP_ENV": "production", "APP_URL": "https://shop.example.org", "APP_KEY": "base64:" + strings.Repeat("a", 43) + "="})
	app.AddCheck(anetos.Check{Name: "billing", Security: true, Run: func(context.Context) []anetos.Finding {
		return []anetos.Finding{{Severity: anetos.Problem, Message: "BILLING_TEST_MODE=true in production: nobody pays"}}
	}})
	app.AddCheck(anetos.Check{Name: "mail", Run: func(context.Context) []anetos.Finding {
		return []anetos.Finding{
			{Severity: anetos.Warning, Message: "MAIL_FROM_ADDRESS isn't set"},                          // not security
			{Severity: anetos.Warning, Message: "MAIL_SMTP_URL has tls=none", Rule: "setting.mail-tls"}, // security
		}
	}})
	app.AddSecurityRule(anetos.SecurityRule{ID: "shop.webhook", Summary: "Webhooks without signatures", OWASP: anetos.OWASPAccessControl,
		Docs: "https://shop.example.org/docs/webhooks", Run: func(context.Context, *anetos.App) []anetos.SecurityFinding {
			return []anetos.SecurityFinding{{Severity: anetos.Warning, Where: "POST /webhooks", File: "routes/web.go", Line: 12, Message: "no signature check"}}
		}})
	app.AddSecurityRule(anetos.SecurityRule{ID: "shop.deep", Level: anetos.SecurityAdvanced, Run: func(context.Context, *anetos.App) []anetos.SecurityFinding {
		return []anetos.SecurityFinding{{Severity: anetos.Note, Where: "x", Message: "a thorough look"}}
	}})
	return app
}

type securityJSON struct {
	Level    string
	Findings []struct {
		Severity, Rule, Where, File, Message, OWASP, Docs string
		Line                                              int
	}
	Ignored []struct{ Rule, Where, Reason string }
	Summary map[string]int
	Limits  string
}

func TestSecurityCheck(t *testing.T) {
	t.Chdir(t.TempDir()) // no security-ignore.yaml
	code, out, errOut := execute(t, securityApp(t), "security:check", "--format=json")
	if code != 1 || !strings.Contains(errOut, "1 problem, 2 warnings") {
		t.Errorf("exit %d (a problem fails it): %s", code, errOut)
	}
	var rep securityJSON
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	rules := map[string]string{}
	for _, f := range rep.Findings {
		rules[f.Rule] = f.Severity + " " + f.Where + " " + f.OWASP
	}
	want := map[string]string{
		"check.billing":    "problem BILLING_TEST_MODE " + anetos.OWASPMisconfiguration,
		"setting.mail-tls": "warning MAIL_SMTP_URL " + anetos.OWASPCryptographic,
		"shop.webhook":     "warning POST /webhooks " + anetos.OWASPAccessControl,
	}
	if len(rules) != len(want) {
		t.Errorf("findings: %v", rules)
	}
	for r, w := range want {
		if rules[r] != w {
			t.Errorf("%s: %q, want %q", r, rules[r], w)
		}
	}
	if rep.Findings[0].Rule != "check.billing" || rep.Level != "basic" || rep.Limits == "" || rep.Summary["problems"] != 1 {
		t.Errorf("order, level, limits, summary: %+v", rep)
	}

	// Advanced runs the advanced rules too; --strict fails on warnings.
	code, out, _ = execute(t, securityApp(t), "security:check", "--level=advanced", "--format=markdown")
	if code != 1 || !strings.Contains(out, "`shop.deep`") || !strings.Contains(out, "[`shop.webhook`](https://shop.example.org/docs/webhooks)") || !strings.Contains(out, "| Severity |") {
		t.Errorf("advanced, markdown: %d\n%s", code, out)
	}
	if code, _, errOut := execute(t, securityApp(t), "security:check", "--level=nope"); code != 2 || !strings.Contains(errOut, "basic or advanced") {
		t.Errorf("--level=nope: %d %s", code, errOut)
	}
}

func TestSecurityIgnore(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "security-ignore.yaml"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`
- rule: check.billing
  reason: the shop isn't open yet
- rule: shop.webhook
  where: POST /webhooks
  reason: the payment provider's IP addresses only
  expires: 2000-01-01
- rule: route.csrf
  where: POST /nowhere
  reason: an old one
`)
	code, out, _ := execute(t, securityApp(t), "security:check", "--format=json")
	var rep securityJSON
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if code != 0 || len(rep.Ignored) != 1 || rep.Ignored[0].Rule != "check.billing" || rep.Ignored[0].Reason != "the shop isn't open yet" {
		t.Errorf("ignored: %d %+v", code, rep.Ignored)
	}
	got := map[string]string{}
	for _, f := range rep.Findings {
		got[f.Rule] = f.Severity
	}
	// The expired entry ignores nothing and is reported; the unused one
	// is a note.
	if got["shop.webhook"] != "warning" || got["ignore.expired"] != "warning" || got["ignore.unused"] != "note" {
		t.Errorf("findings: %v", got)
	}

	// The first entry that hasn't expired counts; an expired entry
	// whose finding is gone, or ignored by another, is a note.
	write(`
- rule: shop.webhook
  where: POST /webhooks
  reason: until the provider signs its calls
  expires: 2000-01-01
- rule: shop.webhook
  reason: the provider's IP addresses only
- rule: route.csrf
  reason: fixed since
  expires: 2000-01-01
`)
	_, out, _ = execute(t, securityApp(t), "security:check", "--format=json")
	rep = securityJSON{}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got = map[string]string{}
	notes := 0
	for _, f := range rep.Findings {
		got[f.Rule] = f.Severity
		if f.Rule == "ignore.unused" {
			notes++
		}
	}
	if len(rep.Ignored) != 1 || rep.Ignored[0].Reason != "the provider's IP addresses only" || got["ignore.expired"] != "" || notes != 2 {
		t.Errorf("precedence: %v %+v", got, rep.Ignored)
	}

	// A key it doesn't know is an error: a typo of where or expires
	// would ignore more, for longer.
	write("- rule: shop.webhook\n  wher: POST /elsewhere\n  reason: typo\n")
	if code, _, errOut := execute(t, securityApp(t), "security:check"); code != 1 || !strings.Contains(errOut, "wher") {
		t.Errorf("an unknown key: %d %s", code, errOut)
	}

	// Another file, named in the report; an entry expires at the end of
	// its day in the app's zone.
	if err := os.WriteFile(filepath.Join(dir, "other.yaml"), []byte("- rule: check.billing\n  reason: closed\n  expires: 2099-12-31\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := execute(t, securityApp(t), "security:check", "--ignore=other.yaml"); code != 0 || !strings.Contains(out, "1 ignored (other.yaml)") || !strings.Contains(out, "Ignored (other.yaml)") {
		t.Errorf("--ignore=other.yaml: %d\n%s", code, out)
	}

	write("- rule: check.billing\n")
	if code, _, errOut := execute(t, securityApp(t), "security:check"); code != 1 || !strings.Contains(errOut, "has no reason") {
		t.Errorf("an entry without a reason: %d %s", code, errOut)
	}
	if code, _, errOut := execute(t, securityApp(t), "security:check", "--ignore=missing.yaml"); code != 1 || !strings.Contains(errOut, "missing.yaml") {
		t.Errorf("a missing file named: %d %s", code, errOut)
	}
}

// TestSecurityIgnoreExpiry checks that an entry lasts until the end of
// its day in the app's zone.
func TestSecurityIgnoreExpiry(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "security-ignore.yaml"), []byte("- rule: check.billing\n  reason: closed\n  expires: 2030-01-01\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for at, want := range map[string]int{
		"2030-01-01T17:59:00Z": 0, // 23:59 in Dhaka
		"2030-01-01T18:00:00Z": 1, // the 2nd in Dhaka, still the 1st in UTC
	} {
		app := newApp(t, config.Map{"APP_ENV": "production", "APP_TIMEZONE": "Asia/Dhaka", "APP_URL": "https://shop.example.org", "APP_KEY": "base64:" + strings.Repeat("a", 43) + "="})
		app.AddCheck(anetos.Check{Name: "billing", Security: true, Run: func(context.Context) []anetos.Finding {
			return []anetos.Finding{{Severity: anetos.Problem, Message: "BILLING_TEST_MODE=true in production"}}
		}})
		now, _ := time.Parse(time.RFC3339, at)
		app.SetClock(func() time.Time { return now })
		if code, out, _ := execute(t, app, "security:check"); code != want {
			t.Errorf("at %s: exit %d, want %d\n%s", at, code, want, out)
		}
	}
}

func TestSecurityReports(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("APP_ENV=development\nBILLING_TEST_MODE=false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "security-ignore.yaml"), []byte("# kept\n\n- rule: route.csrf\n  reason: an old one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := execute(t, securityApp(t), "security:check", "--format=sarif", "--out=report.sarif")
	if code != 1 || !strings.Contains(out, "Wrote report.sarif: 1 problem") {
		t.Errorf("--out: %d %s", code, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "report.sarif"))
	if err != nil {
		t.Fatal(err)
	}
	var sarif struct {
		Version string
		Runs    []struct {
			Tool struct {
				Driver struct {
					Rules []struct {
						ID, HelpURI string
						Properties  map[string]any
					}
				}
			}
			Results []struct {
				RuleID, Level       string
				RuleIndex           int
				PartialFingerprints map[string]string
				Locations           []struct {
					PhysicalLocation struct {
						ArtifactLocation struct{ URI, URIBaseID string }
						Region           struct{ StartLine int }
					}
				}
			}
		}
	}
	if err := json.Unmarshal(b, &sarif); err != nil || sarif.Version != "2.1.0" || len(sarif.Runs) != 1 {
		t.Fatalf("%v\n%s", err, b)
	}
	locs := map[string]string{}
	run := sarif.Runs[0]
	for _, r := range run.Results {
		l := r.Locations[0].PhysicalLocation
		locs[r.RuleID] = r.Level + " " + l.ArtifactLocation.URI + ":" + strconv.Itoa(l.Region.StartLine)
		if rule := run.Tool.Driver.Rules[r.RuleIndex]; rule.ID != r.RuleID || rule.Properties["security-severity"] == nil ||
			l.ArtifactLocation.URIBaseID != "%SRCROOT%" || len(r.PartialFingerprints["anetosFinding/v1"]) != 32 {
			t.Errorf("SARIF result %s: rule %+v, %+v, %v", r.RuleID, rule, l, r.PartialFingerprints)
		}
	}
	if locs["check.billing"] != "error .env.example:2" || locs["shop.webhook"] != "warning routes/web.go:12" || locs["ignore.unused"] != "note security-ignore.yaml:3" {
		t.Errorf("SARIF locations: %v", locs)
	}
	if code, out, _ := execute(t, securityApp(t), "security:check", "--format=json", "--out=-"); code != 1 || !strings.HasPrefix(out, "{") {
		t.Errorf("--out=-: %d %s", code, out)
	}
	code, out, _ = execute(t, securityApp(t), "security:check")
	if code != 1 || !strings.Contains(out, "problem  check.billing  BILLING_TEST_MODE") || !strings.Contains(out, "POST /webhooks  routes/web.go:12") ||
		!strings.Contains(out, "doesn't make the app secure") {
		t.Errorf("text:\n%s", out)
	}
}

func TestAddSecurityRule(t *testing.T) {
	app := newApp(t, config.Map{})
	run := func(context.Context, *anetos.App) []anetos.SecurityFinding { return nil }
	for _, r := range []anetos.SecurityRule{{ID: "NoDots", Run: run}, {ID: "a.b"}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("AddSecurityRule(%+v) didn't panic", r.ID)
				}
			}()
			app.AddSecurityRule(r)
		}()
	}
	app.AddSecurityRule(anetos.SecurityRule{ID: "a.b", Run: run})
	defer func() {
		if recover() == nil {
			t.Error("a rule added twice didn't panic")
		}
	}()
	app.AddSecurityRule(anetos.SecurityRule{ID: "a.b", Run: run})
}

// TestSecurityRulesDocumented checks that every rule the framework's code
// names (a SecurityRule's ID, a Finding's Rule) has its section in the
// guide, where reports link to.
func TestSecurityRulesDocumented(t *testing.T) {
	guide, err := os.ReadFile(filepath.Join("docs", "site", "guides", "security-check.md"))
	if err != nil {
		t.Fatal(err)
	}
	id := regexp.MustCompile(`(?:ID|Rule): +"((?:route|setting)\.[a-z0-9-]+)"`)
	found := 0
	err = filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (path == "examples" || path == "drivers" || path == "cli" || strings.HasPrefix(d.Name(), ".")) && path != "." {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range id.FindAllStringSubmatch(string(src), -1) {
			found++
			if !strings.Contains(string(guide), "\n### `"+m[1]+"`\n") {
				t.Errorf("%s: rule %s has no section in docs/site/guides/security-check.md (### `%s`)", path, m[1], m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found < 20 {
		t.Errorf("found %d rule names in the code, want the framework's (20 or more)", found)
	}
}
