// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/currency"
	"golang.org/x/text/language"
	xmessage "golang.org/x/text/message"
	"golang.org/x/text/number"
)

// Numeric is the types [Number] and [Currency] format.
type Numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// formatter formats numbers and dates for one locale.
type formatter struct {
	tag     language.Tag
	printer *xmessage.Printer
	digits  [10]string // the locale's digits, for dates
}

// formatChain returns the catalogs the format and relative sections of
// locale come from: the locale's and its parents', then the app's
// English and the core's, never another language's (a fallback locale
// in Bangla must not give a German visitor Bangla month names).
func (tr *Translator) formatChain(locale string) []*catalog {
	if c, ok := tr.fchains.Load(locale); ok {
		return c.([]*catalog)
	}
	tag, err := language.Parse(locale)
	if err != nil {
		tag = tr.def
	}
	out := tr.appChain(tag)
	for _, c := range tr.appChain(language.English) {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	out = append(out, tr.core)
	if tr.nfchains.Add(1) > maxChains {
		return out
	}
	c, _ := tr.fchains.LoadOrStore(locale, out)
	return c.([]*catalog)
}

// formatLookup returns the format or relative message of key for locale
// and the locale of its catalog, or nil.
func (tr *Translator) formatLookup(locale, key string) (*message, language.Tag) {
	for _, c := range tr.formatChain(locale) {
		if m := c.msgs[key]; m != nil {
			return m, c.tag
		}
	}
	return nil, language.Und
}

// formatText returns the text of the format message key, or def.
func (tr *Translator) formatText(locale, key, def string) string {
	if m, _ := tr.formatLookup(locale, key); m != nil && m.text != "" {
		return m.text
	}
	return def
}

// formatter returns the formatter of locale, made once. The numbering
// system is CLDR's for the language (Bangla digits for bn, Arabic-Indic
// for ar) unless the locale's catalog sets format.numbering ("latn").
func (tr *Translator) formatter(locale string) *formatter {
	if f, ok := tr.formatters.Load(locale); ok {
		return f.(*formatter)
	}
	tag, err := language.Parse(locale)
	if err != nil {
		tag = tr.def
	}
	ptag := tag
	if nu := tr.formatText(locale, "format.numbering", ""); nu != "" {
		if t, err := tag.SetTypeForKey("nu", nu); err == nil {
			ptag = t
		}
	}
	f := &formatter{tag: tag, printer: xmessage.NewPrinter(ptag)}
	for d := range 10 {
		f.digits[d] = f.printer.Sprint(number.Decimal(d))
	}
	if tr.nformatters.Add(1) > maxChains {
		return f
	}
	v, _ := tr.formatters.LoadOrStore(locale, f)
	return v.(*formatter)
}

// localDigits replaces the ASCII digits of s with the locale's.
func (f *formatter) localDigits(s string) string {
	if f.digits[0] == "0" {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteString(f.digits[r-'0'])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isZero reports whether a formatted number shows only zeros (-0.0001
// rounded), so it takes no minus sign.
func (f *formatter) isZero(s string) bool {
	for _, d := range f.digits[1:] {
		if strings.Contains(s, d) {
			return false
		}
	}
	return true
}

// decimal formats n with opts, without a minus sign when it rounds to 0.
func decimal[N Numeric](f *formatter, n N, opts ...number.Option) string {
	s := f.printer.Sprint(number.Decimal(n, opts...))
	if n < 0 && f.isZero(s) {
		return f.printer.Sprint(number.Decimal(0, opts...))
	}
	return s
}

// Number formats n for ctx's locale: grouped, with the locale's decimal
// separator and digits, and at most three decimal places:
//
//	i18n.Number(ctx, 1234567.891) // en: 1,234,567.891; bn: ১২,৩৪,৫৬৭.৮৯১; fr: 1 234 567,891
func Number[N Numeric](ctx context.Context, n N) string {
	return decimal(From(ctx).formatter(Locale(ctx)), n)
}

// maxPlaces bounds [Fixed]'s decimal places.
const maxPlaces = 20

// Fixed formats n for ctx's locale with exactly places decimal places
// (0 to 20), rounding half to even: Fixed(ctx, 2.5, 2) is "2.50" in
// English.
func Fixed[N Numeric](ctx context.Context, n N, places int) string {
	return decimal(From(ctx).formatter(Locale(ctx)), n, number.Scale(min(max(places, 0), maxPlaces)))
}

// Percent formats a ratio as a percentage for ctx's locale, rounded to a
// whole percent: Percent(ctx, 0.256) is "26%" in English, "26 %" in
// French.
func Percent[N Numeric](ctx context.Context, ratio N) string {
	return From(ctx).formatter(Locale(ctx)).printer.Sprint(number.Percent(ratio))
}

// Currency formats an amount of a currency (an ISO 4217 code: "USD",
// "BDT") for ctx's locale, with the currency's usual decimal places and
// symbol, where the catalog's format.currency pattern puts them
// ("{symbol}{amount}" in English):
//
//	i18n.Currency(ctx, 1234.5, "USD") // en: $1,234.50; fr: 1 234,50 $US; bn: ১,২৩৪.৫০ US$
//
// amount is in the currency's main unit: divide cents by 100. A
// negative amount has a minus sign in front ("-$5.00"); an amount that
// isn't a number (NaN, ±Inf), or a code that isn't a currency, is shown
// as a number followed by the code.
func Currency[N Numeric](ctx context.Context, amount N, code string) string {
	tr, locale := From(ctx), Locale(ctx)
	f := tr.formatter(locale)
	unit, err := currency.ParseISO(code)
	if v := float64(amount); err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return decimal(f, amount) + " " + code
	}
	scale, _ := currency.Standard.Rounding(unit)
	digits := decimal(f, amount, number.Scale(scale))
	sign := ""
	if amount < 0 {
		// the amount without its sign: format |amount|, in its own type
		// to stay exact (float64 when -amount overflows)
		abs := -amount
		if abs < 0 {
			digits = decimal(f, -float64(amount), number.Scale(scale))
		} else {
			digits = decimal(f, abs, number.Scale(scale))
		}
		if !f.isZero(digits) {
			sign = "-"
		}
	}
	pattern := tr.formatText(locale, "format.currency", "{symbol}{amount}")
	symbol := f.printer.Sprint(currency.Symbol(unit))
	// CLDR's currency spacing: a symbol of letters (BDT, US$ after the
	// amount) next to the digits gets a no-break space.
	if r, _ := utf8.DecodeLastRuneInString(symbol); strings.Contains(pattern, "{symbol}{amount}") && !unicode.IsSymbol(r) {
		symbol += " "
	} else if r, _ := utf8.DecodeRuneInString(symbol); strings.Contains(pattern, "{amount}{symbol}") && !unicode.IsSymbol(r) {
		symbol = " " + symbol
	}
	return sign + fill(pattern, []any{"symbol", symbol, "amount", digits, "code", unit.String()})
}

// formatCount formats Plural's {count} for locale.
func (tr *Translator) formatCount(locale string, n int) string {
	return tr.formatter(locale).printer.Sprint(number.Decimal(n))
}

// numberArg formats a value given to a message as a number, when it is
// one (validation arguments: "at least 3"): the locale's digits and
// decimal separator, without grouping (max:2030 is no amount), exactly
// for integers.
func numberArg(f *formatter, s string) (string, bool) {
	digits := strings.TrimPrefix(s, "-")
	whole, frac, dot := strings.Cut(digits, ".")
	if whole == "" || strings.Trim(whole, "0123456789") != "" || strings.Trim(frac, "0123456789") != "" || (dot && frac == "") {
		return s, false
	}
	if !dot {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return f.printer.Sprint(number.Decimal(i, number.NoSeparator())), true
		}
		return f.localDigits(s), true // beyond int64: digit by digit
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s, false
	}
	return f.printer.Sprint(number.Decimal(v, number.Scale(len(frac)), number.NoSeparator())), true
}

// LocalNumber returns s, a number written in ASCII ("3", "2.5"), in
// ctx's locale's digits and decimal separator ("৩" in Bangla, "2,5" in
// French), without grouping, or s unchanged when it isn't one. Package
// validate uses it for the numbers of size rules.
func LocalNumber(ctx context.Context, s string) string {
	out, _ := numberArg(From(ctx).formatter(Locale(ctx)), s)
	return out
}
