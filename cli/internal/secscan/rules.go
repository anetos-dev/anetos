// SPDX-License-Identifier: Apache-2.0

// Package secscan has the anetos tool's layers of the security check
// (design D329, D330): rules over the project's source code, a search
// for secrets written in its files, govulncheck, and checks of the
// running site. The tool passes what
// they find to the app's security:check command, which reports it with
// the app's own findings.
package secscan

import "strings"

// Level is how thorough the check is, as the app's --level.
type Level int

const (
	// Basic rules find what is almost always a mistake.
	Basic Level = iota
	// Advanced rules add those that find what needs a look.
	Advanced
)

// Rule describes a rule for the app's report, as the app's
// --findings file has it.
type Rule struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	OWASP   string `json:"owasp"`
	Docs    string `json:"docs"`

	level Level
}

// Finding is what a rule found, as the app's --findings file has it
// (anetos.SecurityFinding's JSON).
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // note, warning or problem
	Where    string `json:"where"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

// The OWASP Top 10:2025 categories the rules use.
const (
	owaspAccess    = "A01:2025 Broken Access Control"
	owaspConfig    = "A02:2025 Security Misconfiguration"
	owaspSupply    = "A03:2025 Software Supply Chain Failures"
	owaspCrypto    = "A04:2025 Cryptographic Failures"
	owaspInjection = "A05:2025 Injection"
	owaspAuth      = "A07:2025 Authentication Failures"
	owaspIntegrity = "A08:2025 Software or Data Integrity Failures"
)

// docs is the guide's section of a rule.
func docs(id string) string {
	return "https://docs.anetos.dev/guides/security-check/#" + strings.ReplaceAll(id, ".", "")
}

// rules are the tool's rules, by ID.
var rules = map[string]Rule{
	"source.sql":         {Summary: "SQL built from request data", OWASP: owaspInjection},
	"source.xss":         {Summary: "Request data written as HTML without escaping", OWASP: owaspInjection},
	"source.redirect":    {Summary: "Redirects to a URL from the request", OWASP: owaspAccess},
	"source.exec":        {Summary: "Commands run with request data", OWASP: owaspInjection},
	"source.tls":         {Summary: "TLS connections that don't check certificates", OWASP: owaspCrypto},
	"source.rand":        {Summary: "Secrets made with math/rand", OWASP: owaspCrypto},
	"source.secret":      {Summary: "Secrets written in the project's files", OWASP: owaspCrypto},
	"source.path":        {Summary: "File paths from the request", OWASP: owaspAccess, level: Advanced},
	"source.ssrf":        {Summary: "Requests to a URL from the request", OWASP: owaspAccess, level: Advanced},
	"source.mass-assign": {Summary: "Requests bound into a model", OWASP: owaspIntegrity, level: Advanced},
	"source.owner":       {Summary: "Records loaded by ID without an access check", OWASP: owaspAccess, level: Advanced},
	"dependency.vuln":    {Summary: "Known vulnerabilities in the code the app uses", OWASP: owaspSupply},
	"live.headers":       {Summary: "Security headers the site doesn't send", OWASP: owaspConfig},
	"live.cookie":        {Summary: "Cookies without Secure, HttpOnly or SameSite", OWASP: owaspAuth},
	"live.tls":           {Summary: "Certificates that don't verify, old TLS versions", OWASP: owaspCrypto},
	"live.https":         {Summary: "The site served over plain HTTP", OWASP: owaspCrypto},
	"live.exposed":       {Summary: "Settings, the git repository or debug endpoints served", OWASP: owaspConfig},
	"live.debug":         {Summary: "Stack traces in error pages", OWASP: owaspConfig},
	"live.unchecked":     {Summary: "The running site couldn't be checked", OWASP: owaspConfig},
	"live.cors":          {Summary: "CORS that allows any origin", OWASP: owaspConfig, level: Advanced},
	"live.trace":         {Summary: "TRACE answered", OWASP: owaspConfig, level: Advanced},
	"live.version":       {Summary: "Server versions in headers", OWASP: owaspConfig, level: Advanced},
	"live.cert-expiry":   {Summary: "Certificates about to expire", OWASP: owaspCrypto, level: Advanced},
}

// RuleList is the rules that run at level, with their IDs and docs, as
// the app's --findings file has them.
func RuleList(level Level) []Rule { return ruleList(level) }

// ruleList is the rules that run at level, with their IDs and docs.
func ruleList(level Level) []Rule {
	var out []Rule
	for id, r := range rules {
		if r.level <= level {
			r.ID, r.Docs = id, docs(id)
			out = append(out, r)
		}
	}
	return out
}
