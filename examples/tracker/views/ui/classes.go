// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"slices"
	"strings"
)

// The starter theme's classes (public/static/app.css) of the variants,
// sizes and tones.

// class is a button's classes: a link needs "button", a <button> is
// styled as the primary one without any.
func (v Variant) class(link bool, sizes []Size) string {
	var c []string
	if link {
		c = append(c, "button")
	}
	switch v {
	case Secondary:
		c = append(c, "secondary")
	case Danger:
		c = append(c, "danger")
	case Ghost:
		c = append(c, "ghost")
	}
	if has(sizes, Small) {
		c = append(c, "small")
	}
	if has(sizes, FullWidth) {
		c = append(c, "full")
	}
	return strings.Join(c, " ")
}

// toneClass is the class of a tone added to a badge's or a message's.
func (t Tone) toneClass() string {
	switch t {
	case Success:
		return " success"
	case Warning:
		return " warning"
	case Error:
		return " danger"
	case Info:
		return " info"
	}
	return ""
}

// flashClass is the class of a tone added to a message's: as a badge's,
// but "error" for Error, which stylesheets from before v0.5 style too.
func (t Tone) flashClass() string {
	if t == Error {
		return " error"
	}
	return t.toneClass()
}

// has reports whether sizes has s.
func has(sizes []Size, s Size) bool { return slices.Contains(sizes, s) }
