package analysis

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// MACRO HYGIENE. A macro's expansion is parsed into the declaring file, so
// its top-level names join the file's and its code is resolved in the
// file's scope. Two rules keep that from going wrong silently:
//
//   - Names. A top-level name an expansion declares that the file declares
//     or imports too, or that another expansion in the file declares, is
//     an error at the declarations naming both sides
//     (checkExpansionCollisions). The expansion that collides declares
//     nothing (its declaration is declined), so the file's own declaration
//     keeps its meaning and the error is reported alone. Two files are two
//     scopes, so expansions in two files never collide.
//
//   - References. An expansion's code names only what it declares itself,
//     what its own imports bring (hidden under `<alias>$<name>`), the
//     prelude, and what the declaration writes: the names in its parameter
//     and result types, and a name of its body that the macro copied into
//     the code (a range its span map maps into the body). A bare name that
//     resolves to any other declaration or import of the file is an error
//     at the declaration (macroHygieneErrors): the macro reached the
//     file's names by accident, and would mean something else in a file
//     that declares them differently.

// declaredName is a top-level name a node declares, and where.
type declaredName struct {
	name      string
	line, col int
	// imported is set for a name an import binds.
	imported bool
}

// topLevelNames are the names n declares in its file's scope.
func topLevelNames(n ast.Node) []declaredName {
	one := func(name string, line, col int) []declaredName {
		if name == "" || strings.Contains(name, "$") {
			return nil
		}
		return []declaredName{{name: name, line: line, col: col}}
	}
	switch v := n.(type) {
	case *ast.FuncDef:
		return one(v.Name, v.Line, v.Col)
	case *ast.StructDef:
		return one(v.Name, v.Line, v.Col)
	case *ast.EnumDef:
		return one(v.Name, v.Line, v.Col)
	case *ast.TypeDef:
		return one(v.Name, v.Line, v.Col)
	case *ast.TypeAlias:
		return one(v.Name, v.Line, v.Col)
	case *ast.InterfaceDef:
		return one(v.Name, v.Line, v.Col)
	case *ast.OnceBinding:
		return one(v.Name, v.Line, v.Col)
	case *ast.ExternFunc:
		return one(v.Name, v.Line, v.Col)
	case *ast.ExternType:
		return one(v.Name, v.Line, v.Col)
	case *ast.MacroDecl:
		return one(v.Name, v.NameLine, v.NameCol)
	case *ast.ImportStmt:
		return importBindings(v)
	case *ast.ImportBlock:
		var out []declaredName
		for _, e := range v.Entries {
			out = append(out, importBindings(e)...)
		}
		return out
	}
	return nil
}

// importBindings are the names stmt binds in its file: each selected name
// or its alias, and the file (or type) a whole import names, under its
// alias. An import the front end adds (the prelude's) binds nothing a
// file wrote.
func importBindings(stmt *ast.ImportStmt) []declaredName {
	if IsSynthesizedLine(stmt.Line) {
		return nil
	}
	at := func(n ast.Node) (int, int) {
		switch v := n.(type) {
		case *ast.Ident:
			return v.Line, v.Col
		case *ast.TypeIdent:
			return v.Line, v.Col
		}
		return stmt.Line, stmt.Col
	}
	var out []declaredName
	add := func(n ast.Node) {
		name := ast.ImportNodeName(n)
		if name == "" || strings.Contains(name, "$") {
			return
		}
		line, col := at(n)
		out = append(out, declaredName{name: name, line: line, col: col, imported: true})
	}
	for i, n := range stmt.Names {
		if i < len(stmt.Aliases) && stmt.Aliases[i] != nil {
			add(stmt.Aliases[i])
		} else {
			add(n)
		}
	}
	if len(stmt.Names) == 0 || stmt.IncludeParent {
		if stmt.ModuleAlias != nil {
			add(stmt.ModuleAlias)
		} else if len(stmt.ModulePath) > 0 {
			add(stmt.ModulePath[len(stmt.ModulePath)-1])
		}
	}
	return out
}

