package analysis

import (
	"path"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/mf2"
)

// A `noun` declaration names a thing a message talks about, as a value of
// std/message's `Noun`:
//
//	pub noun book `.input {$count :integer} .match $count one {{book}} * {{books}}`
//
// Its body is its source-locale form, a message over one implicit `$count`.
// Each catalog's entry for it holds the `source` it was translated from, its
// `gender` in a language whose nouns have one, and a form per grammatical
// case:
//
//	[book]
//	source = '''.input {$count :integer} .match $count one {{book}} * {{books}}'''
//	gender = "feminine"
//	nominative = '''...'''
//	genitive = '''...'''
//
// It expands into the `once` it names (expandNoun); the checks of the
// declaration and its entries are checkNoun's, and those that hold every
// noun of a program to every message are CheckAgreement's.

// nounExpansion is what a `noun` keeps on the `once` it expands into: the
// declaration, the locales it is checked in and the catalogs it read.
type nounExpansion struct {
	decl *ast.KeywordDecl
	// module is the declaring file's module path, and name the noun's
	// qualified name.
	module, name string
	// source is the source locale, locales the listed ones besides it and
	// partial the partial ones.
	source           string
	locales, partial []string
	// catalogs are the catalogs of locales and then partial, in order.
	catalogs []*messageCatalog
	root     string
}

func (e *nounExpansion) inputs() []ExpansionInput {
	var out []ExpansionInput
	for _, c := range e.catalogs {
		out = append(out, ExpansionInput{Path: c.path, Text: c.text, Found: c.found})
	}
	return out
}

func (e *nounExpansion) info() *ExpansionInfo {
	in := &ExpansionInfo{Keyword: keywordNoun, Decl: e.decl}
	for _, c := range e.catalogs {
		entry := c.file.Entry(e.decl.Name)
		if entry == nil {
			continue
		}
		sl, sc := catalog.LineCol(c.text, entry.Span.Start)
		end := entry.Span.End
		for _, f := range entry.Fields {
			end = max(end, f.Value.Span.End)
		}
		if entry.Source.Present {
			end = max(end, entry.Source.Span.End)
		}
		el, ec := catalog.LineCol(c.text, end)
		in.Inputs = append(in.Inputs, ExpansionSpan{Path: c.path, Label: c.locale, Line: sl, Col: sc, EndLine: el, EndCol: ec})
	}
	return in
}

func (e *nounExpansion) partialLocale(locale string) bool {
	for _, l := range e.partial {
		if l == locale {
			return true
		}
	}
	return false
}

// nounGenderKey is the key of a noun's entry that gives its gender; every
// other key but `source` names a case.
const nounGenderKey = "gender"

// nounEntryCurrent reports whether entry, a noun's, was translated from
// source, the declaration's text.
func nounEntryCurrent(entry *catalog.Entry, source string) bool {
	return entry != nil && entry.Source.Present && mf2.Same(entry.Source.Text, source)
}

// expandNoun writes the value a `noun` names:
//
//	pub once book: message$.Noun = message$.compiled_noun("cart.book", "<table>")
//
// The `once` has the declaration's name at its name's position; its type
// and value are in the synthesized band at slot. The table (mf2.NounTable)
// holds the source text as the nominative and each current translation's
// gender and forms, canonical.
func expandNoun(kd *ast.KeywordDecl, site *ExpansionSite, cats *catalogSet, slot int) *ast.OnceBinding {
	m := site.Manifest
	e := &nounExpansion{decl: kd, module: site.ModulePath, root: site.Root, source: m.Source()}
	e.name = path.Base(site.ModulePath) + "." + kd.Name
	if site.Root != "" && m != nil {
		for _, l := range m.Locales {
			if l != e.source {
				e.locales = append(e.locales, l)
			}
		}
		e.partial = m.PartialLocales
		for _, l := range append(append([]string(nil), e.locales...), e.partial...) {
			e.catalogs = append(e.catalogs, cats.get(l))
		}
	}
	source := ""
	if kd.Body != nil {
		source = kd.Body.Value
	}
	table := mf2.NounTable{Name: e.name, Source: e.source, Genders: map[string]string{}, Forms: map[string]map[string]string{}}
	if sm, errs := mf2.Parse(source); len(errs) == 0 {
		table.Forms[e.source] = map[string]string{mf2.Nominative: mf2.Print(sm)}
	}
	for _, c := range e.catalogs {
		entry := c.file.Entry(kd.Name)
		if !nounEntryCurrent(entry, source) {
			continue
		}
		forms := map[string]string{}
		for _, f := range entry.Fields {
			if f.Name == nounGenderKey {
				table.Genders[c.locale] = f.Value.Text
				continue
			}
			if fm, errs := mf2.Parse(f.Value.Text); len(errs) == 0 {
				forms[f.Name] = mf2.Print(fm)
			}
		}
		table.Forms[c.locale] = forms
	}
	value := &ast.Call{
		Func: fieldAccess(identExpr(messageModuleAlias, 0, 0), "compiled_noun", 0, 0),
		Args: []ast.Node{&ast.StringLit{Value: e.name}, &ast.StringLit{Value: mf2.EncodeNoun(table)}},
	}
	typ := &ast.QualifiedType{Module: messageModuleAlias, Member: &ast.SimpleType{Name: "Noun"}}
	// Stamp the added nodes in the synthesized band, as a function's.
	carrier := &ast.FuncDef{ReturnTypeExpr: typ, Body: &ast.Block{Stmts: []ast.Node{exprStmt(value, 0, 0)}}}
	uniquifyPositions(carrier, slot)
	once := &ast.OnceBinding{
		Name:           kd.Name,
		Public:         kd.Public,
		TypeAnnotation: typ,
		Value:          value,
		Doc:            kd.Doc,
		Line:           kd.Line,
		Col:            kd.NameCol,
	}
	once.Span = kd.Span
	once.TriviaCarrier = kd.TriviaCarrier
	once.Expansion = ast.NewExpansion(keywordNoun, e)
	return once
}

// nounExpansionOf is o's noun expansion, or nil.
func nounExpansionOf(o *ast.OnceBinding) *nounExpansion {
	if o == nil || o.Expansion == nil {
		return nil
	}
	e, _ := o.Expansion.Detail().(*nounExpansion)
	return e
}
