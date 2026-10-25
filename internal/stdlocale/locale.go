// Package stdlocale is std/locale's host implementation: parsing and
// canonicalizing BCP 47 tags, POSIX locale values, CLDR fallback chains and
// language matching.
//
// Tag syntax, canonicalization and matching come from
// golang.org/x/text/language. Its BCP 47 handling is a stable algorithm over
// small tables; its CLDR 32 likely-subtag data is used only to tell whether
// a script is a language's likely one, which decides a few fallback steps.
// Parent locales come from Nomi's own CLDR data (internal/cldrdata).
//
// A Nomi `Locale` is its canonical tag, so every function here takes and
// returns tags as strings, and a string reaching one from std/locale is
// always a tag that Parse accepted.
package stdlocale

import (
	"errors"
	"strings"

	"golang.org/x/text/language"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/localefiles"
	"github.com/nomi-language/nomi/rt"
)

// The Kind values of a LocaleAttempt, in std/locale's `Error`
// declaration order; 0 is success.
const (
	KindOK int64 = iota
	KindEmpty
	KindUnderscore
	KindMalformed
	KindUnknownSubtag
)

// LocaleAttempt carries `Result<Locale, locale.Error>` across the boundary:
// the canonical tag when Kind is 0, else the error's kind and its fields.
// Detail is the underscore error's suggestion, the malformed text or the unknown subtag.
type LocaleAttempt struct {
	Value  string
	Kind   int64
	Detail string
}

// FFIParse is `Locale.parse`, and it also backs the “Locale`…` “ literal.
func FFIParse(tag string) LocaleAttempt { return parse(tag) }

func parse(s string) LocaleAttempt {
	if s == "" {
		return LocaleAttempt{Kind: KindEmpty}
	}
	if strings.Contains(s, "_") {
		suggestion := strings.ReplaceAll(s, "_", "-")
		if t, err := language.Parse(suggestion); err == nil {
			suggestion = t.String()
		}
		return LocaleAttempt{Kind: KindUnderscore, Detail: suggestion}
	}
	t, err := language.Parse(s)
	if err != nil {
		var unknown language.ValueError
		if errors.As(err, &unknown) {
			return LocaleAttempt{Kind: KindUnknownSubtag, Detail: unknown.Subtag()}
		}
		return LocaleAttempt{Kind: KindMalformed, Detail: s}
	}
	return LocaleAttempt{Value: t.String()}
}

// posixModifiers maps the POSIX `@modifier`s that name a script or a
// variant onto their BCP 47 subtag. Any other modifier (`@euro`) says
// nothing a tag carries and is dropped.
var posixModifiers = map[string]string{
	"latin":      "Latn",
	"cyrillic":   "Cyrl",
	"devanagari": "Deva",
	"valencia":   "valencia",
}

// FFIFromPosix is `Locale.from_posix`: a POSIX locale value such as `LANG`
// holds (`language[_territory][.codeset][@modifier]`) as a Locale. `C` and
// `POSIX`, with or without a codeset, are `und`: the portable locale names
// no language.
func FFIFromPosix(value string) LocaleAttempt {
	if value == "" {
		return LocaleAttempt{Kind: KindEmpty}
	}
	rest, modifier, _ := strings.Cut(value, "@")
	rest, _, _ = strings.Cut(rest, ".")
	if rest == "C" || rest == "POSIX" {
		return LocaleAttempt{Value: "und"}
	}
	lang, territory, hasTerritory := strings.Cut(rest, "_")
	parts := []string{lang}
	sub := posixModifiers[strings.ToLower(modifier)]
	isScript := len(sub) == 4 && sub != "valencia"
	if isScript {
		parts = append(parts, sub)
	}
	if hasTerritory {
		parts = append(parts, territory)
	}
	if sub != "" && !isScript {
		parts = append(parts, sub)
	}
	a := parse(strings.Join(parts, "-"))
	if a.Kind != KindOK {
		return LocaleAttempt{Kind: KindMalformed, Detail: value}
	}
	return a
}