// generatedBy is the macro expansion among exps that wrote n, a top-level
// node of the file, or nil for a node the file wrote. The function a
// declaration names sits at the declaration's name, so it is told by its
// Expansion; every other node an expansion wrote is in its band.
func generatedBy(n ast.Node, exps map[int]*macroExpansion) *macroExpansion {
	if fn, ok := n.(*ast.FuncDef); ok && fn.Expansion != nil {
		if e, ok := fn.Expansion.Detail().(*macroExpansion); ok {
			return e
		}
	}
	return expansionAt(exps, "", n.LineNum())
}

// fileNameSide is who declares a name of a file: the file itself
// (expansion nil), or a macro expansion.
type fileNameSide struct {
	declaredName
	expansion *macroExpansion
}

// describe says where the side declares the name, for a message: "this
// file declares on line 8", "this file imports on line 3", "`gen! one`
// declares on line 6".
func (s fileNameSide) describe() string {
	switch {
	case s.expansion != nil:
		kd := s.expansion.decl
		return fmt.Sprintf("`%s %s` on line %d declares", kd.QualifiedKeyword(), kd.Name, kd.Line)
	case s.imported:
		return fmt.Sprintf("this file imports on line %d", s.line)
	}
	return fmt.Sprintf("this file declares on line %d", s.line)
}

// checkExpansionCollisions reports each top-level name a macro expansion
// among nodes declares that the file, or an earlier expansion, declares
// too, and answers nodes without the code of every expansion that
// collides: that declaration is declined, so its errors are reported
// alone, and the file's own declaration keeps its meaning.
func checkExpansionCollisions(nodes []ast.Node) []ast.Node {
	exps := macroExpansions(nodes)
	if len(exps) == 0 {
		return nodes
	}
	own := map[string]declaredName{}
	var order []*macroExpansion
	byExp := map[*macroExpansion][]declaredName{}
	for _, n := range nodes {
		if e := generatedBy(n, exps); e != nil {
			if _, seen := byExp[e]; !seen {
				order = append(order, e)
			}
			byExp[e] = append(byExp[e], topLevelNames(n)...)
			continue
		}
		if kd, ok := n.(*ast.KeywordDecl); ok && macroExpansionOf(kd) != nil {
			continue
		}
		if line := n.LineNum(); line <= 0 || IsSynthesizedLine(line) {
			continue
		}
		for _, d := range topLevelNames(n) {
			if _, ok := own[d.name]; !ok {
				own[d.name] = d
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].decl.Line < order[j].decl.Line })
	earlier := map[string]*macroExpansion{}
	dropped := map[*macroExpansion]bool{}
	for _, e := range order {
		kd := e.decl
		label := fmt.Sprintf("`%s` (module %s)", e.name, e.module)
		for _, d := range byExp[e] {
			if d.imported {
				continue
			}
			beside := ""
			if d.name != kd.Name {
				beside = fmt.Sprintf("the macro %s declares `%s` beside `%s`", label, d.name, kd.Name)
			}
			if mine, ok := own[d.name]; ok {
				verb := "declared"
				if mine.imported {
					verb = "imported"
				}
				by := "by"
				if mine.imported {
					by = "declared by"
				}
				te := TypeError{Line: mine.line, Col: mine.col, EndLine: mine.line, EndCol: mine.col + len(d.name),
					Message: fmt.Sprintf("`%s` is %s here and %s `%s %s` on line %d", d.name, verb, by, kd.QualifiedKeyword(), kd.Name, kd.Line)}
				if beside != "" {
					te = te.WithHint(beside + ", so rename this one")
				} else {
					te = te.WithHint("rename one of them")
				}
				e.errs = append(e.errs, te)
				dropped[e] = true
				continue
			}
			if first := earlier[d.name]; first != nil && first != e {
				fk := first.decl
				te := TypeError{Line: kd.Line, Col: kd.Col, EndLine: kd.Line, EndCol: kd.KeywordEndCol(),
					Message: fmt.Sprintf("`%s` is declared by `%s %s` here and by `%s %s` on line %d", d.name, kd.QualifiedKeyword(), kd.Name, fk.QualifiedKeyword(), fk.Name, fk.Line)}
				switch {
				case d.name != kd.Name && d.name != fk.Name:
					te = te.WithHint(fmt.Sprintf("the macro %s declares `%s` for each declaration, so two of them in one file collide: a defect in the macro, which can name it after the declaration (`%s_%s`)", label, d.name, kd.Name, d.name))
				default:
					te = te.WithHint("rename one of them")
				}
				e.errs = append(e.errs, te)
				dropped[e] = true
				continue
			}
		}
		if !dropped[e] {
			for _, d := range byExp[e] {
				if _, ok := earlier[d.name]; !ok && !d.imported {
					earlier[d.name] = e
				}
			}
		}
	}
	if len(dropped) == 0 {
		return nodes
	}
	out := make([]ast.Node, 0, len(nodes))
	for _, n := range nodes {
		if e := generatedBy(n, exps); e != nil && dropped[e] {
			continue
		}
		out = append(out, n)
	}
	for e := range dropped {
		e.dropped, e.declined = true, true
	}
	return out
}

