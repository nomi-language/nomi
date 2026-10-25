package analysis

import (
	"path"
	"path/filepath"
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/mf2"
)

// messageModuleAlias is the name a file's message expansions reach
// std/message by: the alias of a hidden whole-module import the expansion
// adds (messageImport). No source can write a name with `$`, so the import
// takes no name the file has, and the file's code cannot use it.
const messageModuleAlias = "message$"

// messageFormatAlias is the alias of the second hidden import, of
// std/message_format, whose `variant_name` names an enum argument's variant.
const messageFormatAlias = "message_format$"

// messageImportLine is the hidden import's line: in the band the prelude
// imports a build prepends are in (isPreludeInjectLine), at a slot the
// prelude's own statements never reach, so the import, like theirs, takes
// no source index.
var messageImportLine = synthSlot(synthOriginPrelude, synthIndexCap-1, synthKindEquatable)

// messageImports are `import std/message as message$` and `import
// std/message_format as message_format$`.
func messageImports() []ast.Node {
	l := messageImportLine
	imp := func(module, alias string, col int) *ast.ImportStmt {
		return &ast.ImportStmt{
			ModulePath:   []ast.Node{&ast.Ident{Name: "std", Line: l, Col: col + 1}, &ast.Ident{Name: module, Line: l, Col: col + 2}},
			FileSegments: 2,
			ModuleAlias:  &ast.Ident{Name: alias, Line: l, Col: col + 3},
			Line:         l,
			Col:          col,
		}
	}
	return []ast.Node{imp("message", messageModuleAlias, 1), imp("message_format", messageFormatAlias, 5)}
}

func hasMessageImport(nodes []ast.Node) bool {
	for _, n := range nodes {
		if imp, ok := n.(*ast.ImportStmt); ok && imp.Line == messageImportLine {
			return true
		}
	}
	return false
}

// messageCatalog is one catalog a file's messages are translated in: one
// locale's file at the declaring file's path under the catalog directory.
type messageCatalog struct {
	locale string
	path   string
	found  bool
	text   string
	file   *catalog.File
	diags  []catalog.Diagnostic
}

// catalogSet reads each of a file's catalogs once.
type catalogSet struct {
	site  *ExpansionSite
	files map[string]*messageCatalog
}

func (s *catalogSet) get(locale string) *messageCatalog {
	if c, ok := s.files[locale]; ok {
		return c
	}
	m := s.site.Manifest
	p := filepath.Join(s.site.Root, filepath.FromSlash(m.CatalogDir()), locale, filepath.FromSlash(s.site.ModulePath)+".toml")
	c := &messageCatalog{locale: locale, path: p}
	if text, ok := s.site.read(p); ok {
		c.found, c.text = true, text
		c.file, c.diags = catalog.Parse(text)
	}
	s.files[locale] = c
	return c
}

// messageExpansion is what a `message` keeps on the function it expands
// into: the declaration, the locales it is checked in and the catalogs it
// read, for the checks (checkMessage) and the span map.
type messageExpansion struct {
	decl *ast.KeywordDecl
	// module is the declaring file's module path, and name the message's
	// qualified name.
	module, name string
	// source is the source locale, locales the listed ones besides it and
	// partial the partial ones.
	source           string
	locales, partial []string
	// catalogs are the catalogs of locales and then partial, in order.
	catalogs []*messageCatalog
	// wrappers are, by parameter, the std/message function the generated
	// body wraps the argument with.
	wrappers map[string]string
	// root is the module root.
	root string
	// noProject reports a declaration in a file with no module root, which
	// has no catalogs.
	noProject bool
	// table is the compiled texts, with the parameters by name only, and
	// tableLit the literal the generated body passes it in, whose value
	// the lowering replaces with the table and the parameters' resolved
	// types (MessageTableText).
	table    mf2.Table
	tableLit *ast.StringLit
}

