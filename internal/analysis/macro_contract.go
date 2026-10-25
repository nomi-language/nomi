package analysis

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// THE MACRO CONTRACT. A macro's declaration states what it declares, so a
// reader of the declaration sees the real signature, and the compiler holds
// the expansion to it (macroContractErrors):
//
//   - A declaration whose name is a type name (`one_of! Size`) declares a
//     type of that name.
//   - One that writes a parameter list or a result type (`table! rate(code:
//     String) -> Maybe<Int>`) declares a function of that name. Its
//     parameters end with the declaration's, the same names and types in
//     the same order; the macro may put parameters of its own before them
//     (sqlkit's `conn: sql.Conn`). Its result is the declared type, or a
//     `Result` whose `Ok` type is the declared type (sqlkit's
//     `Result<T, sql.Error>`).
//   - One that writes neither (`database! main`) names what the macro makes,
//     and need not declare anything of its name; what it does declare of
//     that name is held to the visibility rule below.
//
// What the declaration names is `pub` exactly when the declaration is, and
// carries the declaration's doc comment (attachDeclarationDoc), whatever the
// macro wrote. Types compare as the checker resolves them, so the
// expansion's own import of a type (`sqlkit.Rows<Orders>`) is the
// declaration's (`Rows<Orders>` after `import sqlkit.Rows`).
//
// A mismatch is a defect in the macro: an error at the declaration, and the
// program reports its macro declarations' errors alone (MacroFailed), as it
// does for a declaration the macro declined.

// declaresType reports whether kd's name is a type name: the declaration
// declares that type.
func declaresType(kd *ast.KeywordDecl) bool {
	for _, r := range kd.Name {
		return unicode.IsUpper(r)
	}
	return false
}

// declaresFunction reports whether kd declares the function it names: it
// writes a parameter list or a result type.
func declaresFunction(kd *ast.KeywordDecl) bool {
	return !declaresType(kd) && (kd.HasParams || kd.ReturnTypeExpr != nil)
}

// namedItem is the top-level declaration among generated that declares
// name, or nil.
func namedItem(generated []ast.Node, name string) ast.Node {
	for _, n := range generated {
		for _, d := range topLevelNames(n) {
			if !d.imported && d.name == name {
				return n
			}
		}
	}
	return nil
}

// attachDeclarationDoc gives the declaration kd names among generated, an
// expansion's nodes, kd's doc comment in place of any the macro wrote.
func attachDeclarationDoc(kd *ast.KeywordDecl, generated []ast.Node) {
	switch n := namedItem(generated, kd.Name).(type) {
	case *ast.FuncDef:
		n.Doc = kd.Doc
	case *ast.StructDef:
		n.Doc = kd.Doc
	case *ast.EnumDef:
		n.Doc = kd.Doc
	case *ast.TypeDef:
		n.Doc = kd.Doc
	case *ast.TypeAlias:
		n.Doc = kd.Doc
	case *ast.InterfaceDef:
		n.Doc = kd.Doc
	case *ast.OnceBinding:
		n.Doc = kd.Doc
	}
}

// sourceWithDoc is source, an expansion of kd, with kd's doc comment in
// place of any doc comment above the declaration of kd's name, as the
// compiler checks it.
func sourceWithDoc(source string, kd *ast.KeywordDecl) string {
	nodes, err := parser.Parse(lexer.Lex(source))
	if err != nil {
		return source
	}
	item := namedItem(nodes, kd.Name)
	if item == nil || item.LineNum() <= 0 {
		return source
	}
	lines := strings.SplitAfter(source, "\n")
	at := item.LineNum() - 1
	if at >= len(lines) {
		return source
	}
	start := at
	for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "///") {
		start--
	}
	var doc strings.Builder
	if kd.Doc != "" {
		for _, line := range strings.Split(kd.Doc, "\n") {
			if line == "" {
				doc.WriteString("///\n")
			} else {
				doc.WriteString("/// " + line + "\n")
			}
		}
	}
	return strings.Join(lines[:start], "") + doc.String() + strings.Join(lines[at:], "")
}

