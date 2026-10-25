// Package catalogcheck holds a catalog's entries to the declarations they
// translate: the checks of a `message`'s and a `noun`'s entry, and the
// agreement of a program's nouns with its messages. The compiler runs them
// over the catalogs under `[i18n].catalogs` (internal/analysis), and
// `Catalog.load` runs them while a program runs over a catalog it was
// handed (internal/stdmessageformat), so a catalog loaded at run time is
// held to exactly what a compiled one is, and a rejected entry's reason is
// the text the compiler prints.
//
// It knows nothing of the compiler: a declaration is its name, its
// signature (mf2.Signature) and its source text, and a locale is what
// mf2.Validate reads of it. Each problem is a catalog.Diagnostic at a span
// of the catalog's text.
package catalogcheck

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/language"

	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/mf2"
)

// GenderKey is the key of a noun's entry that gives its gender; every other
// key but `source` names a case.
const GenderKey = "gender"

//go:generate go run langnames_gen.go

// LanguageName is the English name of tag's language: `pl` is Polish
// (languageNames, generated from x/text's display names).
func LanguageName(tag string) string {
	t, err := language.Parse(tag)
	if err != nil {
		return tag
	}
	base, _ := t.Base()
	if name, ok := languageNames[base.String()]; ok {
		return name
	}
	return tag
}

// Declaration is a `message` or a `noun` as its entries are checked: its
// declared name, its source text and the locale that text is in.
type Declaration struct {
	Name         string
	Source       string
	SourceLocale string
}

func diag(start, end int, msg string, hints ...string) catalog.Diagnostic {
	if end <= start {
		end = start + 1
	}
	return catalog.Diagnostic{Span: catalog.Span{Start: start, End: end}, Message: msg, Hints: hints}
}

// sourceChecks are the problems of an entry's `source`: none recorded, or
// one older than the declaration's text. In a partial locale neither is
// an error, since the entry then counts as missing.
func sourceChecks(entry *catalog.Entry, d Declaration, locale string, partial bool) []catalog.Diagnostic {
	if partial {
		return nil
	}
	lang := LanguageName(d.SourceLocale)
	switch {
	case !entry.Source.Present:
		return []catalog.Diagnostic{diag(entry.Span.Start, entry.Span.End,
			fmt.Sprintf("%s: `%s` does not record the %s text it was translated from", locale, d.Name, lang),
			fmt.Sprintf("add `source = %s`, the text the translation was made from", catalog.Literal(d.Source)))}
	case !mf2.Same(entry.Source.Text, d.Source):
		return []catalog.Diagnostic{diag(entry.Source.KeySpan.Start, entry.Source.Span.End,
			fmt.Sprintf("%s: `%s` was translated from an older %s text\n  was:%s\n  now:%s", locale, d.Name, lang, ShownText(entry.Source.Text), ShownText(d.Source)),
			fmt.Sprintf("update the translation, then accept it with the quick fix or `nomi messages accept %s %s`", locale, d.Name))}
	}
	return nil
}

// textChecks are the problems of v, a text of an entry in loc's catalog
// checked against sig: its syntax errors, or what mf2.Validate finds,
// placed in the catalog.
func textChecks(v catalog.Value, sig mf2.Signature, loc mf2.Locale) []catalog.Diagnostic {
	m, errs := mf2.Parse(v.Text)
	ds := errs
	prefix := loc.Tag + ": "
	if len(errs) == 0 {
		ds = mf2.Validate(m, sig, loc)
		prefix = ""
	}
	var out []catalog.Diagnostic
	for _, d := range ds {
		start, end := v.Offset(d.Span.Start), v.Offset(d.Span.End)
		if !v.Exact {
			start, end = v.Span.Start, v.Span.End
		}
		out = append(out, diag(start, end, prefix+d.Message, d.Hints...))
	}
	return out
}

