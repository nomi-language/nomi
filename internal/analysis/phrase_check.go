package analysis

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/catalogcheck"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdplural"
)

// The checks of a `phrase` declaration and its catalogs (spec §18 *Phrases*). Every
// one is an error, at the declaration, in its text, or in a catalog's
// entry:
//
//  1. Completeness: every listed locale has the phrase's text, in its own
//     catalog or in a listed ancestor's on its CLDR fallback chain, or
//     the source locale is that ancestor; a partial locale may lack it.
//  2. No stray entries: a catalog key that is no phrase of its file.
//     (Stray catalog files and directories are the program's:
//     CheckCatalogTree.)
//  3. Parameters, 4. types, 5. plural arms, 6. enum arms and 9. MF2
//     validity: mf2.Validate, over the source text and each translation,
//     with the parameters' resolved types.
//  8. Freshness: an entry's recorded source is the current text, compared
//     as MF2 data models. In a listed locale a stale entry is an error; in a
//     partial one it is left out, and the phrase shows in the source
//     locale.

// phraseExpansionOf is fn's phrase expansion, or nil.
func phraseExpansionOf(fn *ast.FuncDef) *phraseExpansion {
	if fn == nil || fn.Expansion == nil {
		return nil
	}
	e, _ := fn.Expansion.Detail().(*phraseExpansion)
	return e
}

// checkPhrase checks the phrase fn expands, and reports whether it found
// an error, in which case its generated body is not checked: what is wrong
// is the declaration's or a catalog's, and a body made from them says
// nothing more.
func (c *checker) checkPhrase(fn *ast.FuncDef, e *phraseExpansion) bool {
	before := len(c.errors)
	kd := e.decl
	sig := mf2.Signature{Name: kd.Name}
	var ft *FuncType
	unresolved := false
	if sym := c.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]; sym != nil {
		ft, _ = sym.Type.(*FuncType)
	}
	for i, p := range kd.Params {
		var t Type
		if ft != nil && i < len(ft.Params) {
			t = ft.Params[i]
		}
		if ft != nil && t == nil {
			// A parameter type that names no type has its error already;
			// the text and the wrapping would only repeat it.
			unresolved = true
			continue
		}
		param := phraseParamOf(c.fa, p.Name, t)
		sig.Params = append(sig.Params, param)
		c.checkPhraseWrapper(p, param, e.wrappers[p.Name])
	}
	if unresolved {
		return true
	}
	if kd.ReturnTypeExpr != nil {
		c.errors = append(c.errors, errAt(kd.ReturnTypeExpr, fmt.Sprintf("a `phrase` declaration returns a `Phrase`, so it is written with no `-> %s`", kd.ReturnTypeExpr.TypeString())))
	}
	if kd.Body == nil {
		c.errors = append(c.errors, TypeError{Line: kd.NameLine, Col: kd.NameCol, EndLine: kd.NameLine, EndCol: kd.NameCol + len(kd.Name),
			Message: fmt.Sprintf("`phrase %s` has no text", kd.Name)}.WithHint(fmt.Sprintf("write its %s text, MessageFormat 2, in backticks after its parameters", languageName(e.source))))
		return true
	}
	source := kd.Body.Value
	srcLoc := pluralLocale(e.source, true)
	if sm, errs := mf2.Parse(source); len(errs) > 0 {
		c.addBodyDiagnostics(kd, e.source+": ", errs)
	} else {
		c.addBodyDiagnostics(kd, "", mf2.Validate(sm, sig, srcLoc))
	}
	for _, cat := range e.catalogs {
		c.checkPhraseEntry(e, cat, sig, source)
	}
	return len(c.errors) > before
}

// phraseParamOf is what mf2.Validate reads of a parameter of type t, in
// the file fa analyzes.
func phraseParamOf(fa *FileAnalysis, name string, t Type) mf2.Param {
	t = resolveTypeVar(t)
	p := mf2.Param{Name: name, Kind: mf2.KindOther}
	if t != nil {
		p.Type = t.String()
	}
	switch {
	case t == nil:
		p.Localize = true
	case TypesEqual(t, TypeInt):
		p.Kind, p.Localize = mf2.KindInt, true
	case TypesEqual(t, TypeFloat):
		p.Kind, p.Localize = mf2.KindFloat, true
	case TypesEqual(t, TypeDecimal):
		p.Kind, p.Localize = mf2.KindDecimal, true
	case TypesEqual(t, TypeString):
		p.Kind, p.Localize = mf2.KindString, true
	default:
		if st, ok := t.(*StructType); ok && st.Name == "Phrase" && st.Origin == "std/phrase" {
			p.Kind, p.Localize = mf2.KindPhrase, true
			return p
		}
		if st, ok := t.(*StructType); ok && st.Name == "Noun" && st.Origin == "std/phrase" {
			p.Kind, p.Localize = mf2.KindNoun, true
			return p
		}
		if st, ok := t.(*StructType); ok && st.Origin == "std/calendar" {
			switch st.Name {
			case "Date":
				p.Kind = mf2.KindDate
			case "Time":
				p.Kind = mf2.KindTime
			case "NaiveDateTime", "DateTime":
				p.Kind = mf2.KindDateTime
			}
		}
		if st, ok := t.(*StructType); ok && st.Name == "Money" && st.Origin == "std/money" {
			p.Kind = mf2.KindMoney
		}
		if lt, ok := t.(*ListType); ok && TypesEqual(lt.Elem, TypeString) {
			p.Kind = mf2.KindList
		}
		if it, ok := t.(*InterfaceType); ok {
			p.Localize = it.Name == "Localize"
			return p
		}
		if et, ok := t.(*EnumType); ok && len(et.TypeArgs) == 0 {
			bare := true
			for _, v := range et.Variants {
				bare = bare && v.Kind == VariantBare
				p.Variants = append(p.Variants, v.Name)
			}
			if bare {
				p.Kind = mf2.KindEnum
			} else {
				p.Variants = nil
			}
		}
		if name := concreteTypeName(t); name != "" {
			p.Localize = fa != nil && fa.HasImpl(name, "Localize")
		}
	}
	return p
}

