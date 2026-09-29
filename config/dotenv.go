// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"io"
	"strings"
)

// ParseDotenv parses .env content into a [Map].
//
// Supported syntax:
//
//	# comment                    full-line comment
//	KEY=value                    unquoted; trailing " # comment" is stripped
//	export KEY=value             optional "export " prefix
//	KEY="line\nnext ${OTHER}"    double quotes: escapes \n \r \t \" \\ \$, ${VAR} expansion, may span lines
//	KEY='raw ${NOT_EXPANDED}'    single quotes: literal, may span lines
//	KEY=                         empty value
//
// ${NAME} is expanded in unquoted and double-quoted values, from keys defined
// earlier in the same content, then from fallback (which may be nil).
// Unknown names expand to the empty string. A malformed reference such as
// "${" or "${A:-default}" is an error. $NAME without braces is left as-is,
// so values such as passwords containing "$" are not mangled.
//
// A UTF-8 byte order mark is ignored and CRLF line endings are treated as LF.
// When a key appears more than once, the last value wins.
func ParseDotenv(r io.Reader, fallback Source) (Map, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return parseDotenv(string(data), nil, fallback)
}

// parseDotenv resolves ${NAME} from over, then from the content's own keys,
// then from under. Load passes the process environment as over, so the value
// a reference sees matches the value the application sees.
func parseDotenv(src string, over, under Source) (Map, error) {
	src = strings.TrimPrefix(src, "\uFEFF")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	p := &dotenvParser{src: src, line: 1, out: Map{}, over: over, under: under}
	if err := p.parse(); err != nil {
		return nil, err
	}
	return p.out, nil
}

type dotenvParser struct {
	src   string
	pos   int
	line  int
	out   Map
	over  Source
	under Source
}

func (p *dotenvParser) errorf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", p.line, fmt.Sprintf(format, args...))
}

func (p *dotenvParser) parse() error {
	for p.pos < len(p.src) {
		p.skipBlank()
		if p.pos >= len(p.src) {
			return nil
		}
		switch p.src[p.pos] {
		case '\n':
			p.pos++
			p.line++
			continue
		case '#':
			p.skipToEOL()
			continue
		}
		if err := p.parseAssignment(); err != nil {
			return err
		}
	}
	return nil
}

func (p *dotenvParser) parseAssignment() error {
	if rest := p.src[p.pos:]; strings.HasPrefix(rest, "export") && len(rest) > 6 && isBlank(rest[6]) {
		p.pos += 6
		p.skipBlank()
	}

	start := p.pos
	for p.pos < len(p.src) && isKeyChar(p.src[p.pos], p.pos == start) {
		p.pos++
	}
	key := p.src[start:p.pos]
	if key == "" {
		return p.errorf("expected a variable name")
	}

	p.skipBlank()
	if p.pos >= len(p.src) || p.src[p.pos] != '=' {
		return p.errorf("expected '=' after %q", key)
	}
	p.pos++
	afterEq := p.pos
	p.skipBlank()

	var value string
	var err error
	if p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == '#' && p.pos > afterEq: // "KEY= # comment" means an empty value
			p.skipToEOL()
		case c == '"':
			value, err = p.parseDoubleQuoted()
		case c == '\'':
			value, err = p.parseSingleQuoted()
		default:
			value, err = p.expand(p.parseUnquoted())
		}
	}
	if err != nil {
		return err
	}
	p.out[key] = value
	return nil
}

func (p *dotenvParser) parseUnquoted() string {
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] != '\n' {
		// " #" starts an inline comment.
		if p.src[p.pos] == '#' && p.pos > start && isBlank(p.src[p.pos-1]) {
			v := strings.TrimRight(p.src[start:p.pos], " \t")
			p.skipToEOL()
			return v
		}
		p.pos++
	}
	return strings.TrimRight(p.src[start:p.pos], " \t\r")
}

func (p *dotenvParser) parseDoubleQuoted() (string, error) {
	startLine := p.line
	p.pos++ // opening quote
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			return b.String(), p.finishQuoted()
		case c == '\\' && p.pos+1 < len(p.src):
			p.pos++
			switch e := p.src[p.pos]; e {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '"', '\\', '$':
				b.WriteByte(e)
			default:
				if e == '\n' {
					p.line++
				}
				b.WriteByte('\\')
				b.WriteByte(e)
			}
			p.pos++
		case c == '$' && strings.HasPrefix(p.src[p.pos:], "${"):
			name, n, ok := parseRef(p.src[p.pos:])
			if !ok {
				return "", p.errorf("invalid variable reference; use ${NAME}")
			}
			b.WriteString(p.lookupVar(name))
			p.pos += n
		default:
			if c == '\n' {
				p.line++
			}
			b.WriteByte(c)
			p.pos++
		}
	}
	p.line = startLine
	return "", p.errorf("unterminated double-quoted value")
}

func (p *dotenvParser) parseSingleQuoted() (string, error) {
	startLine := p.line
	p.pos++ // opening quote
	end := strings.IndexByte(p.src[p.pos:], '\'')
	if end < 0 {
		return "", p.errorf("unterminated single-quoted value")
	}
	v := p.src[p.pos : p.pos+end]
	p.line = startLine + strings.Count(v, "\n")
	p.pos += end + 1
	return v, p.finishQuoted()
}

// finishQuoted allows only blanks and an optional comment after a closing quote.
func (p *dotenvParser) finishQuoted() error {
	p.skipBlank()
	if p.pos >= len(p.src) || p.src[p.pos] == '\n' {
		return nil
	}
	if p.src[p.pos] == '#' {
		p.skipToEOL()
		return nil
	}
	return p.errorf("unexpected characters after closing quote")
}

// expand replaces ${NAME} references in an unquoted value.
func (p *dotenvParser) expand(v string) (string, error) {
	if !strings.Contains(v, "${") {
		return v, nil
	}
	var b strings.Builder
	for {
		i := strings.Index(v, "${")
		if i < 0 {
			b.WriteString(v)
			return b.String(), nil
		}
		name, n, ok := parseRef(v[i:])
		if !ok {
			return "", p.errorf("invalid variable reference; use ${NAME}")
		}
		b.WriteString(v[:i])
		b.WriteString(p.lookupVar(name))
		v = v[i+n:]
	}
}

// parseRef parses a "${NAME}" reference at the start of s, returning the
// name and the number of bytes consumed.
func parseRef(s string) (name string, n int, ok bool) {
	i := 2 // past "${"
	for i < len(s) && isKeyChar(s[i], i == 2) {
		i++
	}
	if i == 2 || i >= len(s) || s[i] != '}' {
		return "", 0, false
	}
	return s[2:i], i + 1, true
}

func (p *dotenvParser) lookupVar(name string) string {
	if p.over != nil {
		if v, ok := p.over.Lookup(name); ok {
			return v
		}
	}
	if v, ok := p.out[name]; ok {
		return v
	}
	if p.under != nil {
		if v, ok := p.under.Lookup(name); ok {
			return v
		}
	}
	return ""
}

func (p *dotenvParser) skipBlank() {
	for p.pos < len(p.src) && (isBlank(p.src[p.pos]) || p.src[p.pos] == '\r') {
		p.pos++
	}
}

func (p *dotenvParser) skipToEOL() {
	for p.pos < len(p.src) && p.src[p.pos] != '\n' {
		p.pos++
	}
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

func isKeyChar(c byte, first bool) bool {
	switch {
	case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9', c == '.':
		return !first
	}
	return false
}
