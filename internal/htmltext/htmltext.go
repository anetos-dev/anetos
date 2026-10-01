// SPDX-License-Identifier: Apache-2.0

// Package htmltext turns HTML into readable plain text, for the text part
// of emails written in HTML: paragraphs and line breaks are kept, lists
// get "- " or numbers, links are followed by their URL in parentheses,
// images by their alt text; scripts, styles and the head are dropped.
// It is forgiving rather than a full HTML parser: it reads tags,
// comments and entities, and doesn't fix broken markup.
package htmltext

import (
	"html"
	"strconv"
	"strings"
	"unicode"
)

// Convert returns the text of the HTML document or fragment s.
func Convert(s string) string {
	c := &conv{}
	for i := 0; i < len(s); {
		if s[i] != '<' {
			j := strings.IndexByte(s[i:], '<')
			if j < 0 {
				j = len(s) - i
			}
			c.text(html.UnescapeString(s[i : i+j]))
			i += j
			continue
		}
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			j := strings.Index(s[i+4:], "-->")
			if j < 0 {
				return c.done()
			}
			i += 4 + j + 3
			continue
		case strings.HasPrefix(s[i:], "<!") || strings.HasPrefix(s[i:], "<?"):
			j := strings.IndexByte(s[i:], '>')
			if j < 0 {
				return c.done()
			}
			i += j + 1
			continue
		}
		t, end, st := parseTag(s, i)
		switch st {
		case notTag: // a lone "<"
			c.text("<")
			i++
			continue
		case unclosed: // as browsers do, the rest is in the tag
			return c.done()
		}
		i = end
		if !t.closing && skipped[t.name] && !t.selfClosing {
			i = skipElement(s, i, t.name)
			continue
		}
		c.tag(t)
	}
	return c.done()
}

// skipped are the elements whose content isn't text.
var skipped = map[string]bool{"head": true, "script": true, "style": true, "title": true, "template": true}

