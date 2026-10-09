// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"regexp"

	"github.com/a-h/templ"
)

// The classes and styles of the tracker's own components
// (tracker.templ).

// priorityClass is the class of a priority's level: the levels other
// than normal have a color.
func priorityClass(level string) string {
	switch level {
	case "low", "high", "urgent":
		return "priority " + level
	}
	return "priority"
}

// labelStyle is a label's color, as the --label variable its chip's
// style reads; anything but #rrggbb is left out (the default grey).
func labelStyle(color string) templ.SafeCSS {
	if !hexColor.MatchString(color) {
		return ""
	}
	return templ.SafeCSS("--label:" + color)
}

// hexColor is a color as labels store it.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
