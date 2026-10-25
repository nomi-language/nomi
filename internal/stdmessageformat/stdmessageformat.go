// Package stdmessageformat is std/message_format's host implementation: it
// formats a message's compiled table (mf2.Table) in a locale with its
// arguments, over the program's CLDR data (stdlocale.Data): the locale's
// plural rules select a variant and its number symbols and patterns write
// the numbers (stdformat.Number).
//
// The text is the requested locale's when the table has one, else the
// nearest of its CLDR parents' (stdlocale.Fallbacks), else the source
// locale's. Two pseudo-locales need no catalog: `en-XA` shows the source
// text accented, bracketed and padded a third longer, and `ar-XB` shows it
// mirrored right to left, as Android's pseudo-locales do.
//
// A noun argument (mf2.NounTable) is written and selected in the locale of
// the message's text: its forms and gender there, or in the nearest locale
// on that locale's chain that translates the noun, else the source's.
package stdmessageformat

import (
	"strconv"
	"strings"

	"golang.org/x/text/language"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdformat"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdplural"
	"github.com/nomi-language/nomi/rt"
)

// The pseudo-locales.
const (
	PseudoAccents = "en-XA"
	PseudoMirror  = "ar-XB"
)

// Fallback is what a table that does not decode formats to: MessageFormat
// 2's fallback representation of a message it cannot format.
const Fallback = "{\ufffd}"

// The argument kinds std/message's Argument.kind writes.
const (
	argInt = iota
	argFloat
	argDecimal
	argString
	argVariant
	argLocalized
	argNoun
	argList
	argDate
	argMoney
	argTime
	argDateTime
)

// FFIFormat is `message_format.format`.
func FFIFormat(fr *rt.Frame, table, locale string, catalogs []string, kinds []int64, numbers, keys, texts []string) string {
	return Format(stdlocale.Data(fr), runtimeOf(fr), table, locale, catalogs, kinds, numbers, keys, texts)
}

// Format formats table in locale over d, with the catalogs Catalog.load
// encoded, nearest first, and r's installed translations.
func Format(d *cldrdata.Data, r *Runtime, table, locale string, catalogs []string, kinds []int64, numbers, keys, texts []string) string {
	t, err := mf2.DecodeTable(table)
	if err != nil {
		// Not a table a `message` declaration wrote (`message.compiled`
		// called by hand): MessageFormat 2's fallback for a message it
		// cannot format, as formatting never fails.
		return Fallback
	}
	src := sources{rtm: r}
	for _, c := range catalogs {
		if cat := decodeCatalog(c); cat != nil {
			src.catalogs = append(src.catalogs, cat)
		}
	}
	pseudo := ""
	if locale == PseudoAccents || locale == PseudoMirror {
		pseudo = locale
	}
	textLocale := t.Source
	numberLocale := locale
	var m *mf2.Message
	if pseudo != "" {
		numberLocale = t.Source
	} else {
		for _, tag := range stdlocale.Fallbacks(d, locale) {
			if found, ok := src.message(table, t, tag); ok {
				textLocale, m = tag, found
				break
			}
		}
	}
	if m == nil {
		if _, m, err = mf2.TableMessage(table, t.Source); err != nil {
			return Fallback
		}
	}
	args := make(map[string]mf2.Value, len(t.Params))
	for i, p := range t.Params {
		if i >= len(kinds) {
			break
		}
		args[p.Name] = value(kinds[i], numbers[i], keys[i], texts[i])
	}
	env := &environment{d: d, src: src, locale: numberLocale, text: textLocale, pseudo: pseudo}
	env.plurals = pluralLocale(d, numberLocale, textLocale)
	env.rtl = pseudo == PseudoMirror || pseudo == "" && RightToLeft(locale)
	out := mf2.Format(m, args, env)
	if pseudo == PseudoAccents {
		out = "[" + out + pad(out) + "]"
	}
	return out
}

// sources are where a text in a locale comes from, nearest first: the
// catalogs Message.format was handed, in its order, then the program's
// own tables, then the translations Locale.load installed.
type sources struct {
	catalogs []*Translations
	rtm      *Runtime
}

// message is the message table, t, in tag, if a source has it.
func (s sources) message(table string, t *mf2.Table, tag string) (*mf2.Message, bool) {
	for _, c := range s.catalogs {
		if c.Locale == tag {
			if m, ok := c.Message(t.Name); ok {
				return m, true
			}
		}
	}
	if _, ok := t.Texts[tag]; ok {
		_, m, err := mf2.TableMessage(table, tag)
		return m, err == nil
	}
	return s.rtm.installedIn(tag).Message(t.Name)
}