// itemHeader is how a message shows n, the declaration an expansion made
// for a declaration's name: `pub fn say(name: String) -> Int`, `enum Size`.
// ft is n's resolved type when it is a function; a parameter whose type
// did not resolve (a type a failed declaration would have declared) is
// nil in it and shows as written.
func itemHeader(n ast.Node, ft *FuncType) string {
	pub := func(p bool) string {
		if p {
			return "pub "
		}
		return ""
	}
	switch v := n.(type) {
	case *ast.FuncDef:
		var b strings.Builder
		fmt.Fprintf(&b, "%sfn %s(", pub(v.Public), v.Name)
		for i, p := range v.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.Name)
			switch {
			case ft != nil && i < len(ft.Params) && ft.Params[i] != nil:
				b.WriteString(": " + ft.Params[i].String())
			case p.TypeAnnotation != nil:
				b.WriteString(": " + p.TypeAnnotation.TypeString())
			}
		}
		b.WriteString(")")
		switch {
		case ft != nil && ft.Return != nil && !isUnitLike(resolveTypeVar(ft.Return)):
			b.WriteString(" -> " + ft.Return.String())
		case ft == nil && v.ReturnTypeExpr != nil:
			b.WriteString(" -> " + v.ReturnTypeExpr.TypeString())
		}
		return b.String()
	case *ast.StructDef:
		return pub(v.Public) + "struct " + v.Name
	case *ast.EnumDef:
		return pub(v.Public) + "enum " + v.Name
	case *ast.TypeDef:
		return pub(v.Public) + "type " + v.Name
	case *ast.TypeAlias:
		return pub(v.Public) + "type " + v.Name
	case *ast.InterfaceDef:
		return pub(v.Public) + "interface " + v.Name
	case *ast.OnceBinding:
		return pub(v.Public) + "once " + v.Name
	}
	return kindOfItem(n) + " " + itemName(n)
}

func itemName(n ast.Node) string {
	if ds := topLevelNames(n); len(ds) > 0 {
		return ds[0].name
	}
	return ""
}

func kindOfItem(n ast.Node) string {
	switch n.(type) {
	case *ast.FuncDef, *ast.ExternFunc:
		return "fn"
	case *ast.OnceBinding:
		return "once"
	case *ast.StructDef:
		return "struct"
	case *ast.EnumDef:
		return "enum"
	case *ast.InterfaceDef:
		return "interface"
	}
	return "type"
}

func itemPublic(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.FuncDef:
		return v.Public
	case *ast.StructDef:
		return v.Public
	case *ast.EnumDef:
		return v.Public
	case *ast.TypeDef:
		return v.Public
	case *ast.TypeAlias:
		return v.Public
	case *ast.InterfaceDef:
		return v.Public
	case *ast.OnceBinding:
		return v.Public
	case *ast.ExternFunc:
		return v.Public
	case *ast.ExternType:
		return v.Public
	}
	return false
}

func isTypeItem(n ast.Node) bool {
	switch n.(type) {
	case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias, *ast.InterfaceDef, *ast.ExternType:
		return true
	}
	return false
}

// declarationText is kd as a message shows it: `greet.hello! say(name:
// String) -> Int`.
func declarationText(kd *ast.KeywordDecl) string {
	var b strings.Builder
	b.WriteString(kd.QualifiedKeyword() + " " + kd.Name)
	if kd.HasParams {
		b.WriteString("(" + paramsText(kd.Params) + ")")
	}
	if kd.ReturnTypeExpr != nil {
		b.WriteString(" -> " + kd.ReturnTypeExpr.TypeString())
	}
	return b.String()
}

func paramsText(params []ast.Param) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.Name
		if p.TypeAnnotation != nil {
			parts[i] += ": " + p.TypeAnnotation.TypeString()
		}
	}
	return strings.Join(parts, ", ")
}