// identifierAt is the identifier that starts at byte offset off of text,
// and whether the byte before it is a `.`: a member, not a bare name.
func identifierAt(text string, off int) (name string, member bool) {
	end := off
	for end < len(text) {
		c := text[end]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '?' || c == '!' {
			end++
			continue
		}
		break
	}
	return text[off:end], off > 0 && text[off-1] == '.'
}

var wordPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// writtenNames are the names kd's parameter and result types write.
func writtenNames(kd *ast.KeywordDecl) map[string]bool {
	out := map[string]bool{}
	add := func(t ast.TypeExpr) {
		if t == nil {
			return
		}
		for _, w := range wordPattern.FindAllString(t.TypeString(), -1) {
			out[w] = true
		}
	}
	for _, p := range kd.Params {
		add(p.TypeAnnotation)
	}
	add(kd.ReturnTypeExpr)
	return out
}

// fromBody reports whether the band position line and col of e's code is
// in a range the macro mapped into the declaration's body, and the body
// writes name there: the user's own text, copied.
func (e *macroExpansion) fromBody(line, col int, name string) bool {
	kd := e.decl
	if kd.Body == nil {
		return false
	}
	m, ok := e.spanAt(line, col)
	if !ok || m.Span.Line <= 0 || filepath.ToSlash(filepath.Clean(m.Span.File)) != e.file {
		return false
	}
	sl, _ := kd.BodyPosition(0)
	el, _ := kd.BodyPosition(len(kd.Body.Value))
	if m.Span.Line < sl || m.Span.Line > el {
		return false
	}
	for _, w := range wordPattern.FindAllString(kd.Body.Value, -1) {
		if w == name {
			return true
		}
	}
	return false
}

