// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"strings"
	"unicode"
)

// splitStatements splits SQL at the semicolons that end statements. It
// skips quoted strings and identifiers (with PostgreSQL E” strings and
// MySQL backslash escapes), comments (nested on PostgreSQL, # on MySQL),
// PostgreSQL dollar-quoted bodies, and BEGIN … END blocks of triggers and
// procedures (including CASE … END inside them). Statements with nothing
// but comments are dropped.
func splitStatements(sql, dialect string) []string {
	var out []string
	start, depth := 0, 0
	add := func(end int) {
		if stmt := strings.TrimSpace(sql[start:end]); hasCode(stmt, dialect) {
			out = append(out, stmt)
		}
		start = end + 1
	}
	for i := 0; i < len(sql); {
		if j := skipNonCode(sql, i, dialect); j > i {
			i = j
			continue
		}
		c := sql[i]
		switch {
		case c == ';' && depth == 0:
			add(i)
			i++
		case isWordStart(sql, i):
			j := i
			for j < len(sql) && isWordByte(sql[j]) {
				j++
			}
			switch strings.ToUpper(sql[i:j]) {
			case "BEGIN":
				if !beginsTransaction(sql[j:]) {
					depth++
				}
			case "CASE":
				depth++
			case "END":
				// END IF / END LOOP / … close blocks this scanner doesn't
				// open; END and END CASE close one it did.
				switch strings.ToUpper(nextWord(sql[j:])) {
				case "IF", "LOOP", "WHILE", "REPEAT", "FOR":
				default:
					depth = max(depth-1, 0)
				}
			}
			i = j
		default:
			i++
		}
	}
	if start < len(sql) {
		add(len(sql))
	}
	return out
}

// skipNonCode returns the index after a quoted section or comment starting
// at i, or i if there is none.
func skipNonCode(s string, i int, dialect string) int {
	c := s[i]
	switch {
	case (c == 'E' || c == 'e') && dialect == "postgres" && i+1 < len(s) && s[i+1] == '\'' && (i == 0 || !isWordByte(s[i-1])):
		return skipQuoted(s, i+1, '\'', true)
	case c == '\'' || c == '"' || c == '`':
		return skipQuoted(s, i, c, dialect == "mysql" && c != '`')
	case c == '-' && strings.HasPrefix(s[i:], "--") && (dialect != "mysql" || i+2 >= len(s) || s[i+2] == ' ' || s[i+2] == '\t' || s[i+2] == '\n' || s[i+2] == '\r'),
		c == '#' && dialect == "mysql":
		if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
			return i + j
		}
		return len(s)
	case c == '/' && strings.HasPrefix(s[i:], "/*"):
		depth := 0
		for j := i; j < len(s)-1; j++ {
			switch {
			case s[j] == '/' && s[j+1] == '*':
				if depth == 0 || dialect == "postgres" {
					depth++
				}
				j++
			case s[j] == '*' && s[j+1] == '/':
				depth--
				j++
				if depth == 0 {
					return j + 1
				}
			}
		}
		return len(s)
	case c == '$' && dialect == "postgres":
		if tag := dollarTag(s[i:]); tag != "" {
			if j := strings.Index(s[i+len(tag):], tag); j >= 0 {
				return i + len(tag) + j + len(tag)
			}
			return len(s)
		}
	}
	return i
}

// hasCode reports whether stmt has anything but comments and space.
func hasCode(stmt, dialect string) bool {
	for i := 0; i < len(stmt); {
		if j := skipNonCode(stmt, i, dialect); j > i {
			if stmt[i] == '\'' || stmt[i] == '"' || stmt[i] == '`' || stmt[i] == '$' || stmt[i] == 'E' || stmt[i] == 'e' {
				return true // a quoted section is code
			}
			i = j
			continue
		}
		if !unicode.IsSpace(rune(stmt[i])) && stmt[i] != ';' {
			return true
		}
		i++
	}
	return false
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

func isWordStart(s string, i int) bool {
	return isWordByte(s[i]) && (i == 0 || !isWordByte(s[i-1]))
}

func nextWord(s string) string {
	s = strings.TrimLeft(s, " \t\r\n")
	j := 0
	for j < len(s) && isWordByte(s[j]) {
		j++
	}
	return s[:j]
}

// beginsTransaction reports whether a BEGIN (followed by rest) starts a
// transaction rather than a block.
func beginsTransaction(rest string) bool {
	trimmed := strings.TrimLeft(rest, " \t\r\n")
	if trimmed == "" || trimmed[0] == ';' {
		return true
	}
	switch strings.ToUpper(nextWord(trimmed)) {
	case "TRANSACTION", "WORK", "DEFERRED", "IMMEDIATE", "EXCLUSIVE":
		return true
	}
	return false
}

func skipQuoted(s string, i int, q byte, backslash bool) int {
	for j := i + 1; j < len(s); j++ {
		if backslash && s[j] == '\\' && j+1 < len(s) {
			j++
			continue
		}
		if s[j] == q {
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(s)
}

func dollarTag(s string) string {
	for j := 1; j < len(s); j++ {
		c := s[j]
		switch {
		case c == '$':
			return s[:j+1]
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || (j > 1 && c >= '0' && c <= '9'):
		default:
			return ""
		}
	}
	return ""
}
