// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

func TestParseDotenv(t *testing.T) {
	input := `# comment line
APP_NAME=blog
export APP_ENV=development
  SPACED   =   value with spaces
INLINE=abc # trailing comment
HASH=abc#not-a-comment
EMPTY=
EMPTY_COMMENT= # only a comment
DQ="hello\nworld \"quoted\" \$HOME \\ end"
SQ='raw ${APP_NAME} \n'
EXP=${APP_NAME}-app
DQ_EXP="${APP_NAME}/${FROM_FALLBACK}/${UNKNOWN}"
DOLLAR=pa$$word$NOEXPAND
MULTI="line1
line2"
SQ_MULTI='a
b'
WIN=crlf` + "\r" + `
DUP=first
DUP=second
dotted.key=ok
`
	got, err := ParseDotenv(strings.NewReader(input), Map{"FROM_FALLBACK": "fb", "APP_NAME": "ignored-fallback"})
	if err != nil {
		t.Fatalf("ParseDotenv: %v", err)
	}

	want := map[string]string{
		"APP_NAME":      "blog",
		"APP_ENV":       "development",
		"SPACED":        "value with spaces",
		"INLINE":        "abc",
		"HASH":          "abc#not-a-comment",
		"EMPTY":         "",
		"EMPTY_COMMENT": "",
		"DQ":            "hello\nworld \"quoted\" $HOME \\ end",
		"SQ":            `raw ${APP_NAME} \n`,
		"EXP":           "blog-app",
		"DQ_EXP":        "blog/fb/",
		"DOLLAR":        "pa$$word$NOEXPAND",
		"MULTI":         "line1\nline2",
		"SQ_MULTI":      "a\nb",
		"WIN":           "crlf",
		"DUP":           "second",
		"dotted.key":    "ok",
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s = %q (present=%v), want %q", k, g, ok, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys, want %d: %v", len(got), len(want), got)
	}
}

func TestParseDotenvErrors(t *testing.T) {
	tests := []struct {
		name, input, wantErr string
	}{
		{"missing equals", "A=1\nNOPE\n", "line 2: expected '=' after \"NOPE\""},
		{"bad key", "=value", "line 1: expected a variable name"},
		{"digit first", "1ABC=x", "line 1: expected a variable name"},
		{"unterminated double", "A=1\nB=\"open\nstill open", "line 2: unterminated double-quoted value"},
		{"unterminated single", "B='open", "line 1: unterminated single-quoted value"},
		{"junk after quote", `B="x" junk`, "line 1: unexpected characters after closing quote"},
		{"unterminated expansion", `B="${OPEN"`, "line 1: invalid variable reference; use ${NAME}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDotenv(strings.NewReader(tt.input), nil)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseDotenvLineNumbersAfterMultiline(t *testing.T) {
	input := "A=\"x\ny\"\nB='p\nq'\nBROKEN\n"
	_, err := ParseDotenv(strings.NewReader(input), nil)
	if err == nil || !strings.HasPrefix(err.Error(), "line 5:") {
		t.Fatalf("err = %v, want line 5", err)
	}
}

func FuzzParseDotenv(f *testing.F) {
	for _, s := range []string{"A=1", "A=\"x\\n\"", "A='x'", "export A=${B}", "A= # c", "A=\"${"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = ParseDotenv(strings.NewReader(s), nil) // must not panic or hang
	})
}

// --- regression tests from the F2–F4 code review ---

func TestParseDotenvInvalidReferences(t *testing.T) {
	for _, input := range []string{
		"A=\"cost ${\"\nB=\"${HOME_X}\"\nC=3", // must not swallow the following lines
		"A=${A:-default}",
		"A=\"${}\"",
		"A=x${",
	} {
		_, err := ParseDotenv(strings.NewReader(input), nil)
		if err == nil || !strings.Contains(err.Error(), "line 1: invalid variable reference") {
			t.Errorf("%q: err = %v", input, err)
		}
	}
}

func TestParseDotenvLineCountWithEscapedNewline(t *testing.T) {
	input := "A=1\nB=\"x\\\ny\"\nC=2\nBROKEN\n"
	_, err := ParseDotenv(strings.NewReader(input), nil)
	if err == nil || !strings.HasPrefix(err.Error(), "line 5:") {
		t.Fatalf("err = %v, want line 5", err)
	}
}

func TestParseDotenvBOMAndCRLF(t *testing.T) {
	got, err := ParseDotenv(strings.NewReader("\xEF\xBB\xBFA=1\r\nB=\"l1\r\nl2\"\r\nexport\tC=3\r\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "1" || got["B"] != "l1\nl2" || got["C"] != "3" {
		t.Errorf("got %q", got)
	}
}

func TestExpansionPrefersProcessEnvironment(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "HOST=localhost\nURL=http://${HOST}:8080\n")
	src, err := Load(LoadOptions{Dir: dir, OSEnv: Map{"HOST": "prod-db"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := src.Lookup("URL"); got != "http://prod-db:8080" {
		t.Errorf("URL = %q, want the process environment's HOST", got)
	}
}