// mustParse reads a tag std/locale already canonicalized.
func mustParse(tag string) language.Tag {
	t, err := language.Parse(tag)
	if err != nil {
		return language.Und
	}
	return t
}

// FFILanguage is `Locale.language`: the language subtag, `und` when the tag
// names none.
func FFILanguage(tag string) string {
	b, _, _ := mustParse(tag).Raw()
	return b.String()
}

// FFIScript is `Locale.script`: the script subtag the tag spells, if any.
func FFIScript(tag string) *string {
	_, s, _ := mustParse(tag).Raw()
	if s.String() == "Zzzz" {
		return nil
	}
	out := s.String()
	return &out
}

// FFIRegion is `Locale.region`: the region subtag the tag spells, if any.
func FFIRegion(tag string) *string {
	_, _, r := mustParse(tag).Raw()
	if r.String() == "ZZ" {
		return nil
	}
	out := r.String()
	return &out
}

// parts is a tag's language, script, region and variants, without
// extensions or private use: the part of a tag locale data is keyed by.
type parts struct {
	lang, script, region string
	variants             []string
}

func partsOf(t language.Tag) parts {
	b, s, r := t.Raw()
	p := parts{lang: b.String()}
	if s.String() != "Zzzz" {
		p.script = s.String()
	}
	if r.String() != "ZZ" {
		p.region = r.String()
	}
	for _, v := range t.Variants() {
		p.variants = append(p.variants, v.String())
	}
	return p
}

func (p parts) String() string {
	out := []string{p.lang}
	if p.script != "" {
		out = append(out, p.script)
	}
	if p.region != "" {
		out = append(out, p.region)
	}
	return strings.Join(append(out, p.variants...), "-")
}

// likelyScript is the script CLDR's likely subtags give tag, or "".
func likelyScript(tag string) string {
	t, err := language.Parse(tag)
	if err != nil {
		return ""
	}
	s, _ := t.Script()
	if s.String() == "Zzzz" {
		return ""
	}
	return s.String()
}

// parentOf is a key's parent under CLDR's inheritance (UTS #35 Part 1,
// "Parent Locales"), and false at root. explicit is the parentLocales table
// that applies: the main one for locale data, the plurals component's for
// plural rules. insertScript is the main data's extra step: a tag with a
// region and no script whose region implies a script other than the
// language's own (zh-TW is Traditional, zh is Simplified) inherits from the
// language with that script, as CLDR's zh-TW is an alias of zh-Hant-TW.
// nonlikely is the main data's rule that a script other than the
// language's likely one inherits from root (sr-Latn is not sr).
func parentOf(root *cldrdata.Data, p parts, explicit func(string) (string, bool), insertScript, nonlikely bool) (parts, bool) {
	if par, ok := explicit(p.String()); ok {
		if par == "und" || par == "root" {
			return parts{}, false
		}
		return partsOf(mustParse(par)), true
	}
	switch {
	case len(p.variants) > 0:
		p.variants = p.variants[:len(p.variants)-1]
	case p.region != "":
		if insertScript && p.script == "" {
			if s := likelyScript(p.lang + "-" + p.region); s != "" && s != likelyScript(p.lang) {
				return parts{lang: p.lang, script: s}, true
			}
		}
		p.region = ""
	case p.script != "":
		if nonlikely && root.NonlikelyScriptParent != "" && p.script != likelyScript(p.lang) {
			return parts{}, false
		}
		p.script = ""
	default:
		return parts{}, false
	}
	if p.lang == "und" && p.script == "" && p.region == "" && len(p.variants) == 0 {
		return parts{}, false
	}
	return p, true
}

// chain is tag, then the tag without extensions when that differs, then its
// ancestors, nearest first, root excluded.
func chain(d *cldrdata.Data, tag string, explicit func(string) (string, bool), insertScript, nonlikely bool) []string {
	t := mustParse(tag)
	out := []string{}
	if t != language.Und {
		out = append(out, t.String())
	}
	p := partsOf(t)
	if s := p.String(); s != t.String() && s != "und" {
		out = append(out, s)
	}
	for {
		next, ok := parentOf(d, p, explicit, insertScript, nonlikely)
		if !ok {
			return out
		}
		p = next
		out = append(out, p.String())
	}
}