// macroHygieneErrors are the errors of the bare names the code of each
// macro expansion among nodes names that resolve to a declaration or an
// import of the file other than its own, and that the declaration does
// not write: one per name and expansion, at the declaration. A name in a
// value is told by what it resolves to (fa's references), so a local of
// the code that shares a name with the file is the code's own; a name in
// a type is read from the code's type expressions, where only a type
// parameter of the code can stand for something else.
func macroHygieneErrors(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	exps := macroExpansions(nodes)
	if len(exps) == 0 {
		return nil
	}
	names := map[string]fileNameSide{}
	own := map[*macroExpansion]map[string]bool{}
	generated := map[*macroExpansion][]ast.Node{}
	for _, n := range nodes {
		e := generatedBy(n, exps)
		if e == nil {
			if kd, ok := n.(*ast.KeywordDecl); ok && macroExpansionOf(kd) != nil {
				continue
			}
			if line := n.LineNum(); line <= 0 || IsSynthesizedLine(line) {
				continue
			}
		} else {
			generated[e] = append(generated[e], n)
			if own[e] == nil {
				own[e] = map[string]bool{}
			}
		}
		for _, d := range topLevelNames(n) {
			if e != nil {
				if d.imported {
					continue
				}
				own[e][d.name] = true
			}
			if _, ok := names[d.name]; !ok {
				names[d.name] = fileNameSide{declaredName: d, expansion: e}
			}
		}
	}
	type finding struct {
		e    *macroExpansion
		name string
		pos  Pos
	}
	found := map[string]finding{}
	written := map[*macroExpansion]map[string]bool{}
	// consider records name, at band position pos of e's code, when it is
	// a name of the file the code may not reach.
	consider := func(e *macroExpansion, name string, pos Pos) {
		if name == "" || strings.Contains(name, "$") || own[e][name] {
			return
		}
		side, ok := names[name]
		if !ok || side.expansion == e {
			return
		}
		if written[e] == nil {
			written[e] = writtenNames(e.decl)
		}
		if written[e][name] || e.fromBody(pos.Line, pos.Col, name) {
			return
		}
		key := fmt.Sprintf("%d\x00%s", e.base, name)
		if f, ok := found[key]; ok && (f.pos.Line < pos.Line || f.pos.Line == pos.Line && f.pos.Col < pos.Col) {
			return
		}
		found[key] = finding{e: e, name: name, pos: pos}
	}
	for pos, sym := range fa.References {
		if sym == nil || !IsSynthesizedLine(pos.Line) {
			continue
		}
		e := expansionAt(exps, "", pos.Line)
		if e == nil || strings.Contains(sym.Name, "$") {
			continue
		}
		switch sym.Kind {
		case SymbolParam, SymbolBinding, SymbolField, SymbolEnumVariant, SymbolArgHint, SymbolInterfaceMethod:
			continue
		}
		if sym.Pos.Line >= e.base && sym.Pos.Line < e.base+synthLineStride && (sym.SourceFile == "" || sym.SourceFile == fa.FilePath) {
			// Something the expansion's own code declares.
			continue
		}
		name, member := identifierAt(e.source, byteOffset(e.source, pos.Line-e.base, pos.Col))
		if !member {
			consider(e, name, pos)
		}
	}
	for e, ns := range generated {
		params := map[string]bool{}
		members := map[ast.Node]bool{}
		for _, n := range ns {
			ast.Inspect(n, func(m ast.Node) bool {
				switch v := m.(type) {
				case *ast.FuncDef:
					addTypeParams(params, v.TypeParams)
				case *ast.StructDef:
					addTypeParams(params, v.TypeParams)
				case *ast.EnumDef:
					addTypeParams(params, v.TypeParams)
				case *ast.InterfaceDef:
					addTypeParams(params, v.TypeParams)
				}
				return true
			})
		}
		for _, n := range ns {
			ast.Inspect(n, func(m ast.Node) bool {
				switch v := m.(type) {
				case *ast.QualifiedType:
					members[v.Member] = true
					consider(e, v.Module, Pos{Line: v.ModuleLine, Col: v.ModuleCol})
				case *ast.SimpleType:
					if !members[v] && !params[v.Name] {
						consider(e, v.Name, Pos{Line: v.Line, Col: v.Col})
					}
				case *ast.GenericType:
					if !members[v] && !params[v.Name] {
						consider(e, v.Name, Pos{Line: v.Line, Col: v.Col})
					}
				}
				return true
			})
		}
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := found[keys[i]], found[keys[j]]
		if a.e.decl.Line != b.e.decl.Line {
			return a.e.decl.Line < b.e.decl.Line
		}
		return a.pos.Line < b.pos.Line || a.pos.Line == b.pos.Line && a.pos.Col < b.pos.Col
	})
	var out []TypeError
	for _, k := range keys {
		f := found[k]
		kd := f.e.decl
		te := TypeError{Line: kd.Line, Col: kd.Col, EndLine: kd.Line, EndCol: kd.KeywordEndCol(),
			Message: fmt.Sprintf("the macro `%s` (module %s) expanded `%s %s` to code that names `%s`, which %s: a macro's code names only what it declares, what its own imports bring and what the declaration writes, a defect in the macro",
				f.e.name, f.e.module, kd.QualifiedKeyword(), kd.Name, f.name, names[f.name].describe())}
		te = te.WithHint("the macro imports what its code needs at the top of the expansion (`import std/strings`) and names it through that import")
		te = te.WithHint("`nomi expand` prints what the declaration expands to")
		out = append(out, te)
	}
	return out
}

func addTypeParams(into map[string]bool, params []ast.TypeParam) {
	for _, p := range params {
		into[p.Name] = true
	}
}
