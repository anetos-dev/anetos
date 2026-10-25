// SPDX-License-Identifier: Apache-2.0

package secscan

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"anetos.dev/anetos/cli/internal/testmod"
)

// Keys in the forms the search knows, made when the tests run: written
// whole, they'd stop a push to GitHub (push protection) as real keys do.
const (
	fakeStripe = "sk_" + "live_" + "4eC39HqLyjWDarjtT1zdp7dc"
	fakeAWS    = "AKIA" + "IOSFODNN7EXAMPL" // + E: AWS's example key
)

func TestSecrets(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config/keys.go", "package config\n\nconst stripeKey = \""+fakeStripe+"\"\nvar apiToken = \"q8Zt3LmP0vW7xKc2RbN9\"\nvar name = \"a long ordinary name here\"\n")
	// Code that looks like an assignment of a secret, and isn't.
	write("app/code.go", "package app\n\n"+
		"secret := sha256.Sum256([]byte(key))\n"+
		"tokenHash := base64.RawURLEncoding.EncodeToString(sum[:])\n"+
		"secretKey := hkdf.Expand(sha256.New, master, info2)\n"+
		"apiKey := cfg.Services.Stripe2Key\n"+
		"csrfToken := web.CSRFToken(ctx1)\n"+
		"tokens := strings.Fields(input2)\n"+
		"PasswordHash: hash.Argon2id(p),\n"+
		"cfg := map[string]string{\"tokenizer\": \"bert-base-uncased-v2\"}\n"+
		"user := User{PasswordHash: \"$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy\"}\n"+
		"inviteToken := \"3f2b8c1e-9a4d-4e6f-8b2a-1c3d5e7f9a0b\"\n"+
		"awsDocs := \""+fakeAWS+"E\"\n")
	write("deploy/values.yaml", "db:\n  password: \"Xr7pQ2mZ9wL4tK8v\"\n  user: anetos\nclient_secret: your-client-secret-here\n")
	write(".env", "APP_KEY=base64:c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0MTI=\nAWS="+fakeAWS+"A\n")
	write("main_test.go", "package main\n\nconst key = \""+fakeAWS+"A\"\n")
	write("node_modules/x/index.js", "const k = '"+fakeAWS+"A'\n")
	write("keys/id_rsa.example", "-----BEGIN RSA PRIVATE KEY-----\n")
	got, err := secrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"config/keys.go:3":      "problem",
		"config/keys.go:4":      "warning",
		"deploy/values.yaml:2":  "warning",
		"keys/id_rsa.example:1": "problem",
	}
	seen := map[string]string{}
	for _, f := range got {
		seen[f.File+":"+strconv.Itoa(f.Line)] = f.Severity
		if strings.Contains(f.Message, "4eC39HqLyjWD") || strings.Contains(f.Message, "Xr7pQ2mZ9w") {
			t.Errorf("the secret isn't masked: %s", f.Message)
		}
	}
	for at, sev := range want {
		if seen[at] != sev {
			t.Errorf("%s: %q, want %s (all: %v)", at, seen[at], sev, seen)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("findings: %v", seen)
	}
}