// checkPhraseWrapper holds the wrapping the expansion chose for a
// parameter by its type as written to the parameter's resolved type.
func (c *checker) checkPhraseWrapper(p ast.Param, param mf2.Param, wrapper string) {
	want := map[mf2.Kind]string{mf2.KindInt: "integer", mf2.KindFloat: "float", mf2.KindDecimal: "decimal", mf2.KindString: "text"}
	written := map[string]string{"integer": "Int", "float": "Float", "decimal": "Decimal", "text": "String"}
	at := ast.Node(p.TypeAnnotation)
	if at == nil {
		return
	}
	if w, scalar := want[param.Kind]; scalar && w != wrapper {
		c.errors = append(c.errors, errAt(at, fmt.Sprintf("write `$%s`'s type as `%s`: a phrase reads the types of its parameters as they are written", p.Name, param.Type)))
		return
	}
	if name, scalar := written[wrapper]; scalar && want[param.Kind] != wrapper {
		c.errors = append(c.errors, errAt(at, fmt.Sprintf("this `%s` is not std's `%s`, which a phrase parameter written `%s` must be", name, name, name)))
	}
	if wrapper == "nested" && param.Kind != mf2.KindPhrase {
		c.errors = append(c.errors, errAt(at, "this `Phrase` is not std/phrase's `Phrase`, which a phrase parameter written `Phrase` must be"))
	}
	if wrapper == "noun" && param.Kind != mf2.KindNoun {
		c.errors = append(c.errors, errAt(at, "this `Noun` is not std/phrase's `Noun`, which a phrase parameter written `Noun` must be"))
	}
	for w, want := range map[string]struct {
		kind mf2.Kind
		std  string
	}{"money": {mf2.KindMoney, "std/money's `Money`"}, "date": {mf2.KindDate, "std/calendar's `Date`"},
		"time": {mf2.KindTime, "std/calendar's `Time`"}, "moment": {mf2.KindDateTime, "std/calendar's"}} {
		if wrapper == w && param.Kind != want.kind {
			name := TypeExprBaseName(p.TypeAnnotation)
			std := want.std
			if w == "moment" {
				std += " `" + name + "`"
			}
			c.errors = append(c.errors, errAt(at, fmt.Sprintf("this `%s` is not %s, which a phrase parameter written `%s` must be", name, std, name)))
		}
	}
}

// addBodyDiagnostics reports diagnostics about the declaration's own text,
// placed in its body.
func (c *checker) addBodyDiagnostics(kd *ast.KeywordDecl, prefix string, ds []mf2.Diagnostic) {
	for _, d := range ds {
		l, col := kd.BodyPosition(d.Span.Start)
		el, ec := kd.BodyPosition(d.Span.End)
		if el < l || el == l && ec <= col {
			el, ec = l, col+1
		}
		e := TypeError{Line: l, Col: col, EndLine: el, EndCol: ec, Message: prefix + d.Message}
		for _, h := range d.Hints {
			e = e.WithHint(h)
		}
		c.errors = append(c.errors, e)
	}
}

// catalogError is an error at span of cat.
func catalogError(cat *phraseCatalog, start, end int, msg string, hints ...string) TypeError {
	l, col := catalog.LineCol(cat.text, start)
	el, ec := catalog.LineCol(cat.text, max(end, start+1))
	e := TypeError{File: cat.path, Line: l, Col: col, EndLine: el, EndCol: ec, Message: msg}
	for _, h := range hints {
		e = e.WithHint(h)
	}
	return e
}

func (e *phraseExpansion) partialLocale(locale string) bool {
	return slices.Contains(e.partial, locale)
}