// Fallbacks is the chain locale data is looked up along: tag itself, then
// its CLDR parents, nearest first, root excluded.
func Fallbacks(d *cldrdata.Data, tag string) []string {
	return chain(d, tag, d.Parent, true, true)
}

// PluralFallbacks is the chain plural rules are looked up along, which
// follows the plurals component's parent locales rather than the main ones.
func PluralFallbacks(d *cldrdata.Data, tag string) []string {
	return chain(d, tag, d.PluralParent, false, false)
}

// CollationFallbacks is the chain collation tailorings are looked up along:
// the collations component's parent locales, else the main ones short of
// root (nb's rules are no's), else the tag less its last subtag, so
// zh-Hant takes zh's stroke order and yue zh-Hant's.
func CollationFallbacks(d *cldrdata.Data, tag string) []string {
	explicit := func(t string) (string, bool) {
		if p, ok := d.CollationParent(t); ok {
			return p, true
		}
		if p, ok := d.Parent(t); ok && p != "und" && p != "root" {
			return p, true
		}
		return "", false
	}
	return chain(d, tag, explicit, true, false)
}

// Data is the CLDR data the machine running fr carries: its program's
// locales (cldrdata.From). A nil frame reads cldrdata.Default.
func Data(fr *rt.Frame) *cldrdata.Data {
	if fr == nil {
		return cldrdata.Default()
	}
	return cldrdata.From(fr.Context())
}

// Extension is the value of a Unicode extension key in tag (`nu` in
// `ar-EG-u-nu-latn` is `latn`), or "".
func Extension(tag, key string) string {
	t, err := language.Parse(tag)
	if err != nil {
		return ""
	}
	return t.TypeForKey(key)
}

// FFIFallbacks is `Locale.fallbacks`.
func FFIFallbacks(fr *rt.Frame, tag string) []string { return Fallbacks(Data(fr), tag) }

// FFIAvailable is `Locale.available`: the locales the program's nomi.toml
// lists in `[i18n].locales`, in its order, or every locale the data has when
// it lists none.
func FFIAvailable(fr *rt.Frame) []string {
	d := Data(fr)
	if tags, ok := d.Available(); ok {
		return tags
	}
	var out []string
	for _, tag := range d.Locales() {
		if tag != "und" {
			out = append(out, tag)
		}
	}
	return out
}

// FFINegotiate is `Locale.negotiate`: the available locale that best
// matches the requested ones, in order of preference, by CLDR language
// matching, or fallback when none matches well enough.
func FFINegotiate(requested, available []string, fallback string) string {
	if len(requested) == 0 || len(available) == 0 {
		return fallback
	}
	supported := make([]language.Tag, len(available))
	for i, a := range available {
		supported[i] = mustParse(a)
	}
	desired := make([]language.Tag, len(requested))
	for i, r := range requested {
		desired[i] = mustParse(r)
	}
	_, index, confidence := language.NewMatcher(supported).Match(desired...)
	if confidence == language.No {
		return fallback
	}
	return available[index]
}

// FFILoad is `locale_load_raw`, Locale.load: "" when tag is ready, else
// why not (localefiles.Load).
func FFILoad(fr *rt.Frame, tag string) string {
	if fr == nil {
		return ""
	}
	store := cldrdata.StoreFrom(fr.Context())
	if store == nil {
		return ""
	}
	return localefiles.Load(store, localefiles.From(fr.Context()), Fallbacks(store.Data(), tag))
}

// FFILoaded is `locale_loaded_raw`, Locale.loaded: the available locales
// whose data the program has now.
func FFILoaded(fr *rt.Frame) []string {
	available := FFIAvailable(fr)
	if fr == nil {
		return available
	}
	return localefiles.Loaded(cldrdata.StoreFrom(fr.Context()), localefiles.From(fr.Context()), available)
}
