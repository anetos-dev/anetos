---
title: Formats in catalogs
since: v0.3.0
---

# Formats in catalogs

The `format` and `relative` sections of a catalog, which `i18n.Date`,
`Time`, `DateTime`, `Format`, `Currency`, `Ago`, `Duration` and
`LanguageName` use, and the date pattern fields. The framework's English
values are in
[`i18n/locales/en.yaml`](../../../i18n/locales/en.yaml); a language's come
from `anetos lang:add` ([Numbers, dates and languages](../guides/formatting.md)).
A key a locale lacks comes from its parents, then English (the app's
`en` catalogs, then the framework's), never from a fallback locale in
another language.

## `format`

| Key | Value | English |
|---|---|---|
| `format.language` | The language's name in itself | `English` |
| `format.months` | 12 month names, January first, as in dates (`MMMM`) | `[January, …]` |
| `format.months_short` | 12 abbreviations (`MMM`) | `[Jan, …]` |
| `format.months_standalone` | Optional: 12 names on their own (`LLLL`), for languages whose month names change in a date (Russian `марта`, `март`) | — |
| `format.days` | 7 day names, Sunday first (`EEEE`) | `[Sunday, …]` |
| `format.days_short` | 7 abbreviations (`EEE`) | `[Sun, …]` |
| `format.periods` | Before and after noon (`a`) | `[AM, PM]` |
| `format.date.short` … `full` | Date patterns of the four styles | `M/d/yy`, `MMM d, y`, `MMMM d, y`, `EEEE, MMMM d, y` |
| `format.time.short` … `full` | Time patterns | `h:mm a`, `h:mm:ss a`, `h:mm:ss a z`, `h:mm:ss a zzzz` |
| `format.datetime.short` … `full` | How `DateTime` joins them: `{date}`, `{time}` | `{date}, {time}`; `{date} at {time}` (long, full) |
| `format.currency` | Where `Currency` puts `{symbol}` and `{amount}` (`{code}`: the ISO code) | `{symbol}{amount}` |
| `format.numbering` | Optional: a CLDR numbering system for numbers and dates, instead of the language's (`latn` for 0–9) | — |

A symbol of letters next to the amount gets a no-break space
(`BDT 1,250.00`), as CLDR does; a negative amount gets a minus sign in
front.

## Date pattern fields

Patterns follow [CLDR's date format patterns](https://unicode.org/reports/tr35/tr35-dates.html#Date_Field_Symbol_Table),
the subset below. Numbers use the locale's digits.

| Field | Shows | Example (Friday 16 January 2026, 02:04:05 in Dhaka) |
|---|---|---|
| `y`, `yyyy` | Year | `2026` |
| `yy` | Two-digit year | `26` |
| `M`, `MM` | Month number | `1`, `01` |
| `MMM`, `MMMM` | Month name, short and full | `Jan`, `January` |
| `MMMMM` | Its first letter | `J` |
| `LLL`, `LLLL`, `LLLLL` | The same, standalone forms | `Jan`, `January`, `J` |
| `d`, `dd` | Day of the month | `16`, `16` |
| `E`, `EE`, `EEE` | Short day name | `Fri` |
| `EEEE` | Day name | `Friday` |
| `EEEEE` | Its first letter | `F` |
| `a` | Period | `AM` |
| `h`, `hh` | Hour, 1–12 | `2`, `02` |
| `H`, `HH` | Hour, 0–23 | `2`, `02` |
| `m`, `mm` | Minute | `4`, `04` |
| `s`, `ss` | Second | `5`, `05` |
| `z` | Zone abbreviation | `+06` |
| `zzzz` | Zone's IANA name (the abbreviation for a zone without one) | `Asia/Dhaka` |
| `'text'` | Literal text | `'at'` |
| `''` | A single quote, in quoted text too | `h 'o''clock'` → `2 o'clock` |

Other letters are shown as they are: `Y` (week year), `G`, `Q`, `w`, `k`
and the rest of CLDR's fields aren't supported.

## `relative`

| Key | Value | English |
|---|---|---|
| `relative.now` | Under 45 seconds | `just now` |
| `relative.past` | A past time; `{time}` is the duration | `{time} ago` |
| `relative.future` | A future time | `in {time}` |
| `relative.units.second` … `year` | Plural messages for `{count}` units: `second`, `minute`, `hour`, `day`, `month`, `year` | `{one: "{count} hour", other: "{count} hours"}` |

`Duration` shows the largest unit, rounded to the nearest: from 45
seconds a minute, from 45 minutes an hour, from 22 hours a day, from 26
days a month (30 days), from 345 days a year (365 days). `DurationUp`
rounds up, for waits. `Ago` of a zero time is "".

## Limits

Numbers follow the CLDR data in `golang.org/x/text`, which lags CLDR
in places: Spanish groups four-digit numbers (`1.234`, CLDR `1234`),
French groups with a no-break space (U+00A0, CLDR U+202F), and the
`many` plural category of French and Spanish (`1 000 000 de jours`)
isn't used.