// checkPhraseEntry checks e's entry in cat.
func (c *checker) checkPhraseEntry(e *phraseExpansion, cat *phraseCatalog, sig mf2.Signature, source string) {
	kd := e.decl
	partial := e.partialLocale(cat.locale)
	entry := cat.file.Entry(kd.Name)
	if entry == nil {
		if partial || e.fallsBack(cat.locale, source) {
			return
		}
		e2 := TypeError{Line: kd.NameLine, Col: kd.NameCol, EndLine: kd.NameLine, EndCol: kd.NameCol + len(kd.Name),
			Message: fmt.Sprintf("%s: `%s` has no translation", cat.locale, kd.Name)}
		hint := fmt.Sprintf("add `[%s]` with its `source` and `text` to %s", kd.Name, e.relativePath(cat.path))
		if !cat.found {
			hint = fmt.Sprintf("write %s, with a `[%s]` entry holding its `source` and `text`", e.relativePath(cat.path), kd.Name)
		}
		c.errors = append(c.errors, e2.WithHint(hint).WithHint(fmt.Sprintf("or list %s in `[i18n].partial_locales`, where a phrase may be left untranslated", cat.locale)))
		return
	}
	decl := catalogcheck.Declaration{Name: kd.Name, Source: source, SourceLocale: e.source}
	c.addCatalogDiagnostics(cat, catalogcheck.PhraseEntry(entry, decl, sig, pluralLocale(cat.locale, false), partial))
}

// addCatalogDiagnostics reports ds, problems in cat's text.
func (c *checker) addCatalogDiagnostics(cat *phraseCatalog, ds []catalog.Diagnostic) {
	for _, d := range ds {
		c.errors = append(c.errors, catalogError(cat, d.Span.Start, d.Span.End, d.Message, d.Hints...))
	}
}

// fallsBack reports whether a listed locale with no entry of its own gets
// the phrase from an ancestor on its CLDR fallback chain: the source
// locale, or a listed locale whose catalog has a current entry.
func (e *phraseExpansion) fallsBack(locale, source string) bool {
	chain := stdlocale.Fallbacks(cldrfull.Full(), locale)
	for _, anc := range chain[min(1, len(chain)):] {
		if anc == e.source {
			return true
		}
		if !slices.Contains(e.locales, anc) {
			continue
		}
		for _, cat := range e.catalogs {
			if cat.locale != anc {
				continue
			}
			if entry := cat.file.Entry(e.decl.Name); entry != nil && entry.Text.Present && entry.Source.Present && mf2.Same(entry.Source.Text, source) {
				return true
			}
		}
	}
	return false
}

// relativePath is p from the module root, for a hint.
func (e *phraseExpansion) relativePath(p string) string { return relativeToRoot(e.root, p) }

// relativeToRoot is p from the module root, for a hint.
func relativeToRoot(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return p
}

// pluralLocale is the locale a text is checked in: its tag, its language's
// English name, its CLDR plural rules and its grammatical genders and
// cases.
func pluralLocale(tag string, source bool) mf2.Locale {
	d := cldrfull.Full()
	genders, cases := d.Grammar(stdlocale.Fallbacks(d, tag))
	return mf2.Locale{
		Tag:      tag,
		Language: languageName(tag),
		Cardinal: stdplural.Rules(d, tag, false),
		Ordinal:  stdplural.Rules(d, tag, true),
		Source:   source,
		Genders:  genders,
		Cases:    cases,
	}
}

// languageName is the English name of tag's language: `pl` is Polish.
func languageName(tag string) string { return catalogcheck.LanguageName(tag) }

// checkFileCatalogs reports what is wrong in the catalogs the file's
// phrases and nouns read that no one declaration's check reports: a
// catalog that is not TOML a catalog may be written in, and an entry that
// names no phrase or noun of the file.
func (c *checker) checkFileCatalogs(nodes []ast.Node) {
	var catalogs [][]*phraseCatalog
	module := ""
	names := map[string]bool{}
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			if e := phraseExpansionOf(d); e != nil {
				catalogs = append(catalogs, e.catalogs)
				names[e.decl.Name] = true
				module = e.module
			}
		case *ast.OnceBinding:
			if e := nounExpansionOf(d); e != nil {
				catalogs = append(catalogs, e.catalogs)
				names[e.decl.Name] = true
				module = e.module
			}
		}
	}
	if len(catalogs) == 0 {
		return
	}
	seen := map[*phraseCatalog]bool{}
	for _, cats := range catalogs {
		for _, cat := range cats {
			if seen[cat] || !cat.found {
				continue
			}
			seen[cat] = true
			for _, d := range cat.diags {
				c.errors = append(c.errors, catalogError(cat, d.Span.Start, d.Span.End, cat.locale+": "+d.Message, d.Hints...))
			}
			for _, entry := range cat.file.Entries {
				if !names[entry.Name] {
					c.errors = append(c.errors, catalogError(cat, entry.Span.Start, entry.Span.End,
						fmt.Sprintf("%s: `%s` is not a phrase or noun of %s", cat.locale, entry.Name, path.Base(module)+".nomi"),
						"a catalog holds the translations of its Nomi file's `phrase` and `noun` declarations; remove the entry, or declare it"))
				}
			}
		}
	}
}