// nounIn is a noun's gender and forms in one locale.
type nounIn struct {
	gender string
	form   func(kase string) (*mf2.Message, bool)
}

// noun is the noun table text, t, in tag, if a source has it.
func (s sources) noun(text string, t *mf2.NounTable, tag string) (nounIn, bool) {
	from := func(c *Translations, n NounForms) nounIn {
		return nounIn{gender: n.Gender, form: func(kase string) (*mf2.Message, bool) {
			f, ok := n.Forms[kase]
			if !ok {
				return nil, false
			}
			return c.Parse(f)
		}}
	}
	for _, c := range s.catalogs {
		if c.Locale == tag {
			if n, ok := c.Noun(t.Name); ok {
				return from(c, n), true
			}
		}
	}
	if _, ok := t.Forms[tag]; ok {
		return compiledNoun(text, t, tag), true
	}
	if c := s.rtm.installedIn(tag); c != nil {
		if n, ok := c.Noun(t.Name); ok {
			return from(c, n), true
		}
	}
	return nounIn{}, false
}

func compiledNoun(text string, t *mf2.NounTable, tag string) nounIn {
	return nounIn{gender: t.Genders[tag], form: func(kase string) (*mf2.Message, bool) { return mf2.NounForm(text, tag, kase) }}
}

func value(kind int64, number, key, text string) mf2.Value {
	v := mf2.Value{Number: number, Key: key, Text: text}
	switch kind {
	case argInt:
		v.Kind = mf2.KindInt
	case argFloat:
		v.Kind = mf2.KindFloat
		if f, err := strconv.ParseFloat(number, 64); err == nil {
			v.Number = strconv.FormatFloat(f, 'f', -1, 64)
		}
	case argDecimal:
		v.Kind = mf2.KindDecimal
	case argString:
		v.Kind = mf2.KindString
	case argVariant:
		v.Kind = mf2.KindEnum
	case argNoun:
		v.Kind = mf2.KindNoun
	case argList:
		v.Kind = mf2.KindList
	case argDate:
		v.Kind = mf2.KindDate
	case argMoney:
		v.Kind = mf2.KindMoney
	case argTime:
		v.Kind = mf2.KindTime
	case argDateTime:
		v.Kind = mf2.KindDateTime
	default:
		v.Kind = mf2.KindOther
	}
	return v
}

type environment struct {
	d   *cldrdata.Data
	src sources
	// locale writes numbers, text is the locale of the message's text,
	// which nouns are written in, and plurals the locale whose rules select
	// its plural categories (pluralLocale).
	locale, text, plurals string
	pseudo                string
	rtl                   bool
}

// pluralLocale is the locale whose plural rules select a text in text read
// in locale: the reader's, when the text is its own language's (pt read in
// pt-PT selects by pt-PT's rules), else the text's, whose arms are keyed by
// its own language's categories (English read in an unlisted French).
func pluralLocale(d *cldrdata.Data, locale, text string) string {
	for _, tag := range stdlocale.Fallbacks(d, locale) {
		if tag == text {
			return locale
		}
	}
	return text
}

// FFINoun is `message_format.noun_raw`: the noun table's nominative for one
// thing in locale, which a noun's Localize writes.
func FFINoun(fr *rt.Frame, table, locale string) string {
	return Noun(stdlocale.Data(fr), runtimeOf(fr), table, locale)
}

// Noun writes the noun table's nominative for one thing in locale over d
// and r's installed translations.
func Noun(d *cldrdata.Data, r *Runtime, table, locale string) string {
	t, err := mf2.DecodeNoun(table)
	if err != nil {
		return Fallback
	}
	env := &environment{d: d, src: sources{rtm: r}, locale: locale, text: locale, plurals: locale}
	if locale == PseudoAccents || locale == PseudoMirror {
		env.pseudo, env.locale, env.text, env.plurals = locale, t.Source, t.Source, t.Source
	}
	env.rtl = env.pseudo == PseudoMirror || env.pseudo == "" && RightToLeft(locale)
	out := env.Noun(table, mf2.Nominative, "1")
	if env.pseudo == PseudoAccents {
		out = "[" + out + pad(out) + "]"
	}
	return out
}