// paragraphs get a blank line around them; lines a line break.
var (
	paragraphs = map[string]bool{"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"ul": true, "ol": true, "dl": true, "table": true, "blockquote": true, "pre": true, "hr": true, "figure": true}
	lines = map[string]bool{"div": true, "br": true, "tr": true, "li": true, "dt": true, "dd": true, "section": true,
		"article": true, "header": true, "footer": true, "nav": true, "aside": true, "main": true, "address": true,
		"form": true, "center": true, "figcaption": true, "caption": true, "tbody": true, "thead": true, "tfoot": true}
)

type tag struct {
	name                 string
	closing, selfClosing bool
	attrs                map[string]string
}

type status int

const (
	ok       status = iota
	notTag          // "<" isn't followed by a tag name
	unclosed        // the input ends inside the tag
)

// parseTag parses the tag starting at s[i] ('<').
func parseTag(s string, i int) (t tag, end int, st status) {
	j := i + 1
	if j < len(s) && s[j] == '/' {
		t.closing = true
		j++
	}
	start := j
	for j < len(s) && (isLetter(s[j]) || j > start && isDigit(s[j])) {
		j++
	}
	if j == start {
		return t, 0, notTag
	}
	t.name = strings.ToLower(s[start:j])
	for j < len(s) {
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		if j >= len(s) {
			return t, 0, unclosed
		}
		switch s[j] {
		case '>':
			return t, j + 1, ok
		case '/':
			t.selfClosing = true
			j++
			continue
		}
		// An attribute: name, then maybe = and a value.
		ns := j
		for j < len(s) && !isSpace(s[j]) && s[j] != '=' && s[j] != '>' && s[j] != '/' {
			j++
		}
		name := strings.ToLower(s[ns:j])
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		var value string
		if j < len(s) && s[j] == '=' {
			j++
			for j < len(s) && isSpace(s[j]) {
				j++
			}
			if j < len(s) && (s[j] == '"' || s[j] == '\'') {
				q := s[j]
				k := strings.IndexByte(s[j+1:], q)
				if k < 0 {
					return t, 0, unclosed
				}
				value = s[j+1 : j+1+k]
				j += k + 2
			} else {
				vs := j
				for j < len(s) && !isSpace(s[j]) && s[j] != '>' {
					j++
				}
				value = s[vs:j]
			}
		}
		if name != "" {
			if t.attrs == nil {
				t.attrs = map[string]string{}
			}
			t.attrs[name] = html.UnescapeString(value)
		}
	}
	return t, 0, unclosed
}

// skipElement returns the index after the end tag of name, from i, or
// the input's end if there is none.
func skipElement(s string, i int, name string) int {
	for {
		j := strings.Index(s[i:], "</")
		if j < 0 {
			return len(s)
		}
		i += j + 2
		if e := i + len(name); e <= len(s) && strings.EqualFold(s[i:e], name) && (e == len(s) || !isLetter(s[e]) && !isDigit(s[e])) {
			k := strings.IndexByte(s[e:], '>')
			if k < 0 {
				return len(s)
			}
			return e + k + 1
		}
	}
}

func isLetter(b byte) bool { return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' }
func isDigit(b byte) bool  { return '0' <= b && b <= '9' }
func isSpace(b byte) bool  { return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' }

// conv writes the text.
type conv struct {
	b     strings.Builder
	space bool // a space is pending
	nl    int  // line breaks pending
	pre   int  // inside pre elements
	lists []int
	links []link
	cells int // cells written in the current row
}

type link struct {
	href  string
	start int
}

func (c *conv) text(t string) {
	for _, r := range t {
		if c.pre > 0 {
			if r == '\n' {
				c.nl++
				c.space = false
				continue
			}
			c.flush()
			c.b.WriteRune(r)
			continue
		}
		if unicode.IsSpace(r) {
			c.space = true
			continue
		}
		c.flush()
		c.b.WriteRune(r)
	}
}

// flush writes the pending line breaks or space.
func (c *conv) flush() {
	switch {
	case c.b.Len() == 0:
	case c.nl > 0:
		c.b.WriteString(strings.Repeat("\n", c.nl))
	case c.space && !strings.HasSuffix(c.b.String(), "\n"):
		c.b.WriteByte(' ')
	}
	c.nl, c.space = 0, false
}

// breakLines asks for at least n line breaks before the next text.
func (c *conv) breakLines(n int) {
	if c.pre > 0 {
		c.nl += n
		return
	}
	c.nl = max(c.nl, n)
	c.space = false
}

// maxIndent caps the indentation of nested lists.
const maxIndent = 8

func (c *conv) tag(t tag) {
	nested := (t.name == "ul" || t.name == "ol") && (len(c.lists) > 1 || len(c.lists) == 1 && !t.closing)
	switch {
	case nested:
		c.breakLines(1)
	case paragraphs[t.name]:
		c.breakLines(2)
	case lines[t.name]:
		if t.name != "br" || !t.closing {
			c.breakLines(1)
		}
	}
	switch t.name {
	case "pre":
		if t.closing {
			c.pre = max(c.pre-1, 0)
		} else {
			c.pre++
		}
	case "ul", "ol":
		switch {
		case t.closing:
			if len(c.lists) > 0 {
				c.lists = c.lists[:len(c.lists)-1]
			}
		case t.name == "ul":
			c.lists = append(c.lists, -1)
		default:
			c.lists = append(c.lists, 0)
		}
	case "li":
		if t.closing {
			return
		}
		c.flush()
		c.b.WriteString(strings.Repeat("  ", min(max(len(c.lists)-1, 0), maxIndent)))
		if n := len(c.lists); n > 0 && c.lists[n-1] >= 0 {
			c.lists[n-1]++
			c.b.WriteString(strconv.Itoa(c.lists[n-1]) + ". ")
		} else {
			c.b.WriteString("- ")
		}
	case "tr":
		c.cells = 0
	case "td", "th":
		if !t.closing {
			if c.cells > 0 {
				c.space = true
			}
			c.cells++
		}
	case "hr":
		c.flush()
		c.b.WriteString("----")
		c.breakLines(2)
	case "img":
		if alt := strings.TrimSpace(t.attrs["alt"]); alt != "" {
			c.text(alt)
		}
	case "a":
		if !t.closing {
			c.links = append(c.links, link{href: strings.TrimSpace(t.attrs["href"]), start: c.b.Len()})
			return
		}
		if len(c.links) == 0 {
			return
		}
		l := c.links[len(c.links)-1]
		c.links = c.links[:len(c.links)-1]
		if l.start > c.b.Len() || !showLink(l.href, strings.TrimSpace(c.b.String()[l.start:])) {
			return
		}
		c.b.WriteString(" (" + l.href + ")")
	}
}

// showLink reports whether a link's URL should follow its text.
func showLink(href, text string) bool {
	lower := strings.ToLower(href)
	switch {
	case href == "", strings.HasPrefix(href, "#"), strings.HasPrefix(lower, "javascript:"):
		return false
	case text == href, strings.HasPrefix(lower, "mailto:") && text == href[len("mailto:"):]:
		return false
	}
	return true
}

func (c *conv) done() string {
	lines := strings.Split(c.b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}