// macroContractErrors are the ways e's expansion of its declaration
// breaks the contract (the comment at the top of this file), as errors at
// the declaration; generated are the nodes the expansion declares.
func macroContractErrors(fa *FileAnalysis, reg *TypeRegistry, e *macroExpansion, generated []ast.Node) []TypeError {
	kd := e.decl
	label := fmt.Sprintf("the macro `%s` (module %s) expanded `%s`", e.name, e.module, declarationText(kd))
	fail := func(msg string, hints ...string) []TypeError {
		te := TypeError{Line: kd.Line, Col: kd.Col, EndLine: kd.Line, EndCol: kd.KeywordEndCol(),
			Message: label + " " + msg + ", a defect in the macro"}
		for _, h := range hints {
			te = te.WithHint(h)
		}
		return []TypeError{te.WithHint("`nomi expand` prints what the declaration expands to")}
	}
	item := namedItem(generated, kd.Name)
	var fn *ast.FuncDef
	switch {
	case declaresType(kd):
		if item == nil || !isTypeItem(item) {
			return fail(fmt.Sprintf("to code that declares no type `%s`", kd.Name),
				"a declaration whose name is a type name declares that type")
		}
	case declaresFunction(kd):
		fn, _ = item.(*ast.FuncDef)
		if fn == nil {
			return fail(fmt.Sprintf("to code that declares no function `%s`", kd.Name),
				"a declaration that writes parameters or a result type declares the function it names")
		}
	case item == nil:
		return nil
	}
	var ft *FuncType
	if fn != nil {
		if sym := fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]; sym != nil {
			ft, _ = sym.Type.(*FuncType)
		}
	}
	header := itemHeader(item, ft)
	if pub := itemPublic(item); pub != kd.Public {
		if kd.Public {
			return fail(fmt.Sprintf("to `%s`: it should be `pub`, as the declaration is", header))
		}
		return fail(fmt.Sprintf("to `%s`: it should not be `pub`, since the declaration is not", header))
	}
	if fn == nil {
		return nil
	}
	// The declaration's parameters are the function's last ones.
	lead := len(fn.Params) - len(kd.Params)
	paramsMatch := lead >= 0
	for i := 0; paramsMatch && i < len(kd.Params); i++ {
		paramsMatch = fn.Params[lead+i].Name == kd.Params[i].Name
	}
	if !paramsMatch {
		want := "no parameters"
		if len(kd.Params) > 0 {
			want = fmt.Sprintf("`%s`", paramsText(kd.Params))
		}
		return fail(fmt.Sprintf("to `%s`: its parameters should end with the declaration's, %s", header, want),
			"a macro may add parameters before the declaration's, and none after them")
	}
	if ft == nil {
		return nil
	}
	declared := func(te ast.TypeExpr) (Type, bool) {
		if te == nil {
			return nil, false
		}
		t, err := ResolveDeclaredType(te, reg, nil, fa.References)
		return t, err == nil && t != nil
	}
	for i, p := range kd.Params {
		want, ok := declared(p.TypeAnnotation)
		if !ok || lead+i >= len(ft.Params) || ft.Params[lead+i] == nil {
			continue
		}
		if !typesEqualIn(want, ft.Params[lead+i], eqStrict) {
			return fail(fmt.Sprintf("to `%s`: its parameter `%s` should be `%s`", header, p.Name, p.TypeAnnotation.TypeString()))
		}
	}
	if want, ok := declared(kd.ReturnTypeExpr); ok {
		got := resolveTypeVar(ft.Return)
		if got == nil {
			got = TypeUnit
		}
		if !typesEqualIn(want, got, eqStrict) && !okTypeIs(got, want) {
			wrote := kd.ReturnTypeExpr.TypeString()
			return fail(fmt.Sprintf("to `%s`: its result should be `%s`", header, wrote),
				fmt.Sprintf("a macro may answer the declared result or a `Result` of it, `Result<%s, E>`", wrote))
		}
	}
	return nil
}

// recordMacroContracts records with each macro expansion among a file's
// nodes how it breaks the macro contract (macroContractErrors), where
// MacroFailed, MacroErrors and the checker read it. It runs once the
// file's signatures are typed (fileTyping.finish), with the registry they
// were typed with, so a build that types a file and checks none of its
// bodies (a document's own build of another file) knows it too.
func recordMacroContracts(fa *FileAnalysis, reg *TypeRegistry, nodes []ast.Node) {
	exps := macroExpansions(nodes)
	if len(exps) == 0 {
		return
	}
	generated := map[*macroExpansion][]ast.Node{}
	for _, n := range nodes {
		if e := generatedBy(n, exps); e != nil {
			generated[e] = append(generated[e], n)
		}
	}
	for _, e := range exps {
		errs := macroContractErrors(fa, reg, e, generated[e])
		e.contractMu.Lock()
		e.contract = errs
		e.contractMu.Unlock()
	}
}

// contractErrors are the errors the last check of the declaring file
// found in e's expansion against its declaration.
func (e *macroExpansion) contractErrors() []TypeError {
	e.contractMu.Lock()
	defer e.contractMu.Unlock()
	return e.contract
}

// okTypeIs reports whether t is a `Result` whose `Ok` type is want.
func okTypeIs(t, want Type) bool {
	et, ok := t.(*EnumType)
	return ok && et.Name == "Result" && (et.Origin == "std/results" || et.Origin == "") &&
		len(et.TypeArgs) == 2 && typesEqualIn(want, et.TypeArgs[0], eqStrict)
}