// MessageEntry is what is wrong with entry, a message's entry in the
// catalog of loc, against the message's declaration d and signature sig:
// a key other than `source` and `text`, no `text`, a `source` that is
// missing or older than the declaration's text (not in a partial locale),
// and what mf2.Validate finds in the text. It is in the order the compiler
// reports them.
func MessageEntry(entry *catalog.Entry, d Declaration, sig mf2.Signature, loc mf2.Locale, partial bool) []catalog.Diagnostic {
	var out []catalog.Diagnostic
	for _, f := range entry.Fields {
		out = append(out, diag(f.Value.KeySpan.Start, f.Value.KeySpan.End,
			fmt.Sprintf("%s: `%s` is not a key of a message's entry; an entry holds `source` and `text`", loc.Tag, f.Name)))
	}
	if !entry.Text.Present {
		return append(out, diag(entry.Span.Start, entry.Span.End, fmt.Sprintf("%s: `%s` has no `text`", loc.Tag, d.Name), "write its translation as `text = '''...'''`"))
	}
	out = append(out, sourceChecks(entry, d, loc.Tag, partial)...)
	return append(out, textChecks(entry.Text, sig, loc)...)
}

// NounEntry is what is wrong with entry, a noun's entry in the catalog of
// loc, against the noun's declaration d: a `text`, a `source` that is
// missing or older than the declaration's text (not in a partial locale),
// a `gender` missing where the language's nouns have one, given where they
// have none, or not one of its genders, a key that is no case of the
// language, a form that is not a valid message over `$count` with the
// plural arms the locale needs, and no `nominative`.
func NounEntry(entry *catalog.Entry, d Declaration, loc mf2.Locale, partial bool) []catalog.Diagnostic {
	var out []catalog.Diagnostic
	at := func(start, end int, msg string, hints ...string) {
		out = append(out, diag(start, end, msg, hints...))
	}
	if entry.Text.Present {
		at(entry.Text.KeySpan.Start, entry.Text.KeySpan.End,
			fmt.Sprintf("%s: `%s` is a noun, whose entry gives a form per case and no `text`", loc.Tag, d.Name),
			fmt.Sprintf("write its forms as `nominative = %s` and the other cases messages ask for", catalog.Literal(strings.TrimSpace(entry.Text.Text))))
	}
	out = append(out, sourceChecks(entry, d, loc.Tag, partial)...)
	g := entry.Field(GenderKey)
	switch {
	case len(loc.Genders) > 0 && !g.Present:
		at(entry.Span.Start, entry.Span.End, fmt.Sprintf("%s: `%s` has no `gender`", loc.Tag, d.Name),
			fmt.Sprintf("%s nouns are %s: add `gender = \"...\"` with the one `%s` is", loc.Language, JoinOr(Quoted(loc.Genders)), d.Name))
	case len(loc.Genders) > 0 && !slices.Contains(loc.Genders, g.Text):
		at(g.Span.Start, g.Span.End, fmt.Sprintf("%s: `%s` is not a gender in %s", loc.Tag, g.Text, loc.Language),
			fmt.Sprintf("%s has %s", loc.Language, JoinAnd(Quoted(loc.Genders))))
	case len(loc.Genders) == 0 && g.Present:
		at(g.KeySpan.Start, g.Span.End, fmt.Sprintf("%s: %s nouns have no grammatical gender, so `%s` takes no `gender`", loc.Tag, loc.Language, d.Name),
			"remove the `gender`")
	}
	sig := mf2.NounSignature(d.Name)
	cases := loc.CaseNames()
	nominative := false
	for _, f := range entry.Fields {
		if f.Name == GenderKey {
			continue
		}
		if !slices.Contains(cases, f.Name) {
			hint := fmt.Sprintf("%s has %s", loc.Language, JoinAnd(Quoted(cases)))
			if len(loc.Cases) == 0 {
				hint = fmt.Sprintf("%s nouns have one form, the `nominative`", loc.Language)
			}
			at(f.Value.KeySpan.Start, f.Value.KeySpan.End, fmt.Sprintf("%s: `%s` is not a case in %s, nor a key of a noun's entry", loc.Tag, f.Name, loc.Language),
				hint, "a noun's entry holds its `source`, its `gender` where the language has one, and a form per case")
			continue
		}
		nominative = nominative || f.Name == mf2.Nominative
		out = append(out, textChecks(f.Value, sig, loc)...)
	}
	if !nominative {
		at(entry.Span.Start, entry.Span.End, fmt.Sprintf("%s: `%s` has no `nominative` form", loc.Tag, d.Name),
			"add `nominative = '''...'''`, the form a message writes when it names no case")
	}
	return out
}

