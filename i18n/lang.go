// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"context"

	"golang.org/x/text/language"
)

// rtlScripts are the scripts written right to left.
var rtlScripts = map[string]bool{
	"Arab": true, "Hebr": true, "Thaa": true, "Syrc": true, "Nkoo": true,
	"Adlm": true, "Rohg": true, "Mand": true, "Samr": true,
}

// Dir returns the writing direction of ctx's locale, for <html dir>:
// "rtl" for languages written in a right-to-left script (Arabic, Hebrew,
// Persian, Urdu…), else "ltr".
//
//	<html lang={ i18n.Locale(ctx) } dir={ i18n.Dir(ctx) }>
func Dir(ctx context.Context) string { return DirOf(Locale(ctx)) }

// DirOf returns the writing direction of locale: "rtl" or "ltr".
func DirOf(locale string) string {
	tag, err := language.Parse(locale)
	if err != nil {
		return "ltr"
	}
	if script, _ := tag.Script(); rtlScripts[script.String()] {
		return "rtl"
	}
	return "ltr"
}

// LanguageName returns locale's name in its own language, from its
// catalog's format.language ("বাংলা" for bn, "English" for en), for a
// language switcher or to tell an AI model which language to answer in;
// the locale itself when its catalogs don't name it. Only the locale's
// own catalogs (and its parents') are used, never the fallback's.
func LanguageName(ctx context.Context, locale string) string {
	tr := From(ctx)
	tag, err := language.Parse(locale)
	if err != nil {
		return locale
	}
	cats := tr.appChain(tag)
	if isEnglish(tag) {
		cats = append(cats, tr.core)
	}
	if m, ok := find(cats, "format.language"); ok && m.text != "" {
		return m.text
	}
	return tag.String()
}