// nounAt is the locale whose forms of t a text in e.text writes, and the
// noun there: the nearest on its chain that a source translates t into,
// else the source.
func (e *environment) nounAt(text string, t *mf2.NounTable) (string, nounIn) {
	if e.pseudo == "" {
		for _, tag := range stdlocale.Fallbacks(e.d, e.text) {
			if tag == t.Source {
				break
			}
			if n, ok := e.src.noun(text, t, tag); ok {
				return tag, n
			}
		}
	}
	return t.Source, compiledNoun(text, t, t.Source)
}

func (e *environment) Noun(noun, kase, count string) string {
	t, err := mf2.DecodeNoun(noun)
	if err != nil {
		return Fallback
	}
	at, n := e.nounAt(noun, t)
	source := compiledNoun(noun, t, t.Source)
	// The compiler, and Catalog.load, hold every noun to every case a text
	// asks for; a form that is not there (a noun table made by hand) is
	// written as the locale's nominative, else the source's.
	for _, try := range []struct {
		locale string
		noun   nounIn
		kase   string
	}{{at, n, kase}, {at, n, mf2.Nominative}, {t.Source, source, kase}, {t.Source, source, mf2.Nominative}} {
		if m, ok := try.noun.form(try.kase); ok {
			v := mf2.Value{Kind: mf2.KindInt, Number: count}
			v.Text = e.Number(count, mf2.NumberFormat{Grouping: "auto", MinInteger: 1, MaxFraction: -1})
			form := *e
			form.plurals = pluralLocale(e.d, e.locale, try.locale)
			return mf2.Format(m, map[string]mf2.Value{mf2.NounParam: v}, &form)
		}
	}
	return Fallback
}

func (e *environment) Gender(noun string) string {
	t, err := mf2.DecodeNoun(noun)
	if err != nil {
		return ""
	}
	_, n := e.nounAt(noun, t)
	return n.gender
}

func (e *environment) Plural(n string, ordinal bool) string {
	return stdplural.Rules(e.d, e.plurals, ordinal).CategoryOf(n)
}

func (e *environment) Number(n string, f mf2.NumberFormat) string {
	return stdformat.Number(e.d, e.locale, n, f.Percent, f.Grouping, f.MinInteger, f.MinFraction, f.MaxFraction)
}

// Money writes an amount as std/money's Money.format does, in the reader's
// locale: currency is the Currency's literal text, `EUR` or `BTC:8`.
func (e *environment) Money(amount, currency, display string) string {
	code, digitsText, custom := strings.Cut(currency, ":")
	digits := cldrdata.DefaultCurrencyDigits
	if custom {
		if n, err := strconv.Atoi(digitsText); err == nil {
			digits = n
		}
	} else if n, ok := e.d.CurrencyDigits(code); ok {
		digits = n
	}
	shown := map[string]int{"symbol": 0, "narrowSymbol": 1, "code": 2, "name": 3}[display]
	return stdformat.Money(e.d, e.locale, amount, code, digits, !custom, shown)
}

// Date writes a calendar value, given as its ISO 8601 text, as std/calendar's
// format and format_fields do, in the reader's locale.
func (e *environment) Date(v mf2.Value, r mf2.DateRequest) string {
	fields, ok := CivilFields(v.Kind, v.Key)
	if !ok {
		return "{" + v.Key + "}"
	}
	if r.Skeleton != "" {
		return stdformat.FormatSkeleton(e.d, e.locale, fields, r.Skeleton)
	}
	ds, ts := r.DateStyle, r.TimeStyle
	if ds == "" {
		ds = "-"
	}
	if ts == "" {
		ts = "-"
	}
	return stdformat.FormatStyle(e.d, e.locale, fields, ds+"/"+ts)
}