// Noun is a noun as a message's text in one locale reads it: its declared
// name, its qualified name (`staff.courier`), its gender there, "" when it
// has none, and whether it has each form.
type Noun struct {
	Name, Qualified string
	Gender          string
	Has             func(kase string) bool
}

// MissingForm is a noun that lacks a form a text asks for: the noun's
// problem, at its entry.
type MissingForm struct {
	Noun          int
	Message, Hint string
}

// MissingArm is a selector on a noun's gender with no arm for a gender
// some noun has: the text's problem, at the selector.
type MissingArm struct {
	Selector      mf2.GenderSelector
	Gender        string
	Message, Hint string
}

// Agreement holds nouns, every noun a program has as read in loc, to text,
// the text in loc of the message sig declares: every noun has every case
// form the text asks for with `:nomi:noun case=...`, and every selector on
// a noun's gender has an arm for each gender some noun has. Forms are in
// the order of the nouns and then the uses; arms in the order of the
// selectors and then of the language's genders.
func Agreement(text *mf2.Message, sig mf2.Signature, loc mf2.Locale, nouns []Noun) ([]MissingForm, []MissingArm) {
	cases, selectors := mf2.NounUses(text, sig)
	if len(cases) == 0 && len(selectors) == 0 {
		return nil, nil
	}
	// The genders the nouns have here, each with the first noun that has
	// it.
	genderOf := map[string]int{}
	var genders []string
	var forms []MissingForm
	for i, n := range nouns {
		if n.Gender != "" && slices.Contains(loc.Genders, n.Gender) {
			if _, ok := genderOf[n.Gender]; !ok {
				genderOf[n.Gender] = i
				genders = append(genders, n.Gender)
			}
		}
		for _, u := range cases {
			if !slices.Contains(loc.CaseNames(), u.Case) || n.Has(u.Case) {
				continue
			}
			forms = append(forms, MissingForm{Noun: i,
				Message: fmt.Sprintf("%s: `%s` has no `%s` form, which `%s` asks for", loc.Tag, n.Name, u.Case, sig.Name),
				Hint:    fmt.Sprintf("add `%s = '''...'''` to `[%s]`: every noun has each form a message in its locale asks for, since a message may be given any noun", u.Case, n.Name)})
		}
	}
	var arms []MissingArm
	for _, s := range selectors {
		has := map[string]bool{}
		for _, k := range s.Keys {
			has[k.Value] = true
		}
		for _, g := range loc.Genders {
			i, used := genderOf[g]
			if !used || has[g] {
				continue
			}
			arms = append(arms, MissingArm{Selector: s, Gender: g,
				Message: fmt.Sprintf("%s: `%s` has no `%s` arm; the noun `%s` is %s in %s", loc.Tag, sig.Name, g, nouns[i].Qualified, g, loc.Language),
				Hint:    fmt.Sprintf("a selector on a noun's gender has an arm for every gender the program's nouns have in %s: %s", loc.Language, JoinAnd(Quoted(genders)))})
		}
	}
	return forms, arms
}

// ShownText is a message text as a diagnostic shows it after the word that
// names it: on that line when it is one line, else indented under it.
func ShownText(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "\n") {
		return " " + s
	}
	return "\n        " + strings.ReplaceAll(s, "\n", "\n        ")
}

// Quoted is each of xs in backticks.
func Quoted(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "`" + x + "`"
	}
	return out
}

// JoinOr joins items as English does with `or`.
func JoinOr(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// JoinAnd joins items as English does with `and`.
func JoinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