// MessageTableText is the text lit, a string literal in fd, is lowered to:
// for the literal holding the compiled texts of the message fd expands
// into, the table with each parameter's type as a text reads it
// (messageParamOf, resolved by the analysis fa), which a catalog loaded
// at run time is checked against; false for any other literal. The
// expansion cannot write the types, which are resolved after it.
func MessageTableText(fa *FileAnalysis, fd *ast.FuncDef, lit *ast.StringLit) (string, bool) {
	e := messageExpansionOf(fd)
	if e == nil || fa == nil || lit != e.tableLit && lit.Value != e.tableLit.Value {
		return "", false
	}
	table := e.table
	var ft *FuncType
	if sym := fa.Definitions[Pos{Line: fd.Line, Col: fd.Col}]; sym != nil {
		ft, _ = sym.Type.(*FuncType)
	}
	table.Params = make([]mf2.Param, len(e.decl.Params))
	for i, p := range e.decl.Params {
		var t Type
		if ft != nil && i < len(ft.Params) {
			t = ft.Params[i]
		}
		table.Params[i] = messageParamOf(fa, p.Name, t)
	}
	return mf2.EncodeTable(table), true
}

func (e *messageExpansion) inputs() []ExpansionInput {
	var out []ExpansionInput
	for _, c := range e.catalogs {
		out = append(out, ExpansionInput{Path: c.path, Text: c.text, Found: c.found})
	}
	return out
}

func (e *messageExpansion) info() *ExpansionInfo {
	in := &ExpansionInfo{Keyword: keywordMessage, Decl: e.decl}
	for _, c := range e.catalogs {
		entry := c.file.Entry(e.decl.Name)
		if entry == nil {
			continue
		}
		sl, sc := catalog.LineCol(c.text, entry.Span.Start)
		el, ec := catalog.LineCol(c.text, entry.Span.End)
		if entry.Text.Present {
			el, ec = catalog.LineCol(c.text, entry.Text.Span.End)
		}
		in.Inputs = append(in.Inputs, ExpansionSpan{Path: c.path, Label: c.locale, Line: sl, Col: sc, EndLine: el, EndCol: ec})
	}
	return in
}