// CivilFields are the fields std/calendar passes its formatter for a
// calendar value of kind k written as ISO 8601 text: `2026-10-20` (a Date),
// `09:30:00.25` (a Time), `2026-10-20T09:30:00` (a NaiveDateTime) or
// `2026-10-20T09:30:00+02:00[Europe/Warsaw]` (a DateTime).
func CivilFields(k mf2.Kind, text string) ([]int64, bool) {
	f := make([]int64, 9)
	var date, clock string
	switch k {
	case mf2.KindDate:
		date = text
	case mf2.KindTime:
		clock = text
	case mf2.KindDateTime:
		var found bool
		if date, clock, found = strings.Cut(text, "T"); !found {
			return nil, false
		}
	default:
		return nil, false
	}
	if date != "" {
		neg := strings.HasPrefix(date, "-")
		parts := strings.Split(strings.TrimLeft(date, "+-"), "-")
		if len(parts) != 3 {
			return nil, false
		}
		for i, p := range parts {
			n, err := strconv.ParseInt(p, 10, 64)
			if err != nil {
				return nil, false
			}
			f[i] = n
		}
		if neg {
			f[0] = -f[0]
		}
		f[8] |= 1
	}
	if clock != "" {
		if i := strings.IndexByte(clock, '['); i >= 0 {
			clock = clock[:i]
		}
		offset := ""
		if i := strings.IndexAny(clock, "+-Z"); i >= 0 {
			clock, offset = clock[:i], clock[i:]
		}
		hms, frac, _ := strings.Cut(clock, ".")
		parts := strings.Split(hms, ":")
		if len(parts) < 2 {
			return nil, false
		}
		for i, p := range parts {
			n, err := strconv.ParseInt(p, 10, 64)
			if err != nil || i > 2 {
				return nil, false
			}
			f[3+i] = n
		}
		if frac != "" {
			frac = (frac + "000000000")[:9]
			f[6], _ = strconv.ParseInt(frac, 10, 64)
		}
		f[8] |= 2
		if offset != "" {
			f[8] |= 4
			if offset != "Z" {
				sign := int64(1)
				if offset[0] == '-' {
					sign = -1
				}
				hm := strings.Split(offset[1:], ":")
				h, _ := strconv.ParseInt(hm[0], 10, 64)
				var m int64
				if len(hm) > 1 {
					m, _ = strconv.ParseInt(hm[1], 10, 64)
				}
				f[7] = sign * (h*3600 + m*60)
			}
		}
	}
	if f[8]&1 == 0 {
		f[0], f[1], f[2] = 1970, 1, 1
	}
	return f, true
}

func (e *environment) RTL() bool { return e.rtl }

func (e *environment) Text(s string) string {
	switch e.pseudo {
	case PseudoAccents:
		return accented(s)
	case PseudoMirror:
		if s == "" {
			return s
		}
		return "‮" + s + "‬"
	}
	return s
}

// rtlScripts are the scripts written right to left.
var rtlScripts = map[string]bool{
	"Arab": true, "Hebr": true, "Thaa": true, "Syrc": true, "Nkoo": true, "Adlm": true,
	"Rohg": true, "Mand": true, "Samr": true, "Mend": true, "Yezi": true,
}

// RightToLeft reports whether locale is written right to left: its script,
// written or the likeliest for its language, is.
func RightToLeft(locale string) bool {
	t, err := language.Parse(locale)
	if err != nil {
		return false
	}
	s, _ := t.Script()
	return rtlScripts[s.String()]
}

// accents gives each ASCII letter the accented form Android's en-XA uses.
var accents = map[rune]rune{
	'a': 'å', 'b': 'ƀ', 'c': 'ç', 'd': 'ð', 'e': 'é', 'f': 'ƒ', 'g': 'ĝ', 'h': 'ĥ', 'i': 'î', 'j': 'ĵ',
	'k': 'ķ', 'l': 'ļ', 'm': 'ɱ', 'n': 'ñ', 'o': 'ö', 'p': 'þ', 'q': 'ǫ', 'r': 'ŕ', 's': 'š', 't': 'ţ',
	'u': 'û', 'v': 'ṽ', 'w': 'ŵ', 'x': 'ẋ', 'y': 'ý', 'z': 'ž',
	'A': 'Å', 'B': 'Ɓ', 'C': 'Ç', 'D': 'Ð', 'E': 'É', 'F': 'Ƒ', 'G': 'Ĝ', 'H': 'Ĥ', 'I': 'Î', 'J': 'Ĵ',
	'K': 'Ķ', 'L': 'Ļ', 'M': 'Ṁ', 'N': 'Ñ', 'O': 'Ö', 'P': 'Þ', 'Q': 'Ǫ', 'R': 'Ŕ', 'S': 'Š', 'T': 'Ţ',
	'U': 'Û', 'V': 'Ṽ', 'W': 'Ŵ', 'X': 'Ẋ', 'Y': 'Ý', 'Z': 'Ž',
}

func accented(s string) string {
	var b strings.Builder
	for _, r := range s {
		if a, ok := accents[r]; ok {
			r = a
		}
		b.WriteRune(r)
	}
	return b.String()
}

// padWords lengthen en-XA's text, as translations into longer languages
// do.
var padWords = []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}

// pad is about a third of out's length in words, with a space before.
func pad(out string) string {
	want := (len([]rune(out)) + 2) / 3
	var b strings.Builder
	for i := 0; b.Len() < want; i++ {
		b.WriteString(" " + padWords[i%len(padWords)])
	}
	return b.String()
}
