package analysis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalogcheck"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdlocale"
)

// The checks of a `noun` declaration and its catalog entries, all errors:
//
//   - The declaration takes no parameters and no `-> type`, and its text is
//     a valid message over `$count` with the plural arms the source locale
//     needs. Its source locale's language has no grammatical gender, since
//     a declaration has no place to give one.
//   - Completeness and freshness as a message's: every listed locale has
//     an entry, or a listed ancestor's, and each records the current
//     source text.
//   - Gender: an entry has a `gender` exactly when its language's nouns
//     have one, and it is one CLDR lists for the language.
//   - Forms: every other key is a case CLDR lists for the language (or
//     `nominative`, in a language CLDR gives no cases), the `nominative`
//     is there, and each form is a valid message over `$count` with the
//     plural arms the locale needs.
//
// The forms a message asks for and the gender arms a message needs are
// checked over the whole program (CheckAgreement).

// checkNoun checks the noun o expands, and reports whether it found an
// error, in which case its generated value is not checked.
func (c *checker) checkNoun(o *ast.OnceBinding, e *nounExpansion) bool {
	before := len(c.errors)
	kd := e.decl
	nameErr := func(msg string) TypeError {
		return TypeError{Line: kd.NameLine, Col: kd.NameCol, EndLine: kd.NameLine, EndCol: kd.NameCol + len(kd.Name), Message: msg}
	}
	if kd.HasParams {
		c.errors = append(c.errors, nameErr(fmt.Sprintf("`noun %s` takes no parameters", kd.Name)).
			WithHint("its forms read one value, `$count`, how many things the text names, which a message gives with `:nomi:noun count=...`"))
	}
	if kd.ReturnTypeExpr != nil {
		c.errors = append(c.errors, errAt(kd.ReturnTypeExpr, fmt.Sprintf("a `noun` declaration is a `Noun`, so it is written with no `-> %s`", kd.ReturnTypeExpr.TypeString())))
	}
	if kd.Body == nil {
		c.errors = append(c.errors, nameErr(fmt.Sprintf("`noun %s` has no text", kd.Name)).
			WithHint(fmt.Sprintf("write its %s form in backticks, MessageFormat 2 over `$count`: `.input {$count :integer} .match $count one {{book}} * {{books}}`", languageName(e.source))))
		return true
	}
	src := pluralLocale(e.source, false)
	if len(src.Genders) > 0 {
		c.errors = append(c.errors, TypeError{Line: kd.Line, Col: kd.Col, EndLine: kd.Line, EndCol: kd.KeywordEndCol(),
			Message: fmt.Sprintf("`noun %s` is declared in %s, whose nouns have a grammatical gender, which a `noun` declaration has no place to give", kd.Name, languageName(e.source))}.
			WithHint("declare nouns in a project whose `[i18n].source_locale` is a language without grammatical gender, such as English"))
	}
	source := kd.Body.Value
	sig := mf2.NounSignature(kd.Name)
	if sm, errs := mf2.Parse(source); len(errs) > 0 {
		c.addBodyDiagnostics(kd, e.source+": ", errs)
	} else {
		c.addBodyDiagnostics(kd, "", mf2.Validate(sm, sig, src))
	}
	for _, cat := range e.catalogs {
		c.checkNounEntry(e, cat, sig, source)
	}
	return len(c.errors) > before
}

// checkNounEntry checks e's entry in cat.
func (c *checker) checkNounEntry(e *nounExpansion, cat *messageCatalog, sig mf2.Signature, source string) {
	kd := e.decl
	partial := e.partialLocale(cat.locale)
	entry := cat.file.Entry(kd.Name)
	loc := pluralLocale(cat.locale, false)
	if entry == nil {
		if partial || e.fallsBack(cat.locale, source) {
			return
		}
		what := "its `source` and its `nominative` form"
		if len(loc.Genders) > 0 {
			what = "its `source`, its `gender` and its `nominative` form"
		}
		err := TypeError{Line: kd.NameLine, Col: kd.NameCol, EndLine: kd.NameLine, EndCol: kd.NameCol + len(kd.Name),
			Message: fmt.Sprintf("%s: `%s` has no translation", cat.locale, kd.Name)}
		hint := fmt.Sprintf("add `[%s]` with %s to %s", kd.Name, what, relativeToRoot(e.root, cat.path))
		if !cat.found {
			hint = fmt.Sprintf("write %s, with a `[%s]` entry holding %s", relativeToRoot(e.root, cat.path), kd.Name, what)
		}
		c.errors = append(c.errors, err.WithHint(hint).WithHint(fmt.Sprintf("or list %s in `[i18n].partial_locales`, where a noun may be left untranslated", cat.locale)))
		return
	}
	decl := catalogcheck.Declaration{Name: kd.Name, Source: source, SourceLocale: e.source}
	c.addCatalogDiagnostics(cat, catalogcheck.NounEntry(entry, decl, loc, partial))
}

// fallsBack reports whether a listed locale with no entry of its own gets
// the noun from an ancestor on its CLDR fallback chain: the source locale,
// or a listed locale whose catalog has a current entry.
func (e *nounExpansion) fallsBack(locale, source string) bool {
	chain := stdlocale.Fallbacks(cldrfull.Full(), locale)
	for _, anc := range chain[min(1, len(chain)):] {
		if anc == e.source {
			return true
		}
		if !slices.Contains(e.locales, anc) {
			continue
		}
		for _, cat := range e.catalogs {
			if cat.locale == anc && nounEntryCurrent(cat.file.Entry(e.decl.Name), source) {
				return true
			}
		}
	}
	return false
}

func quoted(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "`" + x + "`"
	}
	return out
}

// joinOr joins items as English does with `or`.
func joinOr(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}