// expandMessage writes the function a `message` declares:
//
//	pub fn cart_items(count: Int) -> message$.Message {
//	    message$.compiled("cart.cart_items", "<table>", [message$.integer(count)])
//	}
//
// The parameters are the declaration's own nodes, at their positions; the
// name is at the declaration's name; the return type and the body are in the
// synthesized band at slot. The table holds the source text and each
// locale's current translation (mf2.Table), so a program carries the texts
// of the messages it reaches. Each argument is wrapped by its parameter's
// type as written: `Int`, `Float`, `Decimal` and `String` by their own
// functions, an enum a text formats with `:string` by its variant's name,
// and anything else by its `Localize`. checkMessage holds the wrapping to
// the parameter's resolved type.
func expandMessage(kd *ast.KeywordDecl, site *ExpansionSite, cats *catalogSet, slot int) *ast.FuncDef {
	m := site.Manifest
	e := &messageExpansion{decl: kd, module: site.ModulePath, root: site.Root, source: m.Source(), wrappers: map[string]string{}}
	e.name = path.Base(site.ModulePath) + "." + kd.Name
	if site.Root == "" {
		e.noProject = true
	} else if m != nil {
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
	table := mf2.Table{Name: e.name, Source: e.source, Texts: map[string]string{}}
	for _, p := range kd.Params {
		table.Params = append(table.Params, mf2.Param{Name: p.Name})
	}
	uses := map[string]*mf2.Use{}
	addUses := func(m *mf2.Message) {
		for name, u := range mf2.Uses(m) {
			all := uses[name]
			if all == nil {
				all = &mf2.Use{Functions: map[string]bool{}}
				uses[name] = all
			}
			for f := range u.Functions {
				all.Functions[f] = true
			}
			all.Bare = all.Bare || u.Bare
		}
	}
	if sm, errs := mf2.Parse(source); len(errs) == 0 {
		table.Texts[e.source] = mf2.Print(sm)
		addUses(sm)
	}
	for _, c := range e.catalogs {
		entry := c.file.Entry(kd.Name)
		if entry == nil || !entry.Text.Present {
			continue
		}
		tm, errs := mf2.Parse(entry.Text.Text)
		if len(errs) > 0 {
			continue
		}
		addUses(tm)
		if entry.Source.Present && mf2.Same(entry.Source.Text, source) {
			table.Texts[c.locale] = mf2.Print(tm)
		}
	}

	var args []ast.Node
	for _, p := range kd.Params {
		w := messageWrapper(p, uses[p.Name])
		e.wrappers[p.Name] = w
		operands := []ast.Node{identExpr(p.Name, 0, 0)}
		switch w {
		case "variant", "localized_variant":
			// The variant's name, read where the argument's type is known.
			name := &ast.Call{Func: fieldAccess(identExpr(messageFormatAlias, 0, 0), "variant_name", 0, 0), Args: []ast.Node{identExpr(p.Name, 0, 0)}}
			if w == "variant" {
				operands = []ast.Node{name}
			} else {
				operands = []ast.Node{name, identExpr(p.Name, 0, 0)}
			}
		}
		args = append(args, &ast.Call{Func: fieldAccess(identExpr(messageModuleAlias, 0, 0), w, 0, 0), Args: operands})
	}
	e.table = table
	e.tableLit = &ast.StringLit{Value: mf2.EncodeTable(table)}
	body := &ast.Call{
		Func: fieldAccess(identExpr(messageModuleAlias, 0, 0), "compiled", 0, 0),
		Args: []ast.Node{
			&ast.StringLit{Value: e.name},
			e.tableLit,
			&ast.ListLit{Items: args},
		},
	}
	// Stamp the added nodes in the synthesized band, then give the function
	// its own name's position and the declaration's parameters.
	fn := &ast.FuncDef{
		Name:           kd.Name,
		Public:         kd.Public,
		ReturnTypeExpr: &ast.QualifiedType{Module: messageModuleAlias, Member: &ast.SimpleType{Name: "Message"}},
		Body:           &ast.Block{Stmts: []ast.Node{exprStmt(body, 0, 0)}},
		Doc:            kd.Doc,
	}
	uniquifyPositions(fn, slot)
	fn.Line, fn.Col, fn.FnCol = kd.Line, kd.NameCol, kd.Col
	fn.Params = kd.Params
	fn.ParamsEndTrivia = kd.ParamsEndTrivia
	fn.Span = kd.Span
	fn.TriviaCarrier = kd.TriviaCarrier
	fn.Expansion = ast.NewExpansion(keywordMessage, e)
	return fn
}

// messageWrapper is the std/message function that wraps an argument of
// p's type as written, used by the texts as u says.
func messageWrapper(p ast.Param, u *mf2.Use) string {
	switch TypeExprBaseName(p.TypeAnnotation) {
	case "Int":
		return "integer"
	case "Float":
		return "float"
	case "Decimal":
		return "decimal"
	case "String":
		return "text"
	case "Message":
		return "nested"
	case "Noun":
		return "noun"
	case "List":
		if u != nil && u.Functions["nomi:list"] {
			return "list"
		}
	case "Date":
		return "date"
	case "Time":
		return "time"
	case "NaiveDateTime", "DateTime":
		return "moment"
	case "Money":
		return "money"
	}
	if u != nil && u.Functions["string"] {
		if u.Bare {
			return "localized_variant"
		}
		return "variant"
	}
	return "localized"
}

// sortedLocales is ls sorted, for a stable order of diagnostics.
func sortedLocales(ls []string) []string {
	out := append([]string(nil), ls...)
	sort.Strings(out)
	return out
}