func TestTemplLine(t *testing.T) {
	dir := t.TempDir()
	gen := filepath.Join(dir, "page_templ.go")
	g := "package views\n\nfunc a() {\n\treturn templ.Error{Err: err, FileName: `views/page.templ`, Line: 4, Col: 2}\n}\n\nfunc b() {\n\t_ = templ.Raw(p.Bio).Render(ctx, w)\n}\n"
	if err := os.WriteFile(gen, []byte(g), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package views\n\ntempl Page(p P) {\n\t<h1>{ p.Name }</h1>\n\t@templ.Raw(p.Head)\n\t@templ.Raw(p.Bio)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "page.templ"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	start := strings.Index(g, "templ.Raw(p.Bio)")
	file, line := templLine(gen, start, start+len("templ.Raw(p.Bio)"), 8)
	if filepath.Base(file) != "page.templ" || line != 6 {
		t.Errorf("templLine = %s:%d, want page.templ:6", file, line)
	}
}

func TestVulnFindings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\ngo 1.26\n\nrequire (\n\tgolang.org/x/net v0.50.0\n\tgithub.com/x/y v1.0.0\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stream := `{"config":{"protocol_version":"v1.0.0"}}
{"osv":{"id":"GO-2026-0001","summary":"Panic in http2 frames."}}
{"osv":{"id":"GO-2026-0002","summary":"Bad parse."}}
{"finding":{"osv":"GO-2026-0001","fixed_version":"v0.60.0","trace":[{"module":"golang.org/x/net","version":"v0.50.0","package":"golang.org/x/net/http2"}]}}
{"finding":{"osv":"GO-2026-0001","fixed_version":"v0.60.0","trace":[{"module":"golang.org/x/net","version":"v0.50.0","package":"golang.org/x/net/http2","function":"ReadFrame","receiver":"*Framer","position":{"filename":"http2/frame.go","line":500}},{"module":"golang.org/x/net","version":"v0.50.0","package":"golang.org/x/net/http2","function":"serve","position":{"filename":"http2/server.go","line":90}},{"module":"example.com/shop","package":"example.com/shop/app","function":"Serve","position":{"filename":"app/serve.go","line":12}},{"module":"example.com/shop","package":"example.com/shop","function":"main","position":{"filename":"main.go","line":7}}]}}
{"finding":{"osv":"GO-2026-0002","fixed_version":"v1.27.3","trace":[{"module":"stdlib","version":"v1.27.1","package":"net/url"}]}}
{"finding":{"osv":"GO-2026-0003","trace":[{"module":"github.com/x/y","version":"v1.0.0"}]}}
`
	got := vulnFindings(strings.NewReader(stream), dir)
	if len(got) != 2 {
		t.Fatalf("findings: %+v", got)
	}
	called, imported := got[0], got[1]
	if called.Severity != "problem" || called.File != "app/serve.go" || called.Line != 12 || !strings.Contains(called.Message, "Framer.ReadFrame") ||
		!strings.Contains(called.Message, "go get golang.org/x/net@v0.60.0") || !strings.Contains(called.Message, "Panic in http2 frames") {
		t.Errorf("called: %+v", called)
	}
	if imported.Severity != "note" || imported.File != "go.mod" || imported.Line != 3 || !strings.Contains(imported.Message, "Go 1.27.3 or later") {
		t.Errorf("imported: %+v", imported)
	}
}

// TestRulesDocumented checks that every rule has its section in the
// guide, where reports link to.
func TestRulesDocumented(t *testing.T) {
	guide, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "site", "guides", "security-check.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ruleList(Advanced) {
		if !strings.Contains(string(guide), "\n### `"+r.ID+"`\n") {
			t.Errorf("rule %s has no section in docs/site/guides/security-check.md", r.ID)
		}
		if !strings.HasSuffix(r.Docs, "#"+strings.ReplaceAll(r.ID, ".", "")) || r.Summary == "" || r.OWASP == "" {
			t.Errorf("rule %+v", r)
		}
	}
	if n := len(ruleList(Basic)); n != 15 {
		t.Errorf("%d basic rules, want 15", n)
	}
}

// TestScan runs the rules on a module that uses the real core: a
// handler's SQL from the request, a key in a file, and an advanced rule
// left out at the basic level.
func TestScan(t *testing.T) {
	if testing.Short() {
		t.Skip("loads packages")
	}
	dir := t.TempDir()
	core, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	goMod, goSum, err := testmod.Files(core)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": goMod, "go.sum": goSum,
		"main.go": `package main

import (
	"net/http"
	"os"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
)

type Post struct {
	db.Model
	Title string
}

func search(c *web.Ctx) error {
	_, err := db.Raw[Post](c, "SELECT * FROM posts WHERE title = '"+c.Query("q")+"'")
	return err
}

func file(w http.ResponseWriter, r *http.Request) {
	_, _ = os.ReadFile(r.FormValue("name"))
}

func main() {}
`,
		"config/stripe.yaml": "key: " + fakeStripe + "\n",
	}
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Scan(t.Context(), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range rep.Findings {
		got[f.Rule] = f.Severity + " " + f.Where + " " + f.File + ":" + strconv.Itoa(f.Line)
	}
	want := map[string]string{"source.sql": "problem main.search main.go:17", "source.secret": "problem config/stripe.yaml config/stripe.yaml:1"}
	if len(got) != len(want) {
		t.Errorf("findings: %v", got)
	}
	for r, w := range want {
		if got[r] != w {
			t.Errorf("%s: %q, want %q", r, got[r], w)
		}
	}
	rep, err = Scan(t.Context(), Options{Dir: dir, Level: Advanced})
	if err != nil || !slices.ContainsFunc(rep.Findings, func(f Finding) bool { return f.Rule == "source.path" }) {
		t.Errorf("advanced: %v %+v", err, rep.Findings)
	}
	// A project that doesn't build is an error.
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package main\n\nvar x int = \"text\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(t.Context(), Options{Dir: dir}); err == nil || !strings.Contains(err.Error(), "doesn't build") {
		t.Errorf("a broken project: %v", err)
	}
}
